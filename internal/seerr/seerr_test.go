package seerr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAPIKeyStays(t *testing.T) {
	dir := t.TempDir()
	first, err := EnsureAPIKey(dir)
	if err != nil || len(first) < 32 {
		t.Fatalf("key %q, %v", first, err)
	}
	second, err := EnsureAPIKey(dir)
	if err != nil || second != first {
		t.Fatalf("key changed: %q, %v", second, err)
	}
	info, err := os.Stat(filepath.Join(dir, "api.key"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Errorf("key is readable by others: %o", info.Mode().Perm())
	}
}

func TestSetupUsesJellyfinAndEnablesLibraries(t *testing.T) {
	var logins int
	var sawInit bool
	var libs []library
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "key" && r.URL.Path != "/api/v1/auth/jellyfin" {
			t.Errorf("%s missing API key", r.URL.Path)
		}
		switch {
		case r.URL.Path == "/api/v1/settings/public":
			serverType := 0
			if logins > 0 || sawInit {
				serverType = mediaServerJellyfin
			}
			_ = json.NewEncoder(w).Encode(publicSettings{Initialized: sawInit, MediaServerType: serverType})
		case r.URL.Path == "/api/v1/auth/jellyfin":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["serverType"] != float64(mediaServerJellyfin) || body["hostname"] != JellyfinHost || body["username"] != "admin" {
				t.Errorf("login body = %#v", body)
			}
			logins++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":1}`))
		case r.URL.Path == "/api/v1/settings/jellyfin" && r.Method == http.MethodPost:
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["externalHostname"] != "http://192.168.1.9:8097" {
				t.Errorf("external = %#v", body)
			}
		case r.URL.Path == "/api/v1/settings/main":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["applicationUrl"] != "http://192.168.1.9:8097/seerr" || body["locale"] != "de" {
				t.Errorf("main = %#v", body)
			}
		case r.URL.Path == "/api/v1/settings/initialize":
			sawInit = true
			_ = json.NewEncoder(w).Encode(map[string]bool{"initialized": true})
		case r.URL.Path == "/api/v1/settings/jellyfin/library/sync":
			libs = []library{{ID: "m 1", Name: "Movies", Enabled: false}, {ID: "s", Name: "Shows", Enabled: true}}
			_ = json.NewEncoder(w).Encode(libs)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v1/settings/jellyfin/library/"):
			if r.URL.EscapedPath() != "/api/v1/settings/jellyfin/library/m%201" {
				t.Errorf("enabled path %s", r.URL.EscapedPath())
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	err := (Setup{
		URL: srv.URL, APIKey: "key",
		JellyfinHost: JellyfinHost, JellyfinPort: 18096,
		JellyfinUser: "admin", JellyfinPassword: "secret",
		JellyfinPublicURL: "http://192.168.1.9:8097/",
		ApplicationURL:    "http://192.168.1.9:8097/seerr",
		Language:          "de",
	}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if logins != 1 || !sawInit {
		t.Fatalf("logins %d init %v", logins, sawInit)
	}

	err = (Setup{URL: srv.URL, APIKey: "key"}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if logins != 1 {
		t.Errorf("second run signed in again: %d", logins)
	}
}

func TestLoginAlreadyConfiguredContinues(t *testing.T) {
	step := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/settings/public":
			typ := 4
			if step > 0 {
				typ = mediaServerJellyfin
			}
			_ = json.NewEncoder(w).Encode(publicSettings{Initialized: step > 1, MediaServerType: typ})
		case "/api/v1/auth/jellyfin":
			step = 1
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"Jellyfin hostname already configured"}`))
		case "/api/v1/settings/initialize":
			step = 2
			_ = json.NewEncoder(w).Encode(map[string]bool{"initialized": true})
		case "/api/v1/settings/jellyfin/library/sync":
			_ = json.NewEncoder(w).Encode([]library{})
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	err := (Setup{URL: srv.URL, APIKey: "key", JellyfinUser: "admin", JellyfinPassword: "pw", JellyfinHost: "127.0.0.1", JellyfinPort: 18096}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
}
