package setup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
)

// AutoLogin is one step of signing the account in.
type AutoLogin struct {
	// Step is "code", "password" or "done".
	Step    string `json:"step"`
	Warning string `json:"warning,omitempty"`
}

// AutoFound is the channel and bots the automatic setup found or made. Tokens are secrets and never
// leave the server.
type AutoFound struct {
	ChannelID int64
	Title     string
	Tokens    []string
	Bots      []string
	Skipped   []string
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
	CancelLogin()
	SignedIn() bool
	// Existing reads a channel set up before and returns the bots this account owns.
	Existing(ctx context.Context, channel string) (AutoFound, error)
	// Create makes the channel (unless one is given) and the bots, and returns what exists even on an error.
	Create(ctx context.Context, o AutoCreate, progress func(string)) (AutoFound, error)
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
	job     autoJob
}

type autoJob struct {
	running  bool
	progress []string
	err      string
}

// splitTokens turns a pasted block of tokens, one per line or comma separated, into the form
// config.ParseTokens reads.
func splitTokens(s string) string {
	return strings.NewReplacer("\r", "", "\n", ",").Replace(s)
}

type autoBody struct {
	APIID    string `json:"api_id"`
	APIHash  string `json:"api_hash"`
	Phone    string `json:"phone"`
	Code     string `json:"code"`
	Password string `json:"password"`
	Cancel   bool   `json:"cancel"`
	Channel  string `json:"channel"`
	Title    string `json:"title"`
	Bots     int    `json:"bots"`
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
	mux.HandleFunc("POST /setup/auto/login", w.guard(w.trusted(w.autoLogin)))
	mux.HandleFunc("POST /setup/auto/existing", w.guard(w.trusted(w.autoExisting)))
	mux.HandleFunc("POST /setup/auto/create", w.guard(w.trusted(w.autoCreate)))
	mux.HandleFunc("GET /setup/auto/job", w.guard(w.autoJobState))
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

// chooseAPI uses the person's own api_id when both parts are given, otherwise the built-in one.
func chooseAPI(b autoBody) (int, string, error) {
	idText, hash := strings.TrimSpace(b.APIID), strings.TrimSpace(b.APIHash)
	if idText != "" || hash != "" {
		id, err := strconv.ParseInt(idText, 10, 32)
		if err != nil || id <= 0 {
			return 0, "", errors.New("api_id must be the number from my.telegram.org")
		}
		if hash == "" {
			return 0, "", errors.New("give both api_id and api_hash, or leave both empty to use the built-in ones")
		}
		return int(id), hash, nil
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
	if b.Bots == 0 {
		b.Bots = 3
	}
	w.mu.Lock()
	if w.st.job.running {
		w.mu.Unlock()
		writeError(rw, http.StatusConflict, "this is already running")
		return
	}
	w.st.job = autoJob{running: true, progress: []string{"Starting"}}
	w.st.found = nil
	w.mu.Unlock()

	// The work outlives this request: making bots takes minutes and the page polls for progress.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		found, err := w.auto.Create(ctx, AutoCreate{Title: b.Title, Channel: b.Channel, Bots: b.Bots}, func(msg string) {
			w.mu.Lock()
			w.st.job.progress = append(w.st.job.progress, msg)
			w.mu.Unlock()
		})
		w.mu.Lock()
		defer w.mu.Unlock()
		w.st.job.running = false
		if len(found.Tokens) > 0 {
			w.st.found = &found
		}
		if err != nil {
			w.st.job.err = err.Error()
		}
	}()
	writeJSON(rw, http.StatusOK, map[string]bool{"ok": true})
}

func (w *Wizard) autoJobState(rw http.ResponseWriter, _ *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := map[string]any{
		"running": w.st.job.running, "progress": append([]string{}, w.st.job.progress...), "error": w.st.job.err,
	}
	if w.st.found != nil {
		out["found"] = viewOf(*w.st.found)
	}
	writeJSON(rw, http.StatusOK, out)
}
