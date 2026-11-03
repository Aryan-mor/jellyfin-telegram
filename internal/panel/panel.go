// Package panel is the page for a server that is already running. It edits the Telegram
// settings, links to Jellyfin, Seerr and Fladder, and explains how to open Jellyfin from
// another network.
package panel

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"

	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/lang"
	"github.com/Aryan-mor/jellyfin-telegram/internal/setup"
)

//go:embed page.html
var page string

// LiveBot is one bot the running service already connected.
type LiveBot struct {
	ID    int64
	Name  string
	OK    bool
	Admin bool
	Error string
}

// Options wires the page to the running service.
type Options struct {
	// Code is the same secret as the setup page. The port is shared, so the page must not be open.
	Code string
	// EnvPath is the settings file the page writes.
	EnvPath string
	// Verify tries a set of bots against Telegram before anything is saved.
	Verify setup.Verifier
	// Bots reports the connections already open. It may be nil.
	Bots func() []LiveBot
	// URLs are the addresses printed at startup. It may be nil.
	URLs func() []string
	// SameChannel refuses a different channel once the library has files, because the .strm
	// files would then point at the wrong messages. Nil allows any channel.
	SameChannel func(channel int64) error
	// Seerr and Fladder are true when those apps are served on this gateway.
	Seerr   bool
	Fladder bool
}

// New serves /panel and /panel/.
func New(o Options) http.Handler {
	p := &panel{o: o}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /panel", p.guard(p.handlePage))
	mux.HandleFunc("GET /panel/state", p.guard(p.handleState))
	mux.HandleFunc("POST /panel/settings", p.guard(p.handleSettings))
	mux.HandleFunc("POST /panel/bots", p.guard(p.handleBots))
	return mux
}

type panel struct{ o Options }

func (p *panel) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if subtle.ConstantTimeCompare([]byte(code), []byte(p.o.Code)) == 1 {
			next(w, r)
			return
		}
		// This computer can open the page directly. The page then carries the code, so a site on
		// the internet cannot submit the form. Every other machine still needs the full address.
		if r.Method == http.MethodGet && r.URL.Path == "/panel" && fromThisMachine(r) {
			next(w, r)
			return
		}
		http.Error(w, "On this computer open /panel. From anywhere else, use the address shown on that page.", http.StatusForbidden)
	}
}

func (p *panel) handlePage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(strings.Replace(page, "__PANEL_CODE__", p.o.Code, 1)))
}

func fromThisMachine(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type address struct {
	URL  string `json:"url"`
	Away bool   `json:"away"`
}

type botView struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Hint      string `json:"hint"`
	OK        bool   `json:"ok"`
	Admin     bool   `json:"admin"`
	Connected bool   `json:"connected"`
	Error     string `json:"error,omitempty"`
}

type state struct {
	APIID     int       `json:"api_id"`
	ChannelID int64     `json:"channel_id"`
	Language  string    `json:"language"`
	Bots      []botView `json:"bots"`
	Addresses []address `json:"addresses"`
	Seerr     bool      `json:"seerr"`
	Fladder   bool      `json:"fladder"`
}

