// Package setup is the page a new user sees on first run. It asks only for what cannot be worked
// out: the Telegram details and a language. Everything else happens by itself.
package setup

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/lang"
)

//go:embed page.html
var page string

// Values is what the form submits.
type Values struct {
	APIID       string `json:"api_id"`
	APIHash     string `json:"api_hash"`
	Channel     string `json:"channel"`
	Bots        string `json:"bots"`
	Language    string `json:"language"`
	JellyfinURL string `json:"jellyfin_url"`
	JellyfinKey string `json:"jellyfin_key"`
}

// BotReport is the result of trying one bot.
type BotReport struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Report is the result of trying the submitted values.
type Report struct {
	Channel string      `json:"channel"`
	Videos  int         `json:"videos"`
	Bots    []BotReport `json:"bots"`
}

// Verifier tries the values against Telegram.
type Verifier func(ctx context.Context, apiID int, apiHash string, channel int64, tokens []string) (Report, error)

// Status is shown while the service prepares itself, and at the end.
type Status struct {
	Stage    string   `json:"stage"`
	Percent  int      `json:"percent"`
	Ready    bool     `json:"ready"`
	Error    string   `json:"error,omitempty"`
	URLs     []string `json:"urls,omitempty"`
	User     string   `json:"user,omitempty"`
	Password string   `json:"password,omitempty"`
	// Seerr is true when requests are served under /seerr/.
	Seerr bool `json:"seerr,omitempty"`
	// Fladder is true when the web client is served under /fladder/.
	Fladder bool `json:"fladder,omitempty"`
}

// Wizard serves the page. Every request must carry a code printed on this machine's console. The
// form takes Telegram secrets, and the port may be reachable by others on the network.
type Wizard struct {
	code   string
	env    string
	verify Verifier

	mu     sync.Mutex
	status Status
	saved  chan struct{}
	once   sync.Once
}

// New returns a Wizard that writes to the settings file at envPath.
func New(envPath string, verify Verifier) (*Wizard, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	return &Wizard{code: hex.EncodeToString(raw), env: envPath, verify: verify, saved: make(chan struct{})}, nil
}

// Code returns the secret that opens the page.
func (w *Wizard) Code() string { return w.code }

// Saved is closed once the user has saved working values.
func (w *Wizard) Saved() <-chan struct{} { return w.saved }

// SetStatus updates what the page shows.
func (w *Wizard) SetStatus(s Status) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status = s
}

// Handler serves the page and its API.
func (w *Wizard) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /setup", w.guard(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		rw.Header().Set("Cache-Control", "no-store")
		_, _ = rw.Write([]byte(page))
	}))
	mux.HandleFunc("POST /setup/verify", w.guard(w.handleVerify))
	mux.HandleFunc("POST /setup/save", w.guard(w.handleSave))
	mux.HandleFunc("GET /setup/status", w.guard(func(rw http.ResponseWriter, _ *http.Request) {
		w.mu.Lock()
		s := w.status
		w.mu.Unlock()
		writeJSON(rw, http.StatusOK, s)
	}))
	return mux
}

// Waiting serves the setup paths and tells anyone else to come back, while nothing else is up yet.
func (w *Wizard) Waiting() http.Handler {
	setup := w.Handler()
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/setup" || strings.HasPrefix(r.URL.Path, "/setup/") {
			setup.ServeHTTP(rw, r)
			return
		}
		http.Error(rw, "jellyfin-telegram is starting. The address to set it up is on its console.", http.StatusServiceUnavailable)
	})
}

func (w *Wizard) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if subtle.ConstantTimeCompare([]byte(code), []byte(w.code)) != 1 {
			http.Error(rw, "Open the full address printed on the console of this program.", http.StatusForbidden)
			return
		}
		next(rw, r)
	}
}

