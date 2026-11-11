package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgsetup"
)

// AutoLogin is one step of signing the account in.
type AutoLogin struct {
	// Step is "code", "password" or "done".
	Step    string `json:"step"`
	Warning string `json:"warning,omitempty"`
	// Note says where Telegram put the login code.
	Note string `json:"note,omitempty"`
}

// AutoFound is the channel and bots the automatic setup found or made. Tokens are secrets and never
// leave the server.
type AutoFound struct {
	ChannelID int64
	Title     string
	Tokens    []string
	Bots      []string
	Skipped   []string
	// Owned is set when the account already has 20 bots and the person has to pick from them.
	Owned []OwnedBot
}

// OwnedBot is a bot the signed-in account already has. The token is not included.
type OwnedBot struct {
	Username string `json:"username"`
	Name     string `json:"name,omitempty"`
}

// AutoReuse says which of the account's bots should have their token replaced and join the channel
// that was just prepared. Tokens already taken in this run are kept.
type AutoReuse struct {
	ChannelID int64
	Title     string
	Tokens    []string
	Bots      []string
	Usernames []string
}

// AutoCreate says what to make. A Channel given as text is used instead of a new one.
type AutoCreate struct {
	Title   string
	Channel string
	Bots    int
}

// Auto is the Telegram side of the automatic setup, done as the person's own account.
type Auto interface {
	// BeginLogin sends the code. An apiID of zero means the built-in one.
	BeginLogin(ctx context.Context, apiID int, apiHash, phone string) (AutoLogin, error)
	SubmitCode(ctx context.Context, code string) (AutoLogin, error)
	SubmitPassword(ctx context.Context, password string) (AutoLogin, error)
	// Resend asks Telegram to send the code by the next method.
	Resend(ctx context.Context) (AutoLogin, error)
	CancelLogin()
	SignedIn() bool
	// Resume connects a session saved earlier. True means the phone step can be skipped.
	Resume(ctx context.Context) (bool, error)
	// Logout ends the saved session so a different account can sign in.
	Logout(ctx context.Context) error
	// Existing reads a channel set up before and returns the bots this account owns.
	Existing(ctx context.Context, channel string) (AutoFound, error)
	// Create makes the channel (unless one is given) and the bots, and returns what exists even on an error.
	// progress state is "wait", "ok" or "bad". "ok" means that line has already succeeded.
	Create(ctx context.Context, o AutoCreate, progress func(text, state string)) (AutoFound, error)
	// Reuse replaces the token of bots the account already owns and makes them admins.
	Reuse(ctx context.Context, o AutoReuse, progress func(text, state string)) (AutoFound, error)
	// Stop releases the account's connection, keeping its saved session.
	Stop()
}

// SetAuto turns the automatic setup on. Without it the page offers only the manual form.
func (w *Wizard) SetAuto(a Auto) { w.auto = a }

// StopAuto releases the signed-in account once setup is saved, so the running service can use it.
func (w *Wizard) StopAuto() {
	if w.auto != nil {
		w.auto.Stop()
	}
}

type autoState struct {
	apiID   int
	apiHash string
	found   *AutoFound
	// draft is a channel already made while the bots are still being chosen.
	draft *AutoFound
	owned []OwnedBot
	job   autoJob
}

type jobStep struct {
	Text  string `json:"text"`
	State string `json:"state"`
}

type autoJob struct {
	running bool
	steps   []jobStep
	err     string
}

// splitTokens turns a pasted block of tokens, one per line or comma separated, into the form
// config.ParseTokens reads.
func splitTokens(s string) string {
	return strings.NewReplacer("\r", "", "\n", ",").Replace(s)
}

type autoBody struct {
	APIID     string   `json:"api_id"`
	APIHash   string   `json:"api_hash"`
	Phone     string   `json:"phone"`
	Code      string   `json:"code"`
	Password  string   `json:"password"`
	Resend    bool     `json:"resend"`
	Cancel    bool     `json:"cancel"`
	Channel   string   `json:"channel"`
	Title     string   `json:"title"`
	Bots      int      `json:"bots"`
	Usernames []string `json:"usernames"`
}

// foundView is what the browser may see about the result.
type foundView struct {
	ChannelID int64    `json:"channel_id"`
	Title     string   `json:"title"`
	Bots      []string `json:"bots"`
	Skipped   []string `json:"skipped,omitempty"`
}

