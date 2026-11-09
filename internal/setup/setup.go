// Package setup is the page a new user sees on first run. It asks only for what cannot be worked
// out: the Telegram details and a language. Everything else happens by itself.
package setup

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/lang"
	"github.com/Aryan-mor/jellyfin-telegram/internal/webui"
)

// Values is what the form submits.
type Values struct {
	APIID       string `json:"api_id"`
	APIHash     string `json:"api_hash"`
	Channel     string `json:"channel"`
	Bots        string `json:"bots"`
	Language    string `json:"language"`
	UILanguage  string `json:"ui_language"`
	JellyfinURL string `json:"jellyfin_url"`
	JellyfinKey string `json:"jellyfin_key"`
	// Fladder, Seerr and Downloads are the optional parts. Downloads is part of Seerr, so asking
	// for it turns Seerr on too.
	Fladder   bool `json:"fladder"`
	Seerr     bool `json:"seerr"`
	Downloads bool `json:"downloads"`
	// Group turns on the shared library: new files are posted again with a signed caption.
	Group bool `json:"group"`
	// TelegramSearch looks up a requested title on Telegram and copies a file it finds.
	TelegramSearch bool `json:"telegram_search"`
	// Mirror asks the first start to set up a public channel for films and series.
	// MirrorChannel, when set, is a channel the user already has. Otherwise a new one is created.
	// The title and username are optional; empty ones are chosen at random.
	Mirror         bool   `json:"mirror"`
	MirrorTitle    string `json:"mirror_title"`
	MirrorUsername string `json:"mirror_username"`
	MirrorChannel  string `json:"mirror_channel"`
	// Auto means the Telegram details come from the automatic setup held on the server: the
	// api_id, the channel and the bot tokens never pass through the browser.
	Auto bool `json:"auto"`
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

	auto Auto
	st   autoState
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
		_, _ = rw.Write(webui.Page("setup.html", ""))
	}))
	w.autoRoutes(mux)
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
		if strings.HasPrefix(r.URL.Path, "/ui/") {
			webui.Assets().ServeHTTP(rw, r)
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
	if tokens, err = config.ParseTokens(splitTokens(v.Bots)); err != nil {
		return 0, 0, nil, "", errors.New("bots must hold at least one token from @BotFather")
	}
	if language, err = parseExtras(v); err != nil {
		return 0, 0, nil, "", err
	}
	return apiID, channel, tokens, language, nil
}

// parseExtras checks the part both ways share: the language and an existing Jellyfin.
func parseExtras(v Values) (language string, err error) {
	if raw := strings.TrimSpace(v.Language); raw != "" {
		codes, err := lang.List(raw)
		if err != nil || len(codes) == 0 {
			return "", errors.New("pick a subtitle language from the list")
		}
		language = strings.Join(codes, ",")
	}
	if (strings.TrimSpace(v.JellyfinURL) == "") != (strings.TrimSpace(v.JellyfinKey) == "") {
		return "", errors.New("an existing Jellyfin needs both its address and an API key")
	}
	return language, nil
}

// details is what a request settles: the Telegram app, channel and bots, and the language.
type details struct {
	apiID    int
	apiHash  string
	channel  int64
	tokens   []string
	language string
}

// resolve returns the details of a request: typed in the form, or held by the automatic setup.
func (w *Wizard) resolve(v Values) (details, error) {
	if !v.Auto {
		apiID, channel, tokens, language, err := parse(v)
		return details{apiID, strings.TrimSpace(v.APIHash), channel, tokens, language}, err
	}
	w.mu.Lock()
	st := w.st
	w.mu.Unlock()
	if st.apiID == 0 || st.found == nil || len(st.found.Tokens) == 0 {
		return details{}, errors.New("the automatic setup has not finished yet")
	}
	language, err := parseExtras(v)
	if err != nil {
		return details{}, err
	}
	return details{st.apiID, st.apiHash, st.found.ChannelID, append([]string(nil), st.found.Tokens...), language}, nil
}

func (w *Wizard) handleVerify(rw http.ResponseWriter, r *http.Request) {
	var v Values
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		writeError(rw, http.StatusBadRequest, "bad request")
		return
	}
	d, err := w.resolve(v)
	if err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	report, err := w.verify(r.Context(), d.apiID, d.apiHash, d.channel, d.tokens)
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
	d, err := w.resolve(v)
	if err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	env := map[string]string{
		"TELEGRAM_API_ID":        fmt.Sprint(d.apiID),
		"TELEGRAM_API_HASH":      d.apiHash,
		"TELEGRAM_CHANNEL_ID":    fmt.Sprint(d.channel),
		"TELEGRAM_BOT_TOKENS":    strings.Join(d.tokens, ","),
		"SUBTITLE_LANGUAGE":      d.language,
		"JELLYFIN_URL":           strings.TrimRight(strings.TrimSpace(v.JellyfinURL), "/"),
		"JELLYFIN_API_KEY":       strings.TrimSpace(v.JellyfinKey),
		"SEERR_MANAGED":          fmt.Sprint(v.Seerr || v.Downloads),
		"FLADDER_MANAGED":        fmt.Sprint(v.Fladder),
		"DOWNLOADS_ENABLED":      fmt.Sprint(v.Downloads),
		"SIGNED_LIBRARY":         fmt.Sprint(v.Group),
		"TELEGRAM_SEARCH":        fmt.Sprint(v.TelegramSearch),
		"INBOX_DELETE_ORIGINALS": "false",
		"MIRROR_SETUP":           fmt.Sprint(v.Mirror),
		"MIRROR_TITLE":           strings.TrimSpace(v.MirrorTitle),
		"MIRROR_USERNAME":        strings.TrimPrefix(strings.TrimSpace(v.MirrorUsername), "@"),
		"MIRROR_CHANNEL":         strings.TrimSpace(v.MirrorChannel),
	}
	if code, ok := config.NormalizeUILanguage(v.UILanguage); ok {
		env["UI_LANGUAGE"] = code
	}
	if err = UpdateEnv(w.env, env); err != nil {
		writeError(rw, http.StatusInternalServerError, "cannot write the settings file: "+err.Error())
		return
	}
	w.once.Do(func() { close(w.saved) })
	writeJSON(rw, http.StatusOK, map[string]bool{"ok": true})
}

// TrustedClient reports that a request comes from this machine or a Tailscale address. Signing in a
// Telegram account is only allowed from there.
func TrustedClient(r *http.Request) bool {
	// A tunnel running on this machine connects from loopback but speaks for someone else, and says
	// so in these headers. A person at this computer sends none of them.
	for _, h := range []string{"X-Forwarded-For", "Forwarded", "X-Real-Ip"} {
		if r.Header.Get(h) != "" {
			return false
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1]&0xC0 == 64
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