// parse checks the values and returns what they mean, or a message the user can act on.
func parse(v Values) (apiID int, channel int64, tokens []string, language string, err error) {
	if _, err := fmt.Sscan(strings.TrimSpace(v.APIID), &apiID); err != nil || apiID <= 0 {
		return 0, 0, nil, "", errors.New("api_id must be the number from my.telegram.org")
	}
	if strings.TrimSpace(v.APIHash) == "" {
		return 0, 0, nil, "", errors.New("api_hash is required")
	}
	if channel, err = config.ParseChannelID(v.Channel); err != nil {
		return 0, 0, nil, "", errors.New("the channel must be a link to one of its messages, or its id")
	}
	if tokens, err = config.ParseTokens(strings.NewReplacer("\r", "", "\n", ",").Replace(v.Bots)); err != nil {
		return 0, 0, nil, "", errors.New("bots must hold at least one token from @BotFather")
	}
	if raw := strings.TrimSpace(v.Language); raw != "" {
		if language = lang.Normalize(raw); language == "" {
			return 0, 0, nil, "", errors.New("the language must be a code such as en, de, fa or ja")
		}
	}
	if (strings.TrimSpace(v.JellyfinURL) == "") != (strings.TrimSpace(v.JellyfinKey) == "") {
		return 0, 0, nil, "", errors.New("an existing Jellyfin needs both its address and an API key")
	}
	return apiID, channel, tokens, language, nil
}

func (w *Wizard) handleVerify(rw http.ResponseWriter, r *http.Request) {
	var v Values
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		writeError(rw, http.StatusBadRequest, "bad request")
		return
	}
	apiID, channel, tokens, _, err := parse(v)
	if err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	report, err := w.verify(r.Context(), apiID, strings.TrimSpace(v.APIHash), channel, tokens)
	if err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(rw, http.StatusOK, report)
}

func (w *Wizard) handleSave(rw http.ResponseWriter, r *http.Request) {
	var v Values
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		writeError(rw, http.StatusBadRequest, "bad request")
		return
	}
	apiID, channel, tokens, language, err := parse(v)
	if err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	err = UpdateEnv(w.env, map[string]string{
		"TELEGRAM_API_ID":     fmt.Sprint(apiID),
		"TELEGRAM_API_HASH":   strings.TrimSpace(v.APIHash),
		"TELEGRAM_CHANNEL_ID": fmt.Sprint(channel),
		"TELEGRAM_BOT_TOKENS": strings.Join(tokens, ","),
		"SUBTITLE_LANGUAGE":   language,
		"JELLYFIN_URL":        strings.TrimRight(strings.TrimSpace(v.JellyfinURL), "/"),
		"JELLYFIN_API_KEY":    strings.TrimSpace(v.JellyfinKey),
	})
	if err != nil {
		writeError(rw, http.StatusInternalServerError, "cannot write the settings file: "+err.Error())
		return
	}
	w.once.Do(func() { close(w.saved) })
	writeJSON(rw, http.StatusOK, map[string]bool{"ok": true})
}

// UpdateEnv sets keys in a settings file. Other lines and their order stay as they are.
func UpdateEnv(path string, values map[string]string) error {
	for k, v := range values {
		if strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("%s holds a line break", k)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	newline := "\n"
	if strings.Contains(string(raw), "\r\n") {
		newline = "\r\n"
	}
	var lines []string
	if len(raw) > 0 {
		lines = strings.Split(strings.TrimRight(string(raw), "\r\n"), newline)
	}
	done := map[string]bool{}
	for i, line := range lines {
		key, _, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if v, known := values[strings.TrimSpace(key)]; known {
			lines[i] = strings.TrimSpace(key) + "=" + v
			done[strings.TrimSpace(key)] = true
		}
	}
	for _, key := range sortedKeys(values) {
		if !done[key] {
			lines = append(lines, key+"="+values[key])
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, newline)+newline), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

func writeJSON(rw http.ResponseWriter, code int, v any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-store")
	rw.WriteHeader(code)
	_ = json.NewEncoder(rw).Encode(v)
}

func writeError(rw http.ResponseWriter, code int, message string) {
	writeJSON(rw, code, map[string]string{"error": message})
}
