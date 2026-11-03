package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aryan-mor/jellyfin-telegram/internal/setup"
)

const envFile = `TELEGRAM_API_ID=12345
TELEGRAM_API_HASH=abcdef
TELEGRAM_CHANNEL_ID=1234567890
TELEGRAM_BOT_TOKENS=111:secret-aaaa,222:secret-bbbb
SUBTITLE_LANGUAGE=en
`

func serve(t *testing.T, verify setup.Verifier, same func(int64) error) (*httptest.Server, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(envFile), 0o600); err != nil {
		t.Fatal(err)
	}
	h := New(Options{
		Code: "secret", EnvPath: path, Verify: verify, SameChannel: same,
		Bots: func() []LiveBot { return []LiveBot{{ID: 111, Name: "reader", OK: true, Admin: true}} },
		URLs: func() []string { return []string{"http://192.168.1.4:8097", "http://100.122.54.9:8097"} },
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, path
}

func TestPageRequiresTheConsoleCode(t *testing.T) {
	srv, _ := serve(t, nil, nil)
	res, err := http.Get(srv.URL + "/panel")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("this computer: status %d", res.StatusCode)
	}
	away := httptest.NewRequest(http.MethodGet, "/panel", nil)
	away.RemoteAddr = "203.0.113.8:4000"
	rec := httptest.NewRecorder()
	srv.Config.Handler.ServeHTTP(rec, away)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("other computer: status %d", rec.Code)
	}
	post := httptest.NewRequest(http.MethodPost, "/panel/bots", strings.NewReader(`{"add":"1:x"}`))
	post.RemoteAddr = "127.0.0.1:9"
	rec = httptest.NewRecorder()
	srv.Config.Handler.ServeHTTP(rec, post)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("post without code: status %d", rec.Code)
	}
}

func TestStateHidesTokensAndMarksTailscale(t *testing.T) {
	srv, _ := serve(t, nil, nil)
	res, err := http.Get(srv.URL + "/panel/state?code=secret")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "secret-aaaa") || strings.Contains(string(raw), "abcdef") {
		t.Fatalf("state leaked a secret: %s", raw)
	}
	if !strings.Contains(string(raw), "aaaa") || !strings.Contains(string(raw), "@reader") {
		t.Fatalf("state = %s", raw)
	}
	page, err := http.Get(srv.URL + "/panel?code=secret")
	if err != nil {
		t.Fatal(err)
	}
	defer page.Body.Close()
	html := make([]byte, 40000)
	n, _ := page.Body.Read(html)
	text := string(html[:n])
	for _, want := range []string{"Tailscale", "Add a bot", "/newbot", "does not offer a public ID", `href="/"`, "https://github.com/DonutWare/Fladder/releases"} {
		if !strings.Contains(text, want) {
			t.Fatalf("page is missing %q", want)
		}
	}
}

func TestAddBotIsCheckedBeforeItIsSaved(t *testing.T) {
	var seen int
	srv, path := serve(t, func(_ context.Context, id int, hash string, channel int64, tokens []string) (setup.Report, error) {
		seen = len(tokens)
		if id != 12345 || hash != "abcdef" || channel != 1234567890 {
			t.Errorf("verify got %d %q %d", id, hash, channel)
		}
		for _, token := range tokens {
			if strings.HasPrefix(token, "333:") {
				return setup.Report{Bots: []setup.BotReport{{Name: "@new", Error: "is not an admin of the channel"}}}, nil
			}
		}
		return setup.Report{Bots: []setup.BotReport{{Name: "@new", OK: true}}}, nil
	}, func(int64) error { return nil })

	res, err := http.Post(srv.URL+"/panel/bots?code=secret", "application/json", strings.NewReader(`{"add":"333:secret-cccc"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || seen != 3 {
		t.Fatalf("status %d, bots checked %d", res.StatusCode, seen)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "333:secret-cccc") {
		t.Fatal("a rejected bot was written")
	}

	res, err = http.Post(srv.URL+"/panel/bots?code=secret", "application/json", strings.NewReader(`{"add":"444:secret-dddd"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	saved, _ = os.ReadFile(path)
	if !strings.Contains(string(saved), "444:secret-dddd") || strings.Contains(string(saved), "333:secret-cccc") {
		t.Fatalf("file = %s", saved)
	}
}

func TestSavingTheAPIKeepsTheLanguage(t *testing.T) {
	srv, path := serve(t, func(context.Context, int, string, int64, []string) (setup.Report, error) {
		return setup.Report{Bots: []setup.BotReport{{Name: "@reader", OK: true}}}, nil
	}, nil)
	res, err := http.Post(srv.URL+"/panel/settings?code=secret", "application/json", strings.NewReader(`{"api_id":"12345"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "SUBTITLE_LANGUAGE=en") || !strings.Contains(string(saved), "111:secret-aaaa") {
		t.Fatalf("file = %s", saved)
	}
}

func TestADifferentChannelIsRefused(t *testing.T) {
	srv, path := serve(t, func(context.Context, int, string, int64, []string) (setup.Report, error) {
		t.Fatal("verify should not run")
		return setup.Report{}, nil
	}, func(int64) error { return os.ErrInvalid })
	res, err := http.Post(srv.URL+"/panel/settings?code=secret", "application/json", strings.NewReader(`{"channel":"https://t.me/c/999/1"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d", res.StatusCode)
	}
	saved, _ := os.ReadFile(path)
	if strings.Contains(string(saved), "999") {
		t.Fatal("channel was changed")
	}
}

func TestLoginIsLocalOrTailscaleOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(envFile), 0o600); err != nil {
		t.Fatal(err)
	}
	h := New(Options{
		Code: "secret", EnvPath: path,
		DownloadDo: func(*http.Request, string, map[string]any) (any, error) {
			t.Fatal("login should not run")
			return nil, nil
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/panel/downloads/login?code=secret", strings.NewReader(`{"phone":"+1"}`))
	req.RemoteAddr = "192.168.1.9:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestEnableNeedsASession(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(envFile), 0o600); err != nil {
		t.Fatal(err)
	}
	h := New(Options{
		Code: "secret", EnvPath: path,
		DownloadDo: func(_ *http.Request, action string, _ map[string]any) (any, error) {
			if action == "enable" {
				return nil, os.ErrInvalid
			}
			return map[string]any{"ok": true}, nil
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/panel/automation?code=secret", strings.NewReader(`{"enabled":true}`))
	req.RemoteAddr = "127.0.0.1:9"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestATunnelOnThisMachineIsNotTrusted(t *testing.T) {
	local := httptest.NewRequest(http.MethodPost, "/panel/downloads/login", nil)
	local.RemoteAddr = "127.0.0.1:5000"
	if !trustedClient(local) {
		t.Fatal("this computer was refused")
	}
	for _, header := range []string{"X-Forwarded-For", "Forwarded", "X-Real-Ip"} {
		tunnel := httptest.NewRequest(http.MethodPost, "/panel/downloads/login", nil)
		tunnel.RemoteAddr = "127.0.0.1:5000"
		tunnel.Header.Set(header, "203.0.113.9")
		if trustedClient(tunnel) {
			t.Errorf("a request relayed with %s passed the login check", header)
		}
	}
}
