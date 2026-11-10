package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joho/godotenv"
)

var good = Values{
	APIID: " 12345 ", APIHash: "abcdef", Channel: "https://t.me/c/4325588340/5",
	Bots: "111:aaa\r\n222:bbb\n", Language: "ger",
}

func rig(t *testing.T, verify Verifier) (*Wizard, *httptest.Server, string) {
	t.Helper()
	if verify == nil {
		verify = func(context.Context, int, string, int64, []string) (Report, error) { return Report{}, nil }
	}
	env := filepath.Join(t.TempDir(), ".env")
	w, err := New(env, verify)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(w.Handler())
	t.Cleanup(srv.Close)
	return w, srv, env
}

func post(t *testing.T, url string, v Values) *http.Response {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestEveryRouteNeedsTheCode(t *testing.T) {
	w, srv, env := rig(t, nil)
	for _, path := range []string{"/setup", "/setup/status", "/setup?code=wrong"} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("%s gave %d", path, res.StatusCode)
		}
	}
	res := post(t, srv.URL+"/setup/save", good)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("save without a code gave %d", res.StatusCode)
	}
	if _, err := os.Stat(env); err == nil {
		t.Error("settings were written without the code")
	}
	res, err := http.Get(srv.URL + "/setup?code=" + w.Code())
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("with the code: %d", res.StatusCode)
	}
}