func viewOf(f AutoFound) foundView {
	return foundView{ChannelID: f.ChannelID, Title: f.Title, Bots: append([]string{}, f.Bots...), Skipped: f.Skipped}
}

func (w *Wizard) autoRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /setup/auto/info", w.guard(w.autoInfo))
	mux.HandleFunc("POST /setup/auto/resume", w.guard(w.trusted(w.autoResume)))
	mux.HandleFunc("POST /setup/auto/logout", w.guard(w.trusted(w.autoLogout)))
	mux.HandleFunc("POST /setup/auto/login", w.guard(w.trusted(w.autoLogin)))
	mux.HandleFunc("POST /setup/auto/existing", w.guard(w.trusted(w.autoExisting)))
	mux.HandleFunc("POST /setup/auto/create", w.guard(w.trusted(w.autoCreate)))
	mux.HandleFunc("POST /setup/auto/reuse", w.guard(w.trusted(w.autoReuse)))
	mux.HandleFunc("GET /setup/auto/job", w.guard(w.autoJobState))
	mux.HandleFunc("GET /setup/auto/log", w.guard(w.autoLog))
}

// trusted lets a request through only from this machine or Tailscale. The account becomes the owner
// of the library channel, which no one else on the network should be able to start.
func (w *Wizard) trusted(next http.HandlerFunc) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		if w.auto == nil {
			writeError(rw, http.StatusNotFound, "the automatic setup is not available")
			return
		}
		if !TrustedClient(r) {
			writeError(rw, http.StatusForbidden, "signing in to Telegram is only allowed from this computer or over Tailscale")
			return
		}
		next(rw, r)
	}
}

func decodeAuto(rw http.ResponseWriter, r *http.Request) (autoBody, bool) {
	var b autoBody
	if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 1<<16)).Decode(&b); err != nil {
		writeError(rw, http.StatusBadRequest, "bad request")
		return b, false
	}
	return b, true
}

func (w *Wizard) autoInfo(rw http.ResponseWriter, _ *http.Request) {
	_, _, builtin := config.DefaultAPI()
	signed := false
	if w.auto != nil {
		signed = w.auto.SignedIn()
	}
	w.mu.Lock()
	var found *foundView
	if w.st.found != nil {
		v := viewOf(*w.st.found)
		found = &v
	}
	w.mu.Unlock()
	writeJSON(rw, http.StatusOK, map[string]any{
		"available": w.auto != nil, "builtin_api": builtin, "signed_in": signed, "found": found,
	})
}

func (w *Wizard) autoResume(rw http.ResponseWriter, r *http.Request) {
	signed, err := w.auto.Resume(r.Context())
	if err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	w.mu.Lock()
	var found *foundView
	if w.st.found != nil {
		v := viewOf(*w.st.found)
		found = &v
	}
	w.mu.Unlock()
	if signed {
		rememberAPI(w)
	}
	writeJSON(rw, http.StatusOK, map[string]any{"signed_in": signed, "found": found})
}