func (p *panel) handleState(w http.ResponseWriter, _ *http.Request) {
	s, err := p.snapshot()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// Pointer fields stay unchanged when the tab that owns them does not send them.
type settingsBody struct {
	APIID    *string `json:"api_id"`
	APIHash  *string `json:"api_hash"`
	Channel  *string `json:"channel"`
	Language *string `json:"language"`
}

func (p *panel) handleSettings(w http.ResponseWriter, r *http.Request) {
	var body settingsBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	cur, err := readFile(p.o.EnvPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	apiID := cur.apiID
	if body.APIID != nil && strings.TrimSpace(*body.APIID) != "" {
		if _, err := fmt.Sscan(strings.TrimSpace(*body.APIID), &apiID); err != nil || apiID <= 0 {
			writeError(w, http.StatusBadRequest, "api_id must be the number from my.telegram.org")
			return
		}
	}
	hash := cur.hash
	if body.APIHash != nil && strings.TrimSpace(*body.APIHash) != "" {
		hash = strings.TrimSpace(*body.APIHash)
	}
	channel := cur.channel
	if body.Channel != nil && strings.TrimSpace(*body.Channel) != "" {
		channel, err = config.ParseChannelID(*body.Channel)
		if err != nil {
			writeError(w, http.StatusBadRequest, "the channel must be a link to one of its messages, or its id")
			return
		}
	}
	if channel != cur.channel && p.o.SameChannel != nil {
		if err := p.o.SameChannel(channel); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	language := cur.language
	if body.Language != nil && strings.TrimSpace(*body.Language) != cur.language {
		language = ""
		if raw := strings.TrimSpace(*body.Language); raw != "" {
			language = lang.Normalize(raw)
			if language == "" {
				writeError(w, http.StatusBadRequest, "the language must be a code such as en, de, fa or ja")
				return
			}
		}
	}
	if err := p.commit(r, apiID, hash, channel, language, cur.tokens); err != nil {
		writePanelError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restart": true})
}

type botsBody struct {
	Add    string `json:"add"`
	Remove int64  `json:"remove"`
}

func (p *panel) handleBots(w http.ResponseWriter, r *http.Request) {
	var body botsBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	cur, err := readFile(p.o.EnvPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tokens := append([]string(nil), cur.tokens...)
	switch {
	case strings.TrimSpace(body.Add) != "" && body.Remove != 0:
		writeError(w, http.StatusBadRequest, "add one bot or remove one bot, not both")
		return
	case strings.TrimSpace(body.Add) != "":
		id, err := config.BotID(strings.TrimSpace(body.Add))
		if err != nil {
			writeError(w, http.StatusBadRequest, "paste a token from @BotFather")
			return
		}
		for _, t := range tokens {
			have, _ := config.BotID(t)
			if have == id {
				writeError(w, http.StatusBadRequest, "that bot is already listed")
				return
			}
		}
		tokens = append(tokens, strings.TrimSpace(body.Add))
	case body.Remove != 0:
		next := tokens[:0]
		for _, t := range tokens {
			id, _ := config.BotID(t)
			if id != body.Remove {
				next = append(next, t)
			}
		}
		if len(next) == len(tokens) {
			writeError(w, http.StatusBadRequest, "that bot is not listed")
			return
		}
		if len(next) == 0 {
			writeError(w, http.StatusBadRequest, "keep at least one bot")
			return
		}
		tokens = next
	default:
		writeError(w, http.StatusBadRequest, "nothing to change")
		return
	}
	if err := p.commit(r, cur.apiID, cur.hash, cur.channel, cur.language, tokens); err != nil {
		writePanelError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restart": true})
}

// commit checks the bots, then writes the file. A failed check leaves the file as it was.
func (p *panel) commit(r *http.Request, apiID int, hash string, channel int64, language string, tokens []string) error {
	if p.o.Verify != nil {
		report, err := p.o.Verify(r.Context(), apiID, hash, channel, tokens)
		if err != nil {
			return err
		}
		for _, b := range report.Bots {
			if !b.OK {
				return &rejected{report: report}
			}
		}
	}
	return setup.UpdateEnv(p.o.EnvPath, map[string]string{
		"TELEGRAM_API_ID":     fmt.Sprint(apiID),
		"TELEGRAM_API_HASH":   hash,
		"TELEGRAM_CHANNEL_ID": fmt.Sprint(channel),
		"TELEGRAM_BOT_TOKENS": strings.Join(tokens, ","),
		"SUBTITLE_LANGUAGE":   language,
	})
}

type rejected struct{ report setup.Report }

func (e *rejected) Error() string {
	for _, b := range e.report.Bots {
		if !b.OK {
			if b.Error != "" {
				return b.Name + ": " + b.Error
			}
			return b.Name + " cannot read the channel"
		}
	}
	return "a bot cannot read the channel"
}

func writePanelError(w http.ResponseWriter, err error) {
	var bad *rejected
	if errors.As(err, &bad) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "bots": bad.report.Bots})
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}

type fileSettings struct {
	apiID    int
	hash     string
	channel  int64
	language string
	tokens   []string
}

func readFile(path string) (fileSettings, error) {
	m, err := godotenv.Read(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fileSettings{}, err
	}
	var s fileSettings
	if _, err := fmt.Sscan(m["TELEGRAM_API_ID"], &s.apiID); err != nil || s.apiID <= 0 {
		return fileSettings{}, errors.New("TELEGRAM_API_ID is missing from the settings file")
	}
	s.hash = m["TELEGRAM_API_HASH"]
	if s.hash == "" {
		return fileSettings{}, errors.New("TELEGRAM_API_HASH is missing from the settings file")
	}
	s.channel, err = config.ParseChannelID(m["TELEGRAM_CHANNEL_ID"])
	if err != nil {
		return fileSettings{}, fmt.Errorf("TELEGRAM_CHANNEL_ID: %w", err)
	}
	s.tokens, err = config.ParseTokens(m["TELEGRAM_BOT_TOKENS"])
	if err != nil {
		return fileSettings{}, fmt.Errorf("TELEGRAM_BOT_TOKENS: %w", err)
	}
	s.language = m["SUBTITLE_LANGUAGE"]
	return s, nil
}

func (p *panel) snapshot() (state, error) {
	cur, err := readFile(p.o.EnvPath)
	if err != nil {
		return state{}, err
	}
	live := map[int64]LiveBot{}
	if p.o.Bots != nil {
		for _, b := range p.o.Bots() {
			live[b.ID] = b
		}
	}
	s := state{APIID: cur.apiID, ChannelID: cur.channel, Language: cur.language, Bots: []botView{}, Seerr: p.o.Seerr, Fladder: p.o.Fladder}
	for _, token := range cur.tokens {
		id, _ := config.BotID(token)
		view := botView{ID: id, Name: "bot " + strconv.FormatInt(id, 10), Hint: hint(token)}
		if b, ok := live[id]; ok {
			view.Connected = true
			view.OK = b.OK
			view.Admin = b.Admin
			view.Error = b.Error
			if b.Name != "" {
				view.Name = "@" + strings.TrimPrefix(b.Name, "@")
			}
		}
		s.Bots = append(s.Bots, view)
	}
	if p.o.URLs != nil {
		for _, raw := range p.o.URLs() {
			s.Addresses = append(s.Addresses, address{URL: raw, Away: awayAddress(raw)})
		}
	}
	return s, nil
}

func hint(token string) string {
	if len(token) <= 4 {
		return token
	}
	return token[len(token)-4:]
}

// awayAddress reports addresses in 100.64.0.0/10, the range Tailscale uses.
func awayAddress(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1]&0xC0 == 64
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]string{"error": message})
}
