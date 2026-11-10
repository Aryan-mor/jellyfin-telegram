package front

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSeerrStaysUnderItsPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login" {
			t.Errorf("upstream path %s", r.URL.Path)
		}
		if r.Header.Get("Accept-Encoding") != "" {
			t.Errorf("compressed body would skip the rewrite: %q", r.Header.Get("Accept-Encoding"))
		}
		w.Header().Set("Location", "/setup")
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<a href="/">home</a><script src="/_next/static/a.js"></script>https://image.tmdb.org/login`))
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	gateway := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	srv := httptest.NewServer(Handler(Options{Gateway: gateway, Seerr: target}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/seerr/login", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.Header.Get("Location") != "/seerr/setup" {
		t.Errorf("location %q", res.Header.Get("Location"))
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	for _, want := range []string{`href="/seerr/"`, `src="/seerr/_next/static/a.js"`, `https://image.tmdb.org/login`} {
		if !strings.Contains(got, want) {
			t.Errorf("body missing %s: %s", want, got)
		}
	}
	if strings.Contains(got, `src="/_next`) {
		t.Errorf("unprefixed asset: %s", got)
	}

	noredirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err = noredirect.Get(srv.URL + "/seerr")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusTemporaryRedirect || res.Header.Get("Location") != "/seerr/" {
		t.Errorf("redirect %d %q", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestFladderSeerrAPILandsOnSeerr(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/me" {
			t.Errorf("upstream path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	jellyfin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("jellyfin saw %s", r.URL.Path)
	}))
	defer jellyfin.Close()
	jellyfinURL, err := url.Parse(jellyfin.URL)
	if err != nil {
		t.Fatal(err)
	}
	gateway := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	srv := httptest.NewServer(Handler(Options{Gateway: gateway, Jellyfin: jellyfinURL, Seerr: target}))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/v1/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || string(body) != `{"id":1}` {
		t.Errorf("status %d body %s", res.StatusCode, body)
	}
}

func TestSeerrWaitingPageUntilReady(t *testing.T) {
	passed := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { passed++; w.WriteHeader(http.StatusOK) })
	state := SeerrState{Text: "Unpacking Seerr. 40% <b>"}
	h := seerrGate(func() SeerrState { return state }, next)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/seerr/", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusServiceUnavailable || passed != 0 {
		t.Fatalf("code %d, passed %d", rec.Code, passed)
	}
	for _, want := range []string{"Seerr is starting", "Unpacking Seerr. 40% &lt;b&gt;", `http-equiv="refresh"`, "few minutes"} {
		if !strings.Contains(body, want) {
			t.Errorf("page has no %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "<b>") {
		t.Error("the status text was not escaped")
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After")
	}

	state = SeerrState{Ready: true}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/seerr/", nil))
	if rec.Code != http.StatusOK || passed != 1 {
		t.Errorf("ready: code %d, passed %d", rec.Code, passed)
	}
}

func TestSeerrFailurePageDoesNotKeepReloading(t *testing.T) {
	h := seerrGate(func() SeerrState { return SeerrState{Failed: true, Text: "port in use"} }, http.NotFoundHandler())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/seerr/", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "could not start") || !strings.Contains(body, "port in use") || strings.Contains(body, "refresh") {
		t.Errorf("page:\n%s", body)
	}
}

func TestNoSeerrStateMeansAlwaysReady(t *testing.T) {
	next := http.NotFoundHandler()
	if got := seerrGate(nil, next); fmt.Sprintf("%p", got) != fmt.Sprintf("%p", next) {
		t.Error("a nil state should not wrap the proxy")
	}
}