// rememberAPI keeps the built-in API once the account is connected. Without it, saving later
// reports that automatic setup has not finished, even when the channel and the bots exist.
func rememberAPI(w *Wizard) {
	id, hash, ok := config.DefaultAPI()
	if !ok {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.st.apiID == 0 {
		w.st.apiID, w.st.apiHash = id, hash
	}
}

func (w *Wizard) autoLogout(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	running := w.st.job.running
	w.mu.Unlock()
	if running {
		writeError(rw, http.StatusConflict, "wait until this step finishes")
		return
	}
	if err := w.auto.Logout(r.Context()); err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	w.mu.Lock()
	w.st = autoState{}
	w.mu.Unlock()
	tgsetup.ResetTranscript()
	writeJSON(rw, http.StatusOK, map[string]bool{"ok": true})
}

func (w *Wizard) autoLogin(rw http.ResponseWriter, r *http.Request) {
	b, ok := decodeAuto(rw, r)
	if !ok {
		return
	}
	var (
		res AutoLogin
		err error
	)
	switch {
	case b.Cancel:
		w.auto.CancelLogin()
		writeJSON(rw, http.StatusOK, map[string]bool{"ok": true})
		return
	case b.Resend:
		res, err = w.auto.Resend(r.Context())
	case strings.TrimSpace(b.Phone) != "":
		apiID, apiHash, perr := chooseAPI(b)
		if perr != nil {
			writeError(rw, http.StatusBadRequest, perr.Error())
			return
		}
		res, err = w.auto.BeginLogin(r.Context(), apiID, apiHash, b.Phone)
		if err == nil {
			w.mu.Lock()
			w.st.apiID, w.st.apiHash = apiID, apiHash
			w.mu.Unlock()
		}
	case b.Password != "":
		res, err = w.auto.SubmitPassword(r.Context(), b.Password)
	default:
		res, err = w.auto.SubmitCode(r.Context(), b.Code)
	}
	if err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(rw, http.StatusOK, res)
}

// chooseAPI uses the person's own api_id when both parts are a real pair. Anything else, including a
// phone number typed into the optional field, keeps the API built into this program. That value is
// never sent back to the browser.
func chooseAPI(b autoBody) (int, string, error) {
	idText, hashText := strings.TrimSpace(b.APIID), strings.TrimSpace(b.APIHash)
	if idText != "" || hashText != "" {
		id, err := strconv.ParseInt(idText, 10, 32)
		if err == nil && id > 0 && hashText != "" {
			return int(id), hashText, nil
		}
		if _, _, ok := config.DefaultAPI(); !ok {
			if err != nil || id <= 0 {
				return 0, "", errors.New("api_id must be the number from my.telegram.org")
			}
			return 0, "", errors.New("give both api_id and api_hash, or leave both empty to use the built-in ones")
		}
	}
	id, hash, ok := config.DefaultAPI()
	if !ok {
		return 0, "", errors.New("this build has no built-in api_id. Open Advanced and enter yours from my.telegram.org")
	}
	return id, hash, nil
}

func (w *Wizard) autoExisting(rw http.ResponseWriter, r *http.Request) {
	b, ok := decodeAuto(rw, r)
	if !ok {
		return
	}
	if !w.auto.SignedIn() {
		writeError(rw, http.StatusBadRequest, "sign in first")
		return
	}
	rememberAPI(w)
	found, err := w.auto.Existing(r.Context(), b.Channel)
	if err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	w.mu.Lock()
	if len(found.Tokens) > 0 {
		w.st.found = &found
	} else {
		w.st.found = nil
	}
	w.mu.Unlock()
	writeJSON(rw, http.StatusOK, viewOf(found))
}

func (w *Wizard) autoCreate(rw http.ResponseWriter, r *http.Request) {
	b, ok := decodeAuto(rw, r)
	if !ok {
		return
	}
	if !w.auto.SignedIn() {
		writeError(rw, http.StatusBadRequest, "sign in first")
		return
	}
	rememberAPI(w)
	if b.Bots == 0 {
		b.Bots = 6
	}
	if b.Bots < 2 || b.Bots > tgsetup.MaxBots {
		writeError(rw, http.StatusBadRequest, "choose between 2 and 10 bots")
		return
	}
	w.mu.Lock()
	if w.st.job.running {
		w.mu.Unlock()
		writeError(rw, http.StatusConflict, "this is already running")
		return
	}
	w.st.job = autoJob{running: true, steps: []jobStep{{Text: "Starting", State: "wait"}}}
	w.st.found = nil
	w.st.draft = nil
	w.st.owned = nil
	w.mu.Unlock()

	// The work outlives this request: making bots takes minutes and the page polls for progress.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		found, err := w.auto.Create(ctx, AutoCreate{Title: b.Title, Channel: b.Channel, Bots: b.Bots}, w.push)
		// Two bots are enough to play. A later BotFather refusal must not throw away the ones that exist.
		if err != nil && len(found.Tokens) >= 2 {
			found.Owned = nil
			w.push(fmt.Sprintf("%d bots are ready. That is enough to start.", len(found.Tokens)), "ok")
			err = nil
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		w.finish(found, err)
	}()
	writeJSON(rw, http.StatusOK, map[string]bool{"ok": true})
}

func (w *Wizard) autoReuse(rw http.ResponseWriter, r *http.Request) {
	b, ok := decodeAuto(rw, r)
	if !ok {
		return
	}
	if !w.auto.SignedIn() {
		writeError(rw, http.StatusBadRequest, "sign in first")
		return
	}
	rememberAPI(w)
	w.mu.Lock()
	if w.st.job.running {
		w.mu.Unlock()
		writeError(rw, http.StatusConflict, "this is already running")
		return
	}
	if w.st.draft == nil || w.st.draft.ChannelID == 0 || len(w.st.owned) == 0 {
		w.mu.Unlock()
		writeError(rw, http.StatusBadRequest, "there are no bots to choose from")
		return
	}
	allowed := map[string]bool{}
	for _, o := range w.st.owned {
		allowed[strings.ToLower(o.Username)] = true
	}
	var chosen []string
	seen := map[string]bool{}
	for _, raw := range b.Usernames {
		name := strings.TrimPrefix(strings.TrimSpace(raw), "@")
		key := strings.ToLower(name)
		if name == "" || seen[key] {
			continue
		}
		if !allowed[key] {
			w.mu.Unlock()
			writeError(rw, http.StatusBadRequest, "pick a bot from the list")
			return
		}
		seen[key] = true
		chosen = append(chosen, name)
	}
	draft := *w.st.draft
	if len(chosen) < 1 || len(chosen)+len(draft.Bots) > 10 {
		w.mu.Unlock()
		writeError(rw, http.StatusBadRequest, "pick between 1 and 10 bots")
		return
	}
	owned := w.st.owned
	w.st.owned = nil
	w.st.found = nil
	w.st.job = autoJob{running: true, steps: []jobStep{{Text: "Starting", State: "wait"}}}
	w.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		found, err := w.auto.Reuse(ctx, AutoReuse{
			ChannelID: draft.ChannelID, Title: draft.Title, Tokens: draft.Tokens, Bots: draft.Bots, Usernames: chosen,
		}, w.push)
		w.mu.Lock()
		defer w.mu.Unlock()
		w.closeJob(err)
		if (err == nil && len(found.Tokens) > 0) || len(found.Tokens) > len(draft.Tokens) {
			w.st.found = &found
			w.st.draft = nil
			w.st.owned = nil
			return
		}
		w.st.owned = owned
		w.st.draft = &draft
	}()
	writeJSON(rw, http.StatusOK, map[string]bool{"ok": true})
}

