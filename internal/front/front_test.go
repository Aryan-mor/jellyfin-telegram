package front

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const streamBase = "http://127.0.0.1:8097"

type seen struct {
	host, forwardedFor, forwardedProto, acceptEncoding string
}

func rig(t *testing.T) (*httptest.Server, *seen) {
	t.Helper()
	got := &seen{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.host = r.Host
		got.forwardedFor = r.Header.Get("X-Forwarded-For")
		got.forwardedProto = r.Header.Get("X-Forwarded-Proto")
		got.acceptEncoding = r.Header.Get("Accept-Encoding")
		switch {
		case strings.HasSuffix(r.URL.Path, "/PlaybackInfo"):
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("ETag", "old")
			fmt.Fprintf(w, `{"MediaSources":[{"Path":"%s/s/token/5/1","Other":"%s/other"}]}`, streamBase, streamBase)
		case r.URL.Path == "/web/app.js":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `"%s/s/untouched"`, streamBase)
		case r.URL.Path == "/plain":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprintf(w, "%s/s/untouched", streamBase)
		default:
			fmt.Fprint(w, "jellyfin")
		}
	}))
	t.Cleanup(upstream.Close)
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	gateway := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "gateway:"+r.URL.Path) })
	setup := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "setup") })
	panel := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "panel:"+r.URL.Path) })
	srv := httptest.NewServer(Handler(Options{Gateway: gateway, Setup: setup, Panel: panel, Jellyfin: target, StreamBase: streamBase}))
	t.Cleanup(srv.Close)
	return srv, got
}

func body(t *testing.T, res *http.Response) string {
	t.Helper()
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestOnePortRoutesEverything(t *testing.T) {
	srv, _ := rig(t)
	for path, want := range map[string]string{
		"/s/token/5/1": "gateway:/s/token/5/1",
		"/healthz":     "gateway:/healthz",
		"/setup":       "setup",
		"/setup/x":     "setup",
		"/panel":       "panel:/panel",
		"/panel/state": "panel:/panel/state",
		"/web/index":   "jellyfin",
	} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		if got := body(t, res); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
}

func TestStreamAddressFollowsTheAddressTheClientUsed(t *testing.T) {
	srv, got := rig(t)
	for _, host := range []string{"192.168.1.20:8097", "jellyfin.example.com"} {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/Items/1/PlaybackInfo", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		req.Header.Set("Accept-Encoding", "gzip")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if res.Header.Get("ETag") != "" {
			t.Error("the ETag of the original body was kept")
		}
		var out struct {
			MediaSources []struct{ Path, Other string }
		}
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if want := "http://" + host + "/s/token/5/1"; out.MediaSources[0].Path != want {
			t.Errorf("path = %q, want %q", out.MediaSources[0].Path, want)
		}
		if !strings.HasPrefix(out.MediaSources[0].Other, streamBase) {
			t.Errorf("an unrelated address changed: %q", out.MediaSources[0].Other)
		}
		if got.acceptEncoding != "" {
			t.Errorf("compression stayed on: %q", got.acceptEncoding)
		}
		if got.host != host {
			t.Errorf("Jellyfin saw host %q", got.host)
		}
	}
}

func TestTunnelSchemeIsTrustedOnlyFromThisMachine(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "tv.example.com"
	r.Header.Set("X-Forwarded-Proto", "https")

	r.RemoteAddr = "127.0.0.1:5555"
	if got := clientBase(r); got != "https://tv.example.com" {
		t.Errorf("tunnel on this machine: %q", got)
	}
	r.RemoteAddr = "203.0.113.9:5555"
	if got := clientBase(r); got != "http://tv.example.com" {
		t.Errorf("a remote client set the scheme: %q", got)
	}
}

func TestOnlyApiAnswersAreRewritten(t *testing.T) {
	srv, _ := rig(t)
	for _, path := range []string{"/web/app.js", "/plain"} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		if got := body(t, res); !strings.Contains(got, streamBase+"/s/untouched") {
			t.Errorf("%s was changed: %q", path, got)
		}
	}
	for path, want := range map[string]bool{
		"/Items/1/PlaybackInfo": true, "/Users/u/Items": true,
		"/Videos/1/PlaybackInfo": true, "/Videos/1/stream": false, "/web/index.html": false, "/socket": false,
	} {
		if got := rewritable(path); got != want {
			t.Errorf("rewritable(%s) = %v", path, got)
		}
	}
}

func TestForwardedClientAddress(t *testing.T) {
	srv, got := rig(t)
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Forwarded-For", "198.51.100.7")
	req.Header.Set("X-Forwarded-Proto", "https")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	// The test client connects over loopback, like a tunnel on the same machine.
	if got.forwardedFor != "198.51.100.7" || got.forwardedProto != "https" {
		t.Errorf("forwarded for %q, proto %q", got.forwardedFor, got.forwardedProto)
	}
}

func TestWebSocketUpgradePassesThrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			t.Error("upgrade header was dropped")
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
		line, _ := rw.ReadString('\n')
		_, _ = rw.WriteString("echo:" + line)
		_ = rw.Flush()
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(Handler(Options{Gateway: http.NotFoundHandler(), Jellyfin: target, StreamBase: streamBase}))
	defer srv.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprint(conn, "GET /socket HTTP/1.1\r\nHost: x\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(status, "101") {
		t.Fatalf("status %q, %v", status, err)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	fmt.Fprint(conn, "hello\n")
	line, err := reader.ReadString('\n')
	if err != nil || line != "echo:hello\n" {
		t.Errorf("line %q, %v", line, err)
	}
}

func TestSwitchChangesHandlerAtRuntime(t *testing.T) {
	var s Switch
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("before set: %d", rec.Code)
	}
	s.Set(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("after set: %d", rec.Code)
	}
}

func TestExternalSubtitleGetsARealAddress(t *testing.T) {
	in := `{"MediaSources":[{"Id":"abc","MediaStreams":[` +
		`{"Index":0,"Type":"Video"},` +
		`{"Index":3,"Codec":"subrip","DeliveryMethod":"External","DeliveryUrl":"D:\\Library\\x.fas.srt"},` +
		`{"Index":4,"Codec":"ass","DeliveryMethod":"External","DeliveryUrl":"/Videos/1/abc/Subtitles/4/0/Stream.ass"}]}]}`
	out := string(fixSubtitleURLs("/Items/1-2/PlaybackInfo", []byte(in)))
	if !strings.Contains(out, `"/Videos/1-2/abc/Subtitles/3/0/Stream.srt"`) {
		t.Errorf("the file path was kept: %s", out)
	}
	if !strings.Contains(out, `"/Videos/1/abc/Subtitles/4/0/Stream.ass"`) {
		t.Errorf("a good address changed: %s", out)
	}
	if got := string(fixSubtitleURLs("/Items/1-2/Images", []byte(in))); got != in {
		t.Error("an unrelated answer was rewritten")
	}
}
