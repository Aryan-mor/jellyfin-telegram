package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"

	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
)

type fakeAuto struct {
	signed    bool
	apiID     int
	existing  AutoFound
	created   AutoFound
	createErr error
	stopped   bool
}

func (f *fakeAuto) BeginLogin(_ context.Context, apiID int, _, _ string) (AutoLogin, error) {
	f.apiID = apiID
	return AutoLogin{Step: "code"}, nil
}
func (f *fakeAuto) SubmitCode(context.Context, string) (AutoLogin, error) {
	f.signed = true
	return AutoLogin{Step: "done"}, nil
}
func (f *fakeAuto) SubmitPassword(context.Context, string) (AutoLogin, error) {
	return AutoLogin{Step: "done"}, nil
}
func (f *fakeAuto) CancelLogin()   {}
func (f *fakeAuto) SignedIn() bool { return f.signed }
func (f *fakeAuto) Stop()          { f.stopped = true }
func (f *fakeAuto) Existing(context.Context, string) (AutoFound, error) {
	return f.existing, nil
}
func (f *fakeAuto) Create(_ context.Context, o AutoCreate, progress func(string)) (AutoFound, error) {
	progress(fmt.Sprintf("Creating bot 1 of %d", o.Bots))
	return f.created, f.createErr
}

func autoRig(t *testing.T, a Auto) (*Wizard, *httptest.Server, string) {
	t.Helper()
	w, srv, env := rig(t, nil)
	w.SetAuto(a)
	return w, srv, env
}

func call(t *testing.T, w *Wizard, srv *httptest.Server, method, path string, body any) (int, string) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, srv.URL+path+"?code="+w.Code(), rdr)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(out)
}

func TestAutoIsOffWithoutAnAdapter(t *testing.T) {
	w, srv, _ := rig(t, nil)
	code, _ := call(t, w, srv, "POST", "/setup/auto/login", map[string]string{"phone": "+1"})
	if code != http.StatusNotFound {
		t.Fatalf("got %d", code)
	}
	_, body := call(t, w, srv, "GET", "/setup/auto/info", nil)
	if !strings.Contains(body, `"available":false`) {
		t.Fatalf("info: %s", body)
	}
}

func TestAutoRoutesNeedTheCode(t *testing.T) {
	_, srv, _ := autoRig(t, &fakeAuto{})
	res, err := http.Post(srv.URL+"/setup/auto/login?code=wrong", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("got %d", res.StatusCode)
	}
}

func TestAutoLoginNeedsAnApiWhenTheBuildHasNone(t *testing.T) {
	config.DefaultAPIID, config.DefaultAPIHash = "", ""
	w, srv, _ := autoRig(t, &fakeAuto{})
	code, body := call(t, w, srv, "POST", "/setup/auto/login", map[string]string{"phone": "+4915112345678"})
	if code != http.StatusBadRequest || !strings.Contains(body, "built-in") {
		t.Fatalf("got %d %s", code, body)
	}
	code, _ = call(t, w, srv, "POST", "/setup/auto/login", map[string]string{"phone": "+1", "api_id": "12x", "api_hash": "h"})
	if code != http.StatusBadRequest {
		t.Fatalf("a bad api_id was accepted: %d", code)
	}
}