// finish stores a create result. A full account keeps the channel and the owned bots so the
// person can pick; tokens stay off the job until some exist.
// push records a step. A new line replaces the one still waiting, so an unfinished line cannot
// turn into a check just because a later line arrived.
func (w *Wizard) push(text, state string) {
	tgsetup.Record(state + "  " + text)
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.st.job.steps
	if len(s) > 0 && s[len(s)-1].State == "wait" {
		s[len(s)-1] = jobStep{Text: text, State: state}
		w.st.job.steps = s
		return
	}
	w.st.job.steps = append(s, jobStep{Text: text, State: state})
}

// closeJob ends the job. Any line still waiting is a failure: it did not succeed.
func (w *Wizard) closeJob(err error) {
	w.st.job.running = false
	if err == nil {
		return
	}
	w.st.job.err = err.Error()
	for i := range w.st.job.steps {
		if w.st.job.steps[i].State == "wait" {
			w.st.job.steps[i].State = "bad"
		}
	}
}

func (w *Wizard) finish(found AutoFound, err error) {
	w.closeJob(err)
	owned := found.Owned
	found.Owned = nil
	if len(owned) > 0 {
		w.st.owned = owned
		w.st.draft = &found
		w.st.found = nil
		return
	}
	w.st.owned = nil
	if len(found.Tokens) > 0 {
		w.st.found = &found
		w.st.draft = nil
	}
}

func (w *Wizard) autoJobState(rw http.ResponseWriter, _ *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := map[string]any{
		"running": w.st.job.running, "progress": append([]jobStep{}, w.st.job.steps...), "error": w.st.job.err,
	}
	if len(w.st.owned) > 0 {
		out["owned"] = w.st.owned
		if w.st.draft != nil && len(w.st.draft.Bots) > 0 {
			out["kept"] = append([]string{}, w.st.draft.Bots...)
		}
	} else if w.st.found != nil {
		out["found"] = viewOf(*w.st.found)
	}
	writeJSON(rw, http.StatusOK, out)
}

func (w *Wizard) autoLog(rw http.ResponseWriter, _ *http.Request) {
	writeJSON(rw, http.StatusOK, map[string]any{"lines": tgsetup.Transcript()})
}
