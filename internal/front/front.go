// Package front serves everything from one port: the streams, the setup page and Jellyfin. One
// port means one address to remember, and one thing to expose when using it away from home.
package front

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
)

// Switch lets the running server change what it serves, from the setup page to the real thing.
type Switch struct{ h atomic.Pointer[http.Handler] }

// Set replaces the handler.
func (s *Switch) Set(h http.Handler) { s.h.Store(&h) }

// ServeHTTP passes the request on to the current handler.
func (s *Switch) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := s.h.Load()
	if h == nil {
		http.Error(w, "starting", http.StatusServiceUnavailable)
		return
	}
	(*h).ServeHTTP(w, r)
}

// Options configures the front.
type Options struct {
	// Gateway serves streams and health checks.
	Gateway http.Handler
	// Setup serves the setup page. It may be nil.
	Setup http.Handler
	// Panel serves the settings page. It may be nil.
	Panel http.Handler
	// UI serves the built panel and setup scripts. It may be nil.
	UI http.Handler
	// Jellyfin is the server behind this one. Nil serves streams only.
	Jellyfin *url.URL
	// Seerr is the request app, served under /seerr/. Nil leaves that path to Jellyfin.
	Seerr *url.URL
	// SeerrState says how far Seerr is. Seerr starts in the background and takes minutes the first
	// time, so until it is ready /seerr/ shows that instead of an error. Nil means always ready.
	SeerrState func() SeerrState
	// StreamBase is the address written into .strm files.
	StreamBase string
}

type clientBaseKey struct{}

// Handler builds the front.
//
// Jellyfin reads a .strm itself using StreamBase, which for a private server is a loopback address.
// It also tells clients that address so they can read directly, and from any other machine it
// leads nowhere. So every API answer has it replaced with the address that client used to get here,
// which works at home, on a phone away from home, and through a tunnel, with no setting to get right.
func Handler(o Options) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/s/", o.Gateway)
	mux.Handle("/healthz", o.Gateway)
	if o.Setup != nil {
		mux.Handle("/setup", o.Setup)
		mux.Handle("/setup/", o.Setup)
	}
	if o.Panel != nil {
		mux.Handle("/panel", o.Panel)
		mux.Handle("/panel/", o.Panel)
	}
	if o.UI != nil {
		mux.Handle("/ui/", o.UI)
	}
	if o.Seerr != nil {
		proxy := seerrGate(o.SeerrState, seerrProxy(o.Seerr))
		mux.Handle("/seerr/", proxy)
		mux.HandleFunc("/seerr", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/seerr/", http.StatusTemporaryRedirect)
		})
	}
	if o.Jellyfin == nil {
		return mux
	}
	// Compression is off on this loopback hop, because the body has to stay readable to be rewritten.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(o.Jellyfin)
			pr.SetXForwarded()
			pr.Out.Host = pr.In.Host
			trustLocalProxy(pr)
			if rewritable(pr.In.URL.Path) {
				pr.Out.Header.Del("Accept-Encoding")
				base := clientBase(pr.In)
				pr.Out = pr.Out.WithContext(context.WithValue(pr.Out.Context(), clientBaseKey{}, base))
			}
		},
		ModifyResponse: func(res *http.Response) error {
			return rewriteBody(res, o.StreamBase)
		},
		FlushInterval: -1,
	}
	mux.Handle("/", proxy)
	return mux
}

// rewritable leaves out media and web assets, which never hold a stream address and can be huge.
func rewritable(path string) bool {
	lower := strings.ToLower(path)
	for _, prefix := range []string{"/web/", "/videos/", "/audio/", "/socket"} {
		if strings.HasPrefix(lower, prefix) {
			return strings.HasSuffix(lower, "/playbackinfo")
		}
	}
	return true
}

func rewriteBody(res *http.Response, streamBase string) error {
	base, _ := res.Request.Context().Value(clientBaseKey{}).(string)
	if base == "" || streamBase == "" || !strings.Contains(res.Header.Get("Content-Type"), "json") ||
		res.Header.Get("Content-Encoding") != "" {
		return nil
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if err := res.Body.Close(); err != nil {
		return err
	}
	body = bytes.ReplaceAll(body, []byte(streamBase+"/s/"), []byte(base+"/s/"))
	body = fixSubtitleURLs(res.Request.URL.Path, body)
	res.Body = io.NopCloser(bytes.NewReader(body))
	res.ContentLength = int64(len(body))
	res.Header.Set("Content-Length", strconv.Itoa(len(body)))
	res.Header.Del("ETag")
	return nil
}

// clientBase is the address the client used. Only a proxy on this machine, such as a tunnel, can
// claim a different scheme. From anyone else the header is ignored.
func clientBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if fromLoopback(r) && r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return (&url.URL{Scheme: scheme, Host: r.Host}).String()
}

// trustLocalProxy keeps what a tunnel on this machine reports about the real client, which Jellyfin
// needs to tell local viewers from remote ones. SetXForwarded alone would make every tunnelled
// viewer look like this machine.
func trustLocalProxy(pr *httputil.ProxyRequest) {
	if !fromLoopback(pr.In) {
		return
	}
	if v := pr.In.Header.Get("X-Forwarded-Proto"); v != "" {
		pr.Out.Header.Set("X-Forwarded-Proto", v)
	}
	if v := pr.In.Header.Get("X-Forwarded-For"); v != "" {
		pr.Out.Header.Set("X-Forwarded-For", v)
	}
}

func fromLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// fixSubtitleURLs repairs a Jellyfin 10.11 bug. When a client's profile lists the format "subrip",
// which Android TV does, an external subtitle gets its file path as DeliveryUrl instead of the
// /Videos/.../Subtitles/... address. The client then asks for a path like /D:/Library/x.srt, gets
// 404, and playback stalls until the player gives up on the track. Writing the address Jellyfin
// itself uses for every other profile fixes it for any client.
func fixSubtitleURLs(path string, body []byte) []byte {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 3 || !strings.EqualFold(parts[0], "items") || !strings.EqualFold(parts[2], "playbackinfo") ||
		!bytes.Contains(body, []byte(`"DeliveryUrl"`)) {
		return body
	}
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if dec.Decode(&doc) != nil {
		return body
	}
	changed := false
	sources, _ := doc["MediaSources"].([]any)
	for _, src := range sources {
		source, _ := src.(map[string]any)
		sourceID, _ := source["Id"].(string)
		streams, _ := source["MediaStreams"].([]any)
		for _, st := range streams {
			stream, _ := st.(map[string]any)
			delivery, _ := stream["DeliveryUrl"].(string)
			index, ok := stream["Index"].(json.Number)
			if !ok || sourceID == "" || stream["DeliveryMethod"] != "External" || delivery == "" ||
				strings.HasPrefix(delivery, "/") || strings.Contains(delivery, "://") {
				continue
			}
			ext, _ := stream["Codec"].(string)
			if ext = strings.ToLower(ext); ext == "subrip" || ext == "" {
				ext = "srt"
			}
			stream["DeliveryUrl"] = "/Videos/" + parts[1] + "/" + sourceID + "/Subtitles/" + index.String() + "/0/Stream." + ext
			changed = true
		}
	}
	if !changed {
		return body
	}
	fixed, err := json.Marshal(doc)
	if err != nil {
		return body
	}
	return fixed
}