func TestAutoFlowSavesWithoutTokensCrossingTheBrowser(t *testing.T) {
	config.DefaultAPIID, config.DefaultAPIHash = "4242", "builtinhash"
	t.Cleanup(func() { config.DefaultAPIID, config.DefaultAPIHash = "", "" })
	fake := &fakeAuto{existing: AutoFound{ChannelID: 777, Title: "Mine", Tokens: []string{"111:secret-one", "222:secret-two"}, Bots: []string{"a_bot", "b_bot"}}}
	w, srv, env := autoRig(t, fake)

	// Saving before the setup finished is refused.
	code, _ := call(t, w, srv, "POST", "/setup/save", Values{Auto: true})
	if code != http.StatusBadRequest {
		t.Fatalf("saved too early: %d", code)
	}

	if code, body := call(t, w, srv, "POST", "/setup/auto/login", map[string]string{"phone": "+4915112345678"}); code != 200 || !strings.Contains(body, `"code"`) {
		t.Fatalf("login: %d %s", code, body)
	}
	if fake.apiID != 4242 {
		t.Fatalf("the built-in api_id was not used: %d", fake.apiID)
	}
	call(t, w, srv, "POST", "/setup/auto/login", map[string]string{"code": "12345"})

	code, body := call(t, w, srv, "POST", "/setup/auto/existing", map[string]string{"channel": "t.me/c/777/1"})
	if code != 200 || strings.Contains(body, "secret") {
		t.Fatalf("existing: %d %s", code, body)
	}

	if code, body := call(t, w, srv, "POST", "/setup/save", Values{Auto: true, Language: "de"}); code != 200 {
		t.Fatalf("save: %d %s", code, body)
	}
	got, err := godotenv.Read(env)
	if err != nil {
		t.Fatal(err)
	}
	if got["TELEGRAM_API_ID"] != "4242" || got["TELEGRAM_API_HASH"] != "builtinhash" ||
		got["TELEGRAM_CHANNEL_ID"] != "777" || got["TELEGRAM_BOT_TOKENS"] != "111:secret-one,222:secret-two" {
		t.Fatalf("env: %v", got)
	}
	select {
	case <-w.Saved():
	case <-time.After(time.Second):
		t.Fatal("saved was not signalled")
	}
	w.StopAuto()
	if !fake.stopped {
		t.Fatal("the account was not released")
	}
}

func TestAutoExistingWithoutBotsKeepsNothing(t *testing.T) {
	fake := &fakeAuto{signed: true, existing: AutoFound{ChannelID: 5, Title: "Empty", Skipped: []string{"theirs_bot"}}}
	w, srv, _ := autoRig(t, fake)
	code, body := call(t, w, srv, "POST", "/setup/auto/existing", map[string]string{"channel": "5"})
	if code != 200 || !strings.Contains(body, "theirs_bot") {
		t.Fatalf("%d %s", code, body)
	}
	if code, _ := call(t, w, srv, "POST", "/setup/save", Values{Auto: true}); code != http.StatusBadRequest {
		t.Fatal("saved with no bots")
	}
}

func TestAutoCreateRunsInTheBackground(t *testing.T) {
	fake := &fakeAuto{signed: true, created: AutoFound{ChannelID: 9, Title: "New", Tokens: []string{"1:x"}, Bots: []string{"n_bot"}}}
	w, srv, _ := autoRig(t, fake)
	if code, body := call(t, w, srv, "POST", "/setup/auto/create", map[string]any{"title": "New", "bots": 3}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, body := call(t, w, srv, "GET", "/setup/auto/job", nil)
		var j struct {
			Running  bool
			Progress []string
			Error    string
			Found    *foundView
		}
		if err := json.Unmarshal([]byte(body), &j); err != nil {
			t.Fatal(err)
		}
		if !j.Running {
			if j.Found == nil || j.Found.ChannelID != 9 || len(j.Progress) < 2 || j.Error != "" || strings.Contains(body, `"x"`) {
				t.Fatalf("job: %s", body)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the job never finished")
}

func TestAutoCreateKeepsPartialResultOnError(t *testing.T) {
	fake := &fakeAuto{signed: true, created: AutoFound{ChannelID: 9, Tokens: []string{"1:x"}, Bots: []string{"n_bot"}}, createErr: errors.New("BotFather asked to wait")}
	w, srv, _ := autoRig(t, fake)
	call(t, w, srv, "POST", "/setup/auto/create", map[string]any{"bots": 2})
	for i := 0; i < 100; i++ {
		_, body := call(t, w, srv, "GET", "/setup/auto/job", nil)
		if !strings.Contains(body, `"running":true`) {
			if !strings.Contains(body, "BotFather asked to wait") || !strings.Contains(body, `"n_bot"`) {
				t.Fatalf("job: %s", body)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the job never finished")
}