func TestSaveWritesWhatTheServiceReads(t *testing.T) {
	w, srv, env := rig(t, nil)
	res := post(t, srv.URL+"/setup/save?code="+w.Code(), good)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	select {
	case <-w.Saved():
	default:
		t.Error("saving was not signalled")
	}
	got, err := godotenv.Read(env)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"TELEGRAM_API_ID": "12345", "TELEGRAM_API_HASH": "abcdef", "TELEGRAM_CHANNEL_ID": "4325588340",
		"TELEGRAM_BOT_TOKENS": "111:aaa,222:bbb", "SUBTITLE_LANGUAGE": "de",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestSaveRecordsThePublicMirror(t *testing.T) {
	w, srv, env := rig(t, nil)
	v := good
	v.Mirror = true
	v.MirrorTitle = " Films ab12cd "
	v.MirrorUsername = "@films_ab12cd"
	res := post(t, srv.URL+"/setup/save?code="+w.Code(), v)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	got, err := godotenv.Read(env)
	if err != nil {
		t.Fatal(err)
	}
	if got["MIRROR_SETUP"] != "true" || got["MIRROR_TITLE"] != "Films ab12cd" || got["MIRROR_USERNAME"] != "films_ab12cd" || got["MIRROR_CHANNEL"] != "" {
		t.Fatalf("mirror settings = %q %q %q %q", got["MIRROR_SETUP"], got["MIRROR_TITLE"], got["MIRROR_USERNAME"], got["MIRROR_CHANNEL"])
	}
}

func TestSaveRecordsAnExistingMirror(t *testing.T) {
	w, srv, env := rig(t, nil)
	v := good
	v.Mirror = true
	v.MirrorChannel = " https://t.me/films "
	res := post(t, srv.URL+"/setup/save?code="+w.Code(), v)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	got, err := godotenv.Read(env)
	if err != nil {
		t.Fatal(err)
	}
	if got["MIRROR_SETUP"] != "true" || got["MIRROR_CHANNEL"] != "https://t.me/films" || got["MIRROR_TITLE"] != "" || got["MIRROR_USERNAME"] != "" {
		t.Fatalf("mirror settings = %q %q %q %q", got["MIRROR_SETUP"], got["MIRROR_CHANNEL"], got["MIRROR_TITLE"], got["MIRROR_USERNAME"])
	}
}

func TestBadValuesGetAUsefulMessageAndSaveNothing(t *testing.T) {
	w, srv, env := rig(t, nil)
	bad := []func(*Values){
		func(v *Values) { v.APIID = "abc" },
		func(v *Values) { v.APIHash = "" },
		func(v *Values) { v.Channel = "my channel" },
		func(v *Values) { v.Bots = "not-a-token" },
		func(v *Values) { v.Language = "klingon language" },
		func(v *Values) { v.JellyfinURL = "http://x" },
	}
	for _, change := range bad {
		v := good
		change(&v)
		res := post(t, srv.URL+"/setup/save?code="+w.Code(), v)
		var out map[string]string
		_ = json.NewDecoder(res.Body).Decode(&out)
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest || out["error"] == "" {
			t.Errorf("%+v: status %d, %v", v, res.StatusCode, out)
		}
	}
	if _, err := os.Stat(env); err == nil {
		t.Error("bad values were written")
	}
}

func TestVerifyErrorsAreShownAndNeverEchoSecrets(t *testing.T) {
	w, srv, _ := rig(t, func(_ context.Context, id int, hash string, channel int64, tokens []string) (Report, error) {
		if id != 12345 || hash != "abcdef" || channel != 4325588340 || len(tokens) != 2 {
			t.Errorf("verifier got %d %q %d %v", id, hash, channel, tokens)
		}
		return Report{}, errors.New("no bot can connect")
	})
	res := post(t, srv.URL+"/setup/verify?code="+w.Code(), good)
	defer res.Body.Close()
	var out map[string]string
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusBadRequest || out["error"] != "no bot can connect" {
		t.Errorf("status %d, %v", res.StatusCode, out)
	}
}

func TestStatusReachesThePage(t *testing.T) {
	w, srv, _ := rig(t, nil)
	w.SetStatus(Status{Stage: "Downloading Jellyfin", Percent: 40})
	res, err := http.Get(srv.URL + "/setup/status?code=" + w.Code())
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var s Status
	if err := json.NewDecoder(res.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if s.Stage != "Downloading Jellyfin" || s.Percent != 40 {
		t.Errorf("status %+v", s)
	}
}

func TestWaitingPanelStaysQuiet(t *testing.T) {
	w, _, _ := rig(t, nil)
	srv := httptest.NewServer(w.Waiting())
	defer srv.Close()
	res, err := http.Get(srv.URL + "/panel/state?code=" + w.Code())
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("state %d", res.StatusCode)
	}
	var state map[string]any
	if err := json.NewDecoder(res.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if _, ok := state["bots"].([]any); !ok {
		t.Fatalf("bots %#v", state["bots"])
	}
	if state["starting"] != true {
		t.Fatalf("starting %#v", state["starting"])
	}
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/panel", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/html")
	page, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer page.Body.Close()
	if page.StatusCode != http.StatusOK || !strings.Contains(page.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("page %d %s", page.StatusCode, page.Header.Get("Content-Type"))
	}
}

func TestWaitingOnlyServesSetup(t *testing.T) {
	w, _, _ := rig(t, nil)
	srv := httptest.NewServer(w.Waiting())
	defer srv.Close()
	res, err := http.Get(srv.URL + "/web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status %d", res.StatusCode)
	}
	res, err = http.Get(srv.URL + "/setup?code=" + w.Code())
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("setup status %d", res.StatusCode)
	}
}

func TestUpdateEnvKeepsEverythingElse(t *testing.T) {
	for name, nl := range map[string]string{"unix": "\n", "windows": "\r\n"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			before := strings.Join([]string{"# a comment", "KEEP=1", "", "API=old", "# API=commented"}, nl) + nl
			if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := UpdateEnv(path, map[string]string{"API": "new", "ADDED": "2"}); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := strings.Join([]string{"# a comment", "KEEP=1", "", "API=new", "# API=commented", "ADDED=2"}, nl) + nl
			if string(raw) != want {
				t.Errorf("got %q, want %q", raw, want)
			}
		})
	}
	if err := UpdateEnv(filepath.Join(t.TempDir(), "x"), map[string]string{"A": "b\nC=d"}); err == nil {
		t.Error("a value with a line break could inject another setting")
	}
}
