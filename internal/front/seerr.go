package front

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

// seerrMount is the path Seerr is served under. Seerr has no base-path setting, so responses
// are rewritten. The same rewrite is what the project's own reverse-proxy notes describe.
const seerrMount = "/seerr"

// Paths are longest first so a shorter prefix cannot cut a longer one.
var seerrPaths = []string{
	"/login/plex/loading",
	"/site.webmanifest",
	"/imageproxy/",
	"/avatarproxy/",
	"/api/v1",
	"/android-",
	"/images/",
	"/favicon",
	"/_next",
	"/apple-",
	"/login",
	"/setup",
	"/logo_",
}

// passThrough forwards the request path unchanged. Fladder is already configured for /fladder.
func passThrough(target *url.URL) http.Handler {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
		},
		FlushInterval: -1,
	}
}

func seerrProxy(target *url.URL) http.Handler {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			rest := strings.TrimPrefix(pr.In.URL.Path, seerrMount)
			if rest == "" || rest[0] != '/' {
				rest = "/" + strings.TrimPrefix(rest, "/")
			}
			pr.Out.URL.Path = rest
			pr.Out.URL.RawPath = ""
			pr.SetURL(target)
			pr.Out.Host = target.Host
			// A compressed body cannot be rewritten, so the browser would keep asking for
			// /_next at the site root and the stylesheet would never apply.
			pr.Out.Header.Del("Accept-Encoding")
		},
		ModifyResponse: rewriteSeerr,
		FlushInterval:  -1,
	}
}

func rewriteSeerr(res *http.Response) error {
	if loc := res.Header.Get("Location"); loc != "" {
		res.Header.Set("Location", prefixSeerrLocation(loc))
	}
	if res.StatusCode == http.StatusSwitchingProtocols || res.Body == nil || res.Header.Get("Content-Encoding") != "" {
		return nil
	}
	ctype := strings.ToLower(res.Header.Get("Content-Type"))
	if !seerrText(ctype) {
		return nil
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if err := res.Body.Close(); err != nil {
		return err
	}
	body = []byte(prefixSeerrBody(string(body)))
	res.Body = io.NopCloser(bytes.NewReader(body))
	res.ContentLength = int64(len(body))
	res.Header.Set("Content-Length", strconv.Itoa(len(body)))
	res.Header.Del("ETag")
	return nil
}

func seerrText(ctype string) bool {
	for _, kind := range []string{"text/html", "text/css", "javascript", "json", "text/plain"} {
		if strings.Contains(ctype, kind) {
			return true
		}
	}
	return false
}

func prefixSeerrLocation(loc string) string {
	u, err := url.Parse(loc)
	if err != nil || u.Path == "" {
		return loc
	}
	if !strings.HasPrefix(u.Path, "/") {
		return loc
	}
	path := prefixSeerrPath(u.Path)
	if u.Host == "" && u.Scheme == "" {
		u.Path = path
		return u.String()
	}
	// An absolute address points at the private Seerr port. Keep the browser on this port.
	return (&url.URL{Path: path, RawQuery: u.RawQuery, Fragment: u.Fragment}).String()
}

func prefixSeerrPath(p string) string {
	if p == seerrMount || strings.HasPrefix(p, seerrMount+"/") {
		return p
	}
	if p == "/" {
		return seerrMount + "/"
	}
	return seerrMount + p
}

func prefixSeerrBody(s string) string {
	s = strings.ReplaceAll(s, `href="/"`, `href="/seerr/"`)
	s = strings.ReplaceAll(s, `href:"/"`, `href:"/seerr/"`)
	for _, path := range seerrPaths {
		s = replaceSeerrPath(s, path)
	}
	return s
}

func replaceSeerrPath(s, path string) string {
	var b strings.Builder
	for len(s) > 0 {
		i := strings.Index(s, path)
		if i < 0 {
			b.WriteString(s)
			break
		}
		if !seerrBoundary(s, i) || seerrAlready(s, i) {
			b.WriteString(s[:i+len(path)])
			s = s[i+len(path):]
			continue
		}
		b.WriteString(s[:i])
		b.WriteString(seerrMount)
		b.WriteString(path)
		s = s[i+len(path):]
	}
	return b.String()
}

func seerrAlready(s string, i int) bool {
	return i >= len(seerrMount) && s[i-len(seerrMount):i] == seerrMount
}

func seerrBoundary(s string, i int) bool {
	if i == 0 {
		return true
	}
	switch s[i-1] {
	case '"', '\'', '`', '(', '=', ' ', '\n', '\t', ':':
		return true
	default:
		return false
	}
}
