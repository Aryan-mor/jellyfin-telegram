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
	reused    []string
	reuseErr  error
	resumed   bool
	resumeErr error
	stopped   bool
	loggedOut bool
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
func (f *fakeAuto) Resend(context.Context) (AutoLogin, error) {
	return AutoLogin{Step: "code", Note: "sent again"}, nil
}
func (f *fakeAuto) CancelLogin()   {}
func (f *fakeAuto) SignedIn() bool { return f.signed }
func (f *fakeAuto) Resume(context.Context) (bool, error) {
	f.resumed = true
	return f.signed, f.resumeErr
}
func (f *fakeAuto) Stop() { f.stopped = true }
func (f *fakeAuto) Logout(context.Context) error {
	f.loggedOut = true
	f.signed = false
	return nil
}
func (f *fakeAuto) Existing(context.Context, string) (AutoFound, error) {
	return f.existing, nil
}
func (f *fakeAuto) Create(_ context.Context, o AutoCreate, progress func(string, string)) (AutoFound, error) {
	progress(fmt.Sprintf("Creating bot 1 of %d", o.Bots), "wait")
	if f.createErr == nil {
		progress("Bot @n_bot created", "ok")
	}
	return f.created, f.createErr
}
func (f *fakeAuto) Reuse(_ context.Context, o AutoReuse, progress func(string, string)) (AutoFound, error) {
	f.reused = append([]string{}, o.Usernames...)
	if len(o.Usernames) > 0 {
		progress("Replacing the token of @"+o.Usernames[0], "wait")
		if f.reuseErr == nil {
			progress("New token for @"+o.Usernames[0], "ok")
		}
	}
	if f.reuseErr != nil {
		return AutoFound{}, f.reuseErr
	}
	bots := append(append([]string{}, o.Bots...), o.Usernames...)
	return AutoFound{ChannelID: o.ChannelID, Title: o.Title, Tokens: []string{"9:revoked"}, Bots: bots}, nil
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

func TestAutoResumeSkipsThePhoneWhenAlreadySignedIn(t *testing.T) {
	fake := &fakeAuto{signed: true}
	w, srv, _ := autoRig(t, fake)
	code, body := call(t, w, srv, "POST", "/setup/auto/resume", map[string]any{})
	if code != 200 || !strings.Contains(body, `"signed_in":true`) || !fake.resumed {
		t.Fatalf("%d %s resumed %v", code, body, fake.resumed)
	}
}

func TestAutoLogoutForgetsTheAccount(t *testing.T) {
	fake := &fakeAuto{signed: true}
	w, srv, _ := autoRig(t, fake)
	code, body := call(t, w, srv, "POST", "/setup/auto/logout", map[string]any{})
	if code != 200 || !fake.loggedOut || fake.signed {
		t.Fatalf("%d %s loggedOut %v signed %v", code, body, fake.loggedOut, fake.signed)
	}
}

func TestRememberedAPILetsAutomaticSetupFinish(t *testing.T) {
	w, _, _ := autoRig(t, &fakeAuto{signed: true})
	_, err := w.resolve(Values{Auto: true})
	if err == nil || !strings.Contains(err.Error(), "has not finished") {
		t.Fatalf("got %v", err)
	}
	rememberAPI(w)
	w.mu.Lock()
	w.st.apiID, w.st.apiHash = 5, "hash"
	w.st.found = &AutoFound{ChannelID: 42, Tokens: []string{"123456:abcdefghijklmnopqrstuvwxyz012345"}}
	w.mu.Unlock()
	d, err := w.resolve(Values{Auto: true})
	if err != nil || d.apiID != 5 || d.channel != 42 || len(d.tokens) != 1 {
		t.Fatalf("%v %+v", err, d)
	}
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

func TestAutoLoginKeepsTheBuiltInAPIWhenTheOptionalOneIsNotANumber(t *testing.T) {
	config.DefaultAPIID, config.DefaultAPIHash = "4242", "builtinhash"
	t.Cleanup(func() { config.DefaultAPIID, config.DefaultAPIHash = "", "" })
	fake := &fakeAuto{}
	w, srv, _ := autoRig(t, fake)
	code, body := call(t, w, srv, "POST", "/setup/auto/login", map[string]string{"phone": "+4915112345678", "api_id": "+49 151 1234", "api_hash": ""})
	if code != 200 || !strings.Contains(body, `"code"`) {
		t.Fatalf("phone login: %d %s", code, body)
	}
	if fake.apiID != 4242 {
		t.Fatalf("the built-in api_id was not used: %d", fake.apiID)
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
			Progress []jobStep
			Error    string
			Found    *foundView
		}
		if err := json.Unmarshal([]byte(body), &j); err != nil {
			t.Fatal(err)
		}
		if !j.Running {
			ok := false
			for _, s := range j.Progress {
				if s.Text == "Bot @n_bot created" && s.State == "ok" {
					ok = true
				}
				if strings.Contains(s.Text, "Creating bot") && s.State == "ok" {
					t.Fatalf("an unfinished bot was checked: %s", body)
				}
			}
			if j.Found == nil || j.Found.ChannelID != 9 || !ok || j.Error != "" || strings.Contains(body, `"x"`) {
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

func TestAutoCreateOffersOwnedBotsWhenTheAccountIsFull(t *testing.T) {
	fake := &fakeAuto{signed: true, created: AutoFound{
		ChannelID: 9, Title: "New", Owned: []OwnedBot{{Username: "old_bot", Name: "Old"}},
	}, createErr: errors.New("Telegram allows 20 bots on one account, and this one is full")}
	w, srv, _ := autoRig(t, fake)
	call(t, w, srv, "POST", "/setup/auto/create", map[string]any{"title": "New", "bots": 3})
	var body string
	for i := 0; i < 100; i++ {
		_, body = call(t, w, srv, "GET", "/setup/auto/job", nil)
		if !strings.Contains(body, `"running":true`) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(body, `"old_bot"`) || !strings.Contains(body, "this one is full") || strings.Contains(body, `"found"`) {
		t.Fatalf("job: %s", body)
	}
	if code, text := call(t, w, srv, "POST", "/setup/auto/reuse", map[string]any{"usernames": []string{"nope_bot"}}); code != http.StatusBadRequest || !strings.Contains(text, "pick a bot") {
		t.Fatalf("rejected: %d %s", code, text)
	}
	if code, text := call(t, w, srv, "POST", "/setup/auto/reuse", map[string]any{"usernames": []string{"old_bot"}}); code != 200 {
		t.Fatalf("reuse: %d %s", code, text)
	}
	for i := 0; i < 100; i++ {
		_, body = call(t, w, srv, "GET", "/setup/auto/job", nil)
		if !strings.Contains(body, `"running":true`) {
			if !strings.Contains(body, `"old_bot"`) || strings.Contains(body, "revoked") || strings.Contains(body, `"owned"`) {
				t.Fatalf("job: %s", body)
			}
			if len(fake.reused) != 1 || fake.reused[0] != "old_bot" {
				t.Fatalf("reused %v", fake.reused)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("reuse never finished")
}

func TestAutoCreateDoesNotCheckABotThatWasNotMade(t *testing.T) {
	fake := &fakeAuto{signed: true, createErr: errors.New("no bot")}
	w, srv, _ := autoRig(t, fake)
	call(t, w, srv, "POST", "/setup/auto/create", map[string]any{"bots": 3})
	var body string
	for i := 0; i < 100; i++ {
		_, body = call(t, w, srv, "GET", "/setup/auto/job", nil)
		if !strings.Contains(body, `"running":true`) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(body, `"text":"Creating bot 1 of 3"`) || !strings.Contains(body, `"state":"bad"`) || strings.Contains(body, `"state":"ok"`) {
		t.Fatalf("job: %s", body)
	}
}
