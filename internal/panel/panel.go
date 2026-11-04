// Package panel is the page for a server that is already running. It edits the Telegram
// settings, links to Jellyfin, Seerr and Fladder, and explains how to open Jellyfin from
// another network.
package panel

import (
	"context"
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
	// Accounts lists how to sign in to each app that runs here. It may be nil.
	Accounts func() []Account
	// Downloads reports the automatic download stack. Nil hides that tab's actions.
	Downloads func(context.Context) (any, error)
	// DownloadDo runs one panel action: login, logout, enable or pace.
	DownloadDo func(r *http.Request, action string, body map[string]any) (any, error)
	// Local reports the videos already on this computer. Nil hides the Local files tab.
	Local func(context.Context) (any, error)
	// LocalDo runs one action of that tab: scan, folder, upload, cancel, delete or delete_synced.
	LocalDo func(r *http.Request, action string, body map[string]any) (any, error)
	// Home reports the personal Home Videos and Photos library. Nil hides that tab.
	Home func(context.Context) (any, error)
	// HomeDo runs folder or sync. The channel itself is saved by the panel.
	HomeDo func(r *http.Request, action string, body map[string]any) (any, error)
	// Sessions lists the signed-in Telegram accounts. Nil hides the account tab.
	Sessions func() any
	// Sources reports the Telegram channels that videos are copied from. Nil hides that tab.
	Sources func(context.Context) (any, error)
	// SourceDo runs one action of that tab: add, remove, pause, settings, check or rescan.
	SourceDo func(r *http.Request, action string, body map[string]any) (any, error)
	// Mirrors lists the channels that receive part of the main channel. Nil hides that list.
	Mirrors func(context.Context) (any, error)
	// MirrorDo runs add, save or remove for a mirror channel.
	MirrorDo func(r *http.Request, action string, body map[string]any) (any, error)
}

