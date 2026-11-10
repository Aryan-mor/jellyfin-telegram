package front

import (
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
