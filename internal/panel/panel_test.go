package panel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/setup"
	"github.com/Aryan-mor/jellyfin-telegram/internal/webui"
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
		Bots: func() []LiveBot {
			return []LiveBot{{ID: 111, Name: "reader", OK: true, Admin: true, ChannelTitle: "Films"}}
		},
		URLs: func() []string { return []string{"http://192.168.1.4:8097", "http://100.122.54.9:8097"} },
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, path
}

func TestPageRoutesSurviveRefresh(t *testing.T) {
	srv, _ := serve(t, nil, nil)
	res, err := http.Get(srv.URL + "/panel/bots")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("this computer /panel/bots: %d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "<div id=\"root\">") && !strings.Contains(string(body), "id=\"root\"") {
		t.Fatalf("page was not HTML: %s", body)
	}
	away := httptest.NewRequest(http.MethodGet, "/panel/bots", nil)
	away.RemoteAddr = "203.0.113.8:4000"
	rec := httptest.NewRecorder()
	srv.Config.Handler.ServeHTTP(rec, away)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("other computer: %d", rec.Code)
	}
	with := httptest.NewRequest(http.MethodGet, "/panel/channel/mirrors?code=secret", nil)
	with.RemoteAddr = "203.0.113.8:4000"
	rec = httptest.NewRecorder()
	srv.Config.Handler.ServeHTTP(rec, with)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "root") {
		t.Fatalf("coded page: %d %s", rec.Code, rec.Body.String())
	}
	// Same path as a screen, but a fetch still gets JSON, not the page.
	api := httptest.NewRequest(http.MethodGet, "/panel/local?code=secret", nil)
	api.RemoteAddr = "203.0.113.8:4000"
	api.Header.Set("Accept", "*/*")
	rec = httptest.NewRecorder()
	srv.Config.Handler.ServeHTTP(rec, api)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Header().Get("Content-Type"), "json") {
		t.Fatalf("api list: %d %s %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	doc := httptest.NewRequest(http.MethodGet, "/panel/local", nil)
	doc.RemoteAddr = "127.0.0.1:9"
	doc.Header.Set("Accept", "text/html,application/xhtml+xml")
	rec = httptest.NewRecorder()
	srv.Config.Handler.ServeHTTP(rec, doc)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("document /panel/local: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
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
	if body["channel_title"] != "Films" {
		t.Fatalf("channel_title = %v", body["channel_title"])
	}
	page, err := http.Get(srv.URL + "/panel?code=secret")
	if err != nil {
		t.Fatal(err)
	}
	defer page.Body.Close()
	html, _ := io.ReadAll(page.Body)
	text := string(html)
	if !strings.Contains(text, "secret") || strings.Contains(text, "__PANEL_CODE__") {
		t.Fatalf("the access code was not injected: %s", text)
	}
	if !strings.Contains(text, "/ui/") {
		t.Fatalf("page does not load the built UI: %s", text)
	}
}

func TestStateOmitsTheBuiltInAPI(t *testing.T) {
	config.DefaultAPIID, config.DefaultAPIHash = "12345", "abcdef"
	t.Cleanup(func() { config.DefaultAPIID, config.DefaultAPIHash = "", "" })
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
	if _, ok := body["api_id"]; ok {
		t.Fatalf("the built-in api_id was sent to the browser: %v", body["api_id"])
	}
	if body["default_api"] != true {
		t.Fatalf("default_api = %v", body["default_api"])
	}
}

func TestReplacingTheDefaultAPINeedsBoth(t *testing.T) {
	config.DefaultAPIID, config.DefaultAPIHash = "12345", "abcdef"
	t.Cleanup(func() { config.DefaultAPIID, config.DefaultAPIHash = "", "" })
	srv, path := serve(t, func(context.Context, int, string, int64, []string) (setup.Report, error) {
		return setup.Report{Bots: []setup.BotReport{{Name: "@reader", OK: true}}}, nil
	}, nil)
	res, err := http.Post(srv.URL+"/panel/settings?code=secret", "application/json", strings.NewReader(`{"api_id":"99999"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d", res.StatusCode)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "TELEGRAM_API_ID=12345") || !strings.Contains(string(saved), "TELEGRAM_API_HASH=abcdef") {
		t.Fatalf("file = %s", saved)
	}
	res, err = http.Post(srv.URL+"/panel/settings?code=secret", "application/json", strings.NewReader(`{"api_id":"99999","api_hash":"newhash"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	saved, _ = os.ReadFile(path)
	if strings.Contains(string(saved), "abcdef") || !strings.Contains(string(saved), "TELEGRAM_API_ID=99999") || !strings.Contains(string(saved), "TELEGRAM_API_HASH=newhash") {
		t.Fatalf("file = %s", saved)
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
	if !setup.TrustedClient(local) {
		t.Fatal("this computer was refused")
	}
	for _, header := range []string{"X-Forwarded-For", "Forwarded", "X-Real-Ip"} {
		tunnel := httptest.NewRequest(http.MethodPost, "/panel/downloads/login", nil)
		tunnel.RemoteAddr = "127.0.0.1:5000"
		tunnel.Header.Set(header, "203.0.113.9")
		if setup.TrustedClient(tunnel) {
			t.Errorf("a request relayed with %s passed the login check", header)
		}
	}
}

func TestPageHasTheThreeStepSignInModal(t *testing.T) {
	text := webui.Source()
	for _, want := range []string{"Your Telegram phone number", "Enter the code", "Two-step verification", "Add account"} {
		if !strings.Contains(text, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
}

func TestStateListsHowToSignInToEachApp(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(envFile), 0o600); err != nil {
		t.Fatal(err)
	}
	h := New(Options{Code: "secret", EnvPath: path, Accounts: func() []Account {
		return []Account{{App: "Jellyfin", Path: "/", User: "admin", Password: "pw"}, {App: "Fladder", Path: "/fladder/"}}
	}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panel/state?code=secret", nil))
	var got struct {
		Accounts []Account `json:"accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Accounts) != 2 || got.Accounts[0].User != "admin" || got.Accounts[0].Password != "pw" || got.Accounts[1].User != "" {
		t.Fatalf("accounts %+v", got.Accounts)
	}
	if !strings.Contains(webui.Source(), "Sign in details") {
		t.Error("the page has no Sign in details page")
	}
}

func TestLocalFilesTab(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(envFile), 0o600); err != nil {
		t.Fatal(err)
	}
	var did []string
	h := New(Options{
		Code: "secret", EnvPath: path,
		Local: func(context.Context) (any, error) { return map[string]any{"categories": []any{}}, nil },
		LocalDo: func(_ *http.Request, action string, body map[string]any) (any, error) {
			did = append(did, action)
			return map[string]any{"done": 1}, nil
		},
	})
	call := func(method, target, remote, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// The tab is advertised in the state, and its list needs the code like everything else.
	var st struct {
		Local bool `json:"local"`
	}
	if err := json.Unmarshal(call(http.MethodGet, "/panel/state?code=secret", "127.0.0.1:1", "").Body.Bytes(), &st); err != nil || !st.Local {
		t.Fatalf("state does not offer the tab: %v %+v", err, st)
	}
	if rec := call(http.MethodGet, "/panel/local", "127.0.0.1:1", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("list without the code: %d", rec.Code)
	}
	if rec := call(http.MethodGet, "/panel/local?code=secret", "192.168.1.9:1", ""); rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}

	// Uploading and deleting work from any machine that has the code.
	for _, action := range []string{"upload", "cancel", "delete", "delete_synced", "scan"} {
		if rec := call(http.MethodPost, "/panel/local/"+action+"?code=secret", "192.168.1.9:1", `{"ids":[1]}`); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", action, rec.Code, rec.Body.String())
		}
	}
	if len(did) != 5 {
		t.Fatalf("actions run: %v", did)
	}
	// Which folders of this computer may be read is for this computer or Tailscale only.
	if rec := call(http.MethodPost, "/panel/local/folder?code=secret", "192.168.1.9:1", `{"add":"D:\x"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("folder from the LAN: %d", rec.Code)
	}
	if rec := call(http.MethodPost, "/panel/local/folder?code=secret", "127.0.0.1:9", `{"add":"D:\x"}`); rec.Code != http.StatusOK {
		t.Fatalf("folder from this computer: %d", rec.Code)
	}
	if rec := call(http.MethodPost, "/panel/local/format?code=secret", "127.0.0.1:9", `{}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown action: %d", rec.Code)
	}
	if rec := call(http.MethodPost, "/panel/local/upload", "127.0.0.1:9", `{}`); rec.Code != http.StatusForbidden {
		t.Fatalf("post without the code: %d", rec.Code)
	}
}

func TestPageHasTheLocalFilesTab(t *testing.T) {
	text := webui.Source()
	for _, want := range []string{"Local files", "Upload everything that is only here", "Delete every local copy that is in Telegram", "/panel/local/"} {
		if !strings.Contains(text, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
}

func TestChannelsTab(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(envFile), 0o600); err != nil {
		t.Fatal(err)
	}
	var did []string
	h := New(Options{
		Code: "secret", EnvPath: path,
		Sources: func(context.Context) (any, error) { return map[string]any{"sources": []any{}}, nil },
		SourceDo: func(_ *http.Request, action string, _ map[string]any) (any, error) {
			did = append(did, action)
			return nil, nil
		},
	})
	call := func(method, target, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.RemoteAddr = "192.168.1.9:1"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	var st struct {
		Sources bool `json:"sources"`
	}
	if err := json.Unmarshal(call(http.MethodGet, "/panel/state?code=secret", "").Body.Bytes(), &st); err != nil || !st.Sources {
		t.Fatalf("state does not offer the tab: %v %+v", err, st)
	}
	if rec := call(http.MethodGet, "/panel/sources", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("list without the code: %d", rec.Code)
	}
	if rec := call(http.MethodGet, "/panel/sources?code=secret", ""); rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}
	for _, action := range []string{"add", "remove", "pause", "settings", "check", "rescan"} {
		if rec := call(http.MethodPost, "/panel/sources/"+action+"?code=secret", `{}`); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", action, rec.Code, rec.Body.String())
		}
	}
	if len(did) != 6 {
		t.Fatalf("actions run: %v", did)
	}
	if rec := call(http.MethodPost, "/panel/sources/wipe?code=secret", `{}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown action: %d", rec.Code)
	}
	if rec := call(http.MethodPost, "/panel/sources/news_phone?code=secret", `{}`); rec.Code != http.StatusNotFound {
		t.Fatalf("dead news login: %d", rec.Code)
	}
	if rec := call(http.MethodPost, "/panel/sources/add", `{}`); rec.Code != http.StatusForbidden {
		t.Fatalf("post without the code: %d", rec.Code)
	}
}

func TestPageHasTheChannelsAndProviderTabs(t *testing.T) {
	text := webui.Source()
	for _, want := range []string{"Direct files", "News channels", "@id_bot", "@getidsbot", "/panel/sources/", "Telegram account", "General video", "Create mirror", "Subtitles", "Tailscale", "Add a bot", "/newbot", "does not offer a public ID", "You're using the default. Replace it here if you want your own.", "https://github.com/DonutWare/Fladder/releases"} {
		if !strings.Contains(text, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
	if strings.Contains(text, "Coming soon") || strings.Contains(text, "Bot provider") {
		t.Error("the bot provider tab is still in the page")
	}
}

func TestCreateJobsNeedThisComputer(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(envFile), 0o600); err != nil {
		t.Fatal(err)
	}
	var title string
	h := New(Options{
		Code: "secret", EnvPath: path,
		MirrorCreate: func(r *http.Request, body map[string]any) (any, error) {
			title, _ = body["title"].(string)
			return map[string]any{"ok": true}, nil
		},
		BotCreate: func(_ *http.Request, action string, body map[string]any) (any, error) {
			if action == "reuse" {
				return nil, ErrBusy
			}
			if int(body["count"].(float64)) != 2 {
				t.Fatalf("count = %v", body["count"])
			}
			return map[string]any{"ok": true, "restart": true}, nil
		},
		Job: func() JobView {
			return JobView{Running: true, Progress: []JobLine{{Text: "Starting", State: "wait"}}}
		},
	})
	call := func(method, target, addr, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := call(http.MethodPost, "/panel/mirrors/create?code=secret", "192.168.1.9:1", `{"title":"Films"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("create from the LAN: %d", rec.Code)
	}
	if rec := call(http.MethodPost, "/panel/mirrors/create?code=secret", "127.0.0.1:9", `{"title":"Films"}`); rec.Code != http.StatusOK {
		t.Fatalf("create here: %d %s", rec.Code, rec.Body.String())
	}
	if title != "Films" {
		t.Fatalf("title = %q", title)
	}
	if rec := call(http.MethodPost, "/panel/bots/create?code=secret", "127.0.0.1:9", `{"count":2}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"restart":true`) {
		t.Fatalf("bot create: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(http.MethodPost, "/panel/bots/reuse?code=secret", "127.0.0.1:9", `{"usernames":["a_bot"]}`); rec.Code != http.StatusConflict {
		t.Fatalf("reuse while busy: %d", rec.Code)
	}
	if rec := call(http.MethodGet, "/panel/jobs?code=secret", "192.168.1.9:1", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"running":true`) {
		t.Fatalf("job: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRestartNeedsThisComputer(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(envFile), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls int
	h := New(Options{
		Code: "secret", EnvPath: path,
		Restart: func() error {
			calls++
			return nil
		},
	})
	call := func(addr string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/panel/restart?code=secret", strings.NewReader(`{}`))
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := call("192.168.1.9:1"); rec.Code != http.StatusForbidden {
		t.Fatalf("restart from the LAN: %d", rec.Code)
	}
	if rec := call("127.0.0.1:9"); rec.Code != http.StatusOK || calls != 1 {
		t.Fatalf("restart here: %d calls=%d %s", rec.Code, calls, rec.Body.String())
	}
	gone := New(Options{Code: "secret", EnvPath: path})
	req := httptest.NewRequest(http.MethodPost, "/panel/restart?code=secret", strings.NewReader(`{}`))
	req.RemoteAddr = "127.0.0.1:9"
	rec := httptest.NewRecorder()
	gone.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("restart without a hook: %d", rec.Code)
	}
}

func TestUpdateInstallNeedsThisComputer(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(envFile), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls int
	h := New(Options{
		Code: "secret", EnvPath: path,
		ApplyUpdate: func(context.Context) error {
			calls++
			return nil
		},
	})
	call := func(addr string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/panel/update?code=secret", strings.NewReader(`{}`))
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := call("192.168.1.9:1"); rec.Code != http.StatusForbidden {
		t.Fatalf("install from the LAN: %d", rec.Code)
	}
	if rec := call("127.0.0.1:9"); rec.Code != http.StatusOK || calls != 1 {
		t.Fatalf("install here: %d calls=%d %s", rec.Code, calls, rec.Body.String())
	}
	gone := New(Options{Code: "secret", EnvPath: path})
	req := httptest.NewRequest(http.MethodPost, "/panel/update?code=secret", strings.NewReader(`{}`))
	req.RemoteAddr = "127.0.0.1:9"
	rec := httptest.NewRecorder()
	gone.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("install without a hook: %d", rec.Code)
	}
}