// New serves /panel and /panel/.
func New(o Options) http.Handler {
	p := &panel{o: o}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /panel", p.guard(p.handlePage))
	mux.HandleFunc("GET /panel/state", p.guard(p.handleState))
	mux.HandleFunc("POST /panel/settings", p.guard(p.handleSettings))
	mux.HandleFunc("POST /panel/bots", p.guard(p.handleBots))
	mux.HandleFunc("POST /panel/automation", p.guard(p.handleAutomation))
	mux.HandleFunc("POST /panel/downloads/login", p.guard(p.handleDownloadLogin))
	mux.HandleFunc("POST /panel/downloads/logout", p.guard(p.handleDownloadLogout))
	mux.HandleFunc("POST /panel/downloads/pace", p.guard(p.handleDownloadPace))
	mux.HandleFunc("GET /panel/local", p.guard(p.handleLocal))
	mux.HandleFunc("POST /panel/local/{action}", p.guard(p.handleLocalAction))
	mux.HandleFunc("GET /panel/home", p.guard(p.handleHome))
	mux.HandleFunc("POST /panel/home/channel", p.guard(p.handleHomeChannel))
	mux.HandleFunc("POST /panel/home/{action}", p.guard(p.handleHomeAction))
	mux.HandleFunc("GET /panel/sources", p.guard(p.handleSources))
	mux.HandleFunc("POST /panel/sources/{action}", p.guard(p.handleSourceAction))
	mux.HandleFunc("POST /panel/mirrors/{action}", p.guard(p.handleMirrorAction))
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

// Account is how to sign in to one app. An empty User means the app uses an account the person already has.
type Account struct {
	App      string `json:"app"`
	Path     string `json:"path"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	Note     string `json:"note,omitempty"`
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
	Downloads any       `json:"downloads,omitempty"`
	Mirrors   any       `json:"mirrors,omitempty"`
	Local     bool      `json:"local"`
	Sources   bool      `json:"sources"`
	Telegram  any       `json:"telegram,omitempty"`
	Accounts  []Account `json:"accounts"`
}

func (p *panel) handleState(w http.ResponseWriter, r *http.Request) {
	s, err := p.snapshot()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if p.o.Downloads != nil {
		d, err := p.o.Downloads(r.Context())
		if err != nil {
			s.Downloads = map[string]any{"error": err.Error()}
		} else {
			s.Downloads = d
		}
	}
	if p.o.Mirrors != nil {
		list, err := p.o.Mirrors(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.Mirrors = list
	}
	writeJSON(w, http.StatusOK, s)
}

func (p *panel) handleAutomation(w http.ResponseWriter, r *http.Request) {
	if p.o.DownloadDo == nil {
		writeError(w, http.StatusNotFound, "downloads are not available")
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	out, err := p.o.DownloadDo(r, "enable", body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (p *panel) handleDownloadLogin(w http.ResponseWriter, r *http.Request) {
	if !trustedClient(r) {
		writeError(w, http.StatusForbidden, "Sign in to a Telegram account from this computer or over Tailscale. The panel is plain HTTP, and this step gives full access to the account.")
		return
	}
	p.downloadAction(w, r, "login")
}

func (p *panel) handleDownloadLogout(w http.ResponseWriter, r *http.Request) {
	if !trustedClient(r) {
		writeError(w, http.StatusForbidden, "Sign out of a Telegram account from this computer or over Tailscale.")
		return
	}
	p.downloadAction(w, r, "logout")
}

func (p *panel) handleDownloadPace(w http.ResponseWriter, r *http.Request) {
	p.downloadAction(w, r, "pace")
}

func (p *panel) downloadAction(w http.ResponseWriter, r *http.Request, action string) {
	if p.o.DownloadDo == nil {
		writeError(w, http.StatusNotFound, "downloads are not available")
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	out, err := p.o.DownloadDo(r, action, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if out == nil {
		out = map[string]any{"ok": true}
	}
	writeJSON(w, http.StatusOK, out)
}

func (p *panel) handleHome(w http.ResponseWriter, r *http.Request) {
	if p.o.Home == nil {
		writeError(w, http.StatusNotFound, "the personal library is not available")
		return
	}
	out, err := p.o.Home(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (p *panel) handleHomeChannel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Channel string `json:"channel"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	cur, err := readFile(p.o.EnvPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var id int64
	if strings.TrimSpace(body.Channel) != "" {
		id, err = config.ParseChannelID(body.Channel)
		if err != nil {
			writeError(w, http.StatusBadRequest, "the channel must be a link to one of its messages, or its id")
			return
		}
		if id == cur.channel {
			writeError(w, http.StatusBadRequest, "that is the main channel")
			return
		}
	}
	value := ""
	if id != 0 {
		value = fmt.Sprint(id)
	}
	if err := setup.UpdateEnv(p.o.EnvPath, map[string]string{"HOME_CHANNEL_ID": value}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restart": true})
}

func (p *panel) handleHomeAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if p.o.HomeDo == nil || (action != "folder" && action != "sync") {
		writeError(w, http.StatusNotFound, "unknown action")
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && action != "sync" {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if body == nil {
		body = map[string]any{}
	}
	out, err := p.o.HomeDo(r, action, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if out == nil {
		out = map[string]any{"ok": true}
	}
	writeJSON(w, http.StatusOK, out)
}

func (p *panel) handleLocal(w http.ResponseWriter, r *http.Request) {
	if p.o.Local == nil {
		writeError(w, http.StatusNotFound, "local files are not available")
		return
	}
	out, err := p.o.Local(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// localActions are the only things the Local files tab can ask for.
var localActions = map[string]bool{"scan": true, "folder": true, "upload": true, "cancel": true, "delete": true, "delete_synced": true}

func (p *panel) handleLocalAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if p.o.LocalDo == nil || !localActions[action] {
		writeError(w, http.StatusNotFound, "unknown action")
		return
	}
	// Choosing which folders of this computer the panel may read is limited to this computer or
	// Tailscale, like signing in to an account: the panel is plain HTTP.
	if action == "folder" && !trustedClient(r) {
		writeError(w, http.StatusForbidden, "Choose folders from this computer or over Tailscale. The panel is plain HTTP.")
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		body = map[string]any{}
	}
	out, err := p.o.LocalDo(r, action, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if out == nil {
		out = map[string]any{"ok": true}
	}
	writeJSON(w, http.StatusOK, out)
}

func (p *panel) handleSources(w http.ResponseWriter, r *http.Request) {
	if p.o.Sources == nil {
		writeError(w, http.StatusNotFound, "channel sources are not available")
		return
	}
	out, err := p.o.Sources(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// sourceActions are the only things the Channels tab can ask for.
var sourceActions = map[string]bool{
	"add": true, "remove": true, "pause": true, "strict": true, "settings": true, "check": true, "rescan": true,
	"news": true, "news_add": true, "news_remove": true, "news_pause": true,
	"news_phone": true, "news_code": true, "news_password": true, "news_update": true,
	"news_account": true, "news_settings": true,
}

func (p *panel) handleSourceAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if p.o.SourceDo == nil || !sourceActions[action] {
		writeError(w, http.StatusNotFound, "unknown action")
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		body = map[string]any{}
	}
	out, err := p.o.SourceDo(r, action, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if out == nil {
		out = map[string]any{"ok": true}
	}
	writeJSON(w, http.StatusOK, out)
}

var mirrorActions = map[string]bool{"add": true, "save": true, "remove": true}

func (p *panel) handleMirrorAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if p.o.MirrorDo == nil || !mirrorActions[action] {
		writeError(w, http.StatusNotFound, "unknown action")
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		body = map[string]any{}
	}
	out, err := p.o.MirrorDo(r, action, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if out == nil {
		out = map[string]any{"ok": true}
	}
	writeJSON(w, http.StatusOK, out)
}

func trustedClient(r *http.Request) bool {
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
	s := state{APIID: cur.apiID, ChannelID: cur.channel, Language: cur.language, Bots: []botView{}, Seerr: p.o.Seerr, Fladder: p.o.Fladder, Accounts: []Account{}}
	if p.o.Accounts != nil {
		s.Accounts = p.o.Accounts()
	}
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
	s.Local = p.o.Local != nil
	s.Sources = p.o.Sources != nil
	if p.o.Sessions != nil {
		s.Telegram = map[string]any{"sessions": p.o.Sessions()}
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
