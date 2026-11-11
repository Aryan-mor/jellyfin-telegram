// Package panel is the page for a server that is already running. It edits the Telegram
// settings, links to Jellyfin and Seerr, and explains how to open Jellyfin from
// another network.
package panel

import (
	"context"
	"crypto/subtle"
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
	"github.com/Aryan-mor/jellyfin-telegram/internal/version"
	"github.com/Aryan-mor/jellyfin-telegram/internal/webui"
)

// LiveBot is one bot the running service already connected.
type LiveBot struct {
	ID           int64
	Name         string
	OK           bool
	Admin        bool
	Error        string
	ChannelTitle string
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
	// Seerr is true when it is served on this gateway.
	Seerr bool
	// Accounts lists how to sign in to each app that runs here. It may be nil.
	Accounts func() []Account
	// Pace reports the default upload pace of the Telegram accounts. Nil leaves it out.
	Pace func(context.Context) (any, error)
	// AccountDo runs one account action: login, logout or pace.
	AccountDo func(r *http.Request, action string, body map[string]any) (any, error)
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
	// Starting reports that an empty account list is not final yet. The page waits.
	Starting func() bool
	// Sources reports the Telegram channels that videos are copied from. Nil hides that tab.
	Sources func(context.Context) (any, error)
	// SourceDo runs one action of that tab: add, remove, pause, settings, check or rescan.
	SourceDo func(r *http.Request, action string, body map[string]any) (any, error)
	// Mirrors lists the channels that receive part of the main channel. Nil hides that list.
	Mirrors func(context.Context) (any, error)
	// MirrorDo runs add, save or remove for a mirror channel.
	MirrorDo func(r *http.Request, action string, body map[string]any) (any, error)
	// Forum reports the shared-library switch, its key and the trust list. Nil hides that card.
	Forum func(context.Context) (any, error)
	// ForumDo runs group, originals, trust_add or trust_remove.
	ForumDo func(r *http.Request, action string, body map[string]any) (any, error)
	// MirrorCreate starts a background job that makes a mirror channel. Nil hides that action.
	MirrorCreate func(r *http.Request, body map[string]any) (any, error)
	// BotCreate starts a background job. action is "create" or "reuse".
	BotCreate func(r *http.Request, action string, body map[string]any) (any, error)
	// Job is the latest create job. Nil means none has been wired.
	Job func() JobView
	// Restart starts the program again so new settings apply. Nil means the button is not available.
	Restart func() error
	// ApplyUpdate downloads the newest release for this channel and replaces the program.
	// Nil means the install button is not available. The caller starts the new program.
	ApplyUpdate func(context.Context) error
	// AutoFilter serves the auto-add filter under /panel. It may be nil.
	AutoFilter setup.Mounter
	// Logs lists one page of what an account or a bot did. kind is "account" or "bot".
	Logs func(kind string, actorID int64, page, limit int) (any, int, error)
	// Paused reports that automatic bot and account work is stopped.
	Paused func() bool
	// SetPaused pauses or resumes that work. Nil hides the switch.
	SetPaused func(bool) error
}

// ErrBusy means a create job is already running.
var ErrBusy = errors.New("this is already running")

// Step records one line of a background job. state is "wait", "ok" or "bad".
type Step func(text, state string)

// JobLine is one line the page shows while a channel or a bot is being created.
type JobLine struct {
	Text  string `json:"text"`
	State string `json:"state"`
}

// JobView is the latest create job.
type JobView struct {
	Running  bool      `json:"running"`
	Progress []JobLine `json:"progress"`
	Error    string    `json:"error,omitempty"`
	Restart  bool      `json:"restart,omitempty"`
	Result   any       `json:"result,omitempty"`
	Owned    any       `json:"owned,omitempty"`
}

// New serves /panel and /panel/.
func New(o Options) http.Handler {
	p := &panel{o: o}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /panel", p.guard(p.handlePage))
	mux.HandleFunc("GET /panel/state", p.guard(p.handleState))
	mux.HandleFunc("POST /panel/settings", p.guard(p.handleSettings))
	mux.HandleFunc("POST /panel/ui-language", p.guard(p.handleUILanguage))
	mux.HandleFunc("POST /panel/bots", p.guard(p.handleBots))
	mux.HandleFunc("POST /panel/accounts/login", p.guard(p.handleAccountLogin))
	mux.HandleFunc("POST /panel/accounts/logout", p.guard(p.handleAccountLogout))
	mux.HandleFunc("POST /panel/accounts/pace", p.guard(p.handleAccountPace))
	mux.HandleFunc("GET /panel/local", p.guard(p.handleLocal))
	mux.HandleFunc("POST /panel/local/{action}", p.guard(p.handleLocalAction))
	mux.HandleFunc("GET /panel/home", p.guard(p.handleHome))
	mux.HandleFunc("POST /panel/home/channel", p.guard(p.handleHomeChannel))
	mux.HandleFunc("POST /panel/home/{action}", p.guard(p.handleHomeAction))
	mux.HandleFunc("GET /panel/sources", p.guard(p.handleSources))
	mux.HandleFunc("POST /panel/sources/{action}", p.guard(p.handleSourceAction))
	mux.HandleFunc("POST /panel/mirrors/create", p.guard(p.handleMirrorCreate))
	mux.HandleFunc("POST /panel/mirrors/{action}", p.guard(p.handleMirrorAction))
	mux.HandleFunc("POST /panel/bots/create", p.guard(p.handleBotJob("create")))
	mux.HandleFunc("POST /panel/bots/reuse", p.guard(p.handleBotJob("reuse")))
	mux.HandleFunc("GET /panel/jobs", p.guard(p.handleJobs))
	mux.HandleFunc("GET /panel/activity", p.guard(p.handleActivity))
	mux.HandleFunc("POST /panel/forum/{action}", p.guard(p.handleForumAction))
	mux.HandleFunc("POST /panel/search", p.guard(p.handleSearch))
	mux.HandleFunc("POST /panel/restart", p.guard(p.handleRestart))
	mux.HandleFunc("POST /panel/automation", p.guard(p.handleAutomation))
	mux.HandleFunc("GET /panel/update", p.guard(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, version.Check(r.Context()))
	}))
	mux.HandleFunc("POST /panel/update", p.guard(p.handleUpdate))
	if o.AutoFilter != nil {
		o.AutoFilter.Mount(mux, "/panel", p.guard)
	}
	// Screens such as /panel/bots. A more specific API route, like GET /panel/state, still wins.
	mux.HandleFunc("GET /panel/{path...}", p.guard(p.handlePage))
	return mux
}

// pagePaths are addresses the panel itself navigates to. Refresh must return the page.
var pagePaths = map[string]struct{}{
	"/panel":                   {},
	"/panel/accounts":          {},
	"/panel/channel":           {},
	"/panel/channel/mirrors":   {},
	"/panel/channel/shared":    {},
	"/panel/channel/home":      {},
	"/panel/channel/subtitles": {},
	"/panel/telegram":          {},
	"/panel/bots":              {},
	"/panel/logs":              {},
	"/panel/api":               {},
	"/panel/sources/filter":    {},
	"/panel/sources":           {},
	"/panel/news":              {},
	"/panel/local":             {},
	"/panel/away":              {},
}

// pageAPIs are screens that share a path with a JSON GET. A browser load gets the page;
// a fetch without text/html still gets the JSON.
var pageAPIs = map[string]struct{}{
	"/panel/local":   {},
	"/panel/sources": {},
}

type panel struct{ o Options }

func wantsDocument(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Dest") == "document" || r.Header.Get("Sec-Fetch-Mode") == "navigate" {
		return true
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

func (p *panel) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		codeOK := subtle.ConstantTimeCompare([]byte(code), []byte(p.o.Code)) == 1
		if _, page := pagePaths[r.URL.Path]; r.Method == http.MethodGet && page {
			_, api := pageAPIs[r.URL.Path]
			// /panel/local and /panel/sources are also JSON. Only a real page load takes the HTML.
			if !api || wantsDocument(r) {
				if codeOK || fromThisMachine(r) {
					p.handlePage(w, r)
					return
				}
				http.Error(w, "On this computer open /panel. From anywhere else, use the address shown on that page.", http.StatusForbidden)
				return
			}
		}
		if codeOK {
			next(w, r)
			return
		}
		http.Error(w, "On this computer open /panel. From anywhere else, use the address shown on that page.", http.StatusForbidden)
	}
}

func (p *panel) handleActivity(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind != "account" && kind != "bot" {
		writeError(w, http.StatusBadRequest, "pick an account or a bot")
		return
	}
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	const limit = 40
	if p.o.Logs == nil || id == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "total": 0, "page": 1, "pages": 1})
		return
	}
	items, total, err := p.o.Logs(kind, id, page, limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	pages := 1
	if total > 0 {
		pages = (total + limit - 1) / limit
	}
	if items == nil {
		items = []any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "pages": pages})
}

func (p *panel) handlePage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(webui.Page("panel.html", p.o.Code))
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
	APIID          int       `json:"api_id,omitempty"`
	DefaultAPI     bool      `json:"default_api,omitempty"`
	ChannelID      int64     `json:"channel_id"`
	ChannelTitle   string    `json:"channel_title,omitempty"`
	Language       string    `json:"language"`
	UILanguage     string    `json:"ui_language,omitempty"`
	Bots           []botView `json:"bots"`
	Addresses      []address `json:"addresses"`
	Seerr          bool      `json:"seerr"`
	Pace           any       `json:"pace,omitempty"`
	TelegramSearch bool      `json:"telegram_search"`
	Mirrors        any       `json:"mirrors,omitempty"`
	Local          bool      `json:"local"`
	Sources        bool      `json:"sources"`
	Telegram       any       `json:"telegram,omitempty"`
	Accounts       []Account `json:"accounts"`
	Starting       bool      `json:"starting,omitempty"`
	Forum          any       `json:"forum,omitempty"`
	Paused         bool      `json:"paused"`
}

func (p *panel) handleUILanguage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Language string `json:"language"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	code, ok := config.NormalizeUILanguage(body.Language)
	if !ok {
		writeError(w, http.StatusBadRequest, "pick a language from the list")
		return
	}
	if err := setup.UpdateEnv(p.o.EnvPath, map[string]string{"UI_LANGUAGE": code}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "language": code})
}

func (p *panel) handleSearch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		On *bool `json:"on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.On == nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if err := setup.UpdateEnv(p.o.EnvPath, map[string]string{"TELEGRAM_SEARCH": strconv.FormatBool(*body.On)}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (p *panel) handleState(w http.ResponseWriter, r *http.Request) {
	s, err := p.snapshot()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if p.o.Pace != nil {
		if d, err := p.o.Pace(r.Context()); err == nil {
			s.Pace = d
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
	if p.o.Forum != nil {
		view, err := p.o.Forum(r.Context())
		if err != nil {
			s.Forum = map[string]any{"error": err.Error()}
		} else {
			s.Forum = view
		}
	}
	writeJSON(w, http.StatusOK, s)
}

func (p *panel) handleForumAction(w http.ResponseWriter, r *http.Request) {
	if p.o.ForumDo == nil {
		writeError(w, http.StatusNotFound, "the shared library is not available")
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	out, err := p.o.ForumDo(r, r.PathValue("action"), body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (p *panel) handleAccountLogin(w http.ResponseWriter, r *http.Request) {
	if !setup.TrustedClient(r) {
		writeError(w, http.StatusForbidden, "Sign in to a Telegram account from this computer or over Tailscale. The panel is plain HTTP, and this step gives full access to the account.")
		return
	}
	p.accountAction(w, r, "login")
}

func (p *panel) handleAccountLogout(w http.ResponseWriter, r *http.Request) {
	if !setup.TrustedClient(r) {
		writeError(w, http.StatusForbidden, "Sign out of a Telegram account from this computer or over Tailscale.")
		return
	}
	p.accountAction(w, r, "logout")
}

func (p *panel) handleAccountPace(w http.ResponseWriter, r *http.Request) {
	p.accountAction(w, r, "pace")
}

func (p *panel) accountAction(w http.ResponseWriter, r *http.Request, action string) {
	if p.o.AccountDo == nil {
		writeError(w, http.StatusNotFound, "Telegram accounts are not available")
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	out, err := p.o.AccountDo(r, action, body)
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
	if action == "folder" && !setup.TrustedClient(r) {
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
	"news_account": true, "news_settings": true,
	"discover": true, "discover_add": true,
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

var mirrorActions = map[string]bool{
	"add": true, "save": true, "remove": true, "visibility": true, "admin": true,
}

func (p *panel) handleMirrorCreate(w http.ResponseWriter, r *http.Request) {
	if !setup.TrustedClient(r) {
		writeError(w, http.StatusForbidden, "Create a channel from this computer or over Tailscale. The panel is plain HTTP.")
		return
	}
	p.runJob(w, r, p.o.MirrorCreate)
}

func (p *panel) handleBotJob(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !setup.TrustedClient(r) {
			writeError(w, http.StatusForbidden, "Create a bot from this computer or over Tailscale. The panel is plain HTTP.")
			return
		}
		if p.o.BotCreate == nil {
			writeError(w, http.StatusNotFound, "creating a bot is not available")
			return
		}
		body := readBody(w, r)
		if body == nil {
			return
		}
		out, err := p.o.BotCreate(r, action, body)
		writeJob(w, out, err)
	}
}

func (p *panel) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if !setup.TrustedClient(r) {
		writeError(w, http.StatusForbidden, "Install an update from this computer or over Tailscale. The panel is plain HTTP.")
		return
	}
	if p.o.ApplyUpdate == nil {
		writeError(w, http.StatusNotFound, "installing an update is not available")
		return
	}
	if err := p.o.ApplyUpdate(r.Context()); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func (p *panel) handleRestart(w http.ResponseWriter, r *http.Request) {
	if !setup.TrustedClient(r) {
		writeError(w, http.StatusForbidden, "Restart from this computer or over Tailscale. The panel is plain HTTP.")
		return
	}
	if p.o.Restart == nil {
		writeError(w, http.StatusNotFound, "restart is not available")
		return
	}
	if err := p.o.Restart(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (p *panel) handleAutomation(w http.ResponseWriter, r *http.Request) {
	if p.o.SetPaused == nil {
		writeError(w, http.StatusNotFound, "pause is not available")
		return
	}
	var body struct {
		Paused bool `json:"paused"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if err := p.o.SetPaused(body.Paused); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"paused": body.Paused})
}

func (p *panel) handleJobs(w http.ResponseWriter, _ *http.Request) {
	if p.o.Job == nil {
		writeJSON(w, http.StatusOK, JobView{Progress: []JobLine{}})
		return
	}
	writeJSON(w, http.StatusOK, p.o.Job())
}

func (p *panel) runJob(w http.ResponseWriter, r *http.Request, fn func(*http.Request, map[string]any) (any, error)) {
	if fn == nil {
		writeError(w, http.StatusNotFound, "that action is not available")
		return
	}
	body := readBody(w, r)
	if body == nil {
		return
	}
	out, err := fn(r, body)
	writeJob(w, out, err)
}

func readBody(w http.ResponseWriter, r *http.Request) map[string]any {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return nil
	}
	if body == nil {
		body = map[string]any{}
	}
	return body
}

func writeJob(w http.ResponseWriter, out any, err error) {
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, ErrBusy) {
			code = http.StatusConflict
		}
		writeError(w, code, err.Error())
		return
	}
	if out == nil {
		out = map[string]any{"ok": true}
	}
	writeJSON(w, http.StatusOK, out)
}

func (p *panel) handleMirrorAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if action == "visibility" || action == "admin" {
		if !setup.TrustedClient(r) {
			writeError(w, http.StatusForbidden, "Change a channel from this computer or over Tailscale. The panel is plain HTTP.")
			return
		}
	}
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
	idSet := body.APIID != nil && strings.TrimSpace(*body.APIID) != ""
	if idSet {
		if _, err := fmt.Sscan(strings.TrimSpace(*body.APIID), &apiID); err != nil || apiID <= 0 {
			writeError(w, http.StatusBadRequest, "api_id must be the number from my.telegram.org")
			return
		}
	}
	hash := cur.hash
	hashSet := body.APIHash != nil && strings.TrimSpace(*body.APIHash) != ""
	if hashSet {
		hash = strings.TrimSpace(*body.APIHash)
	}
	// A half-filled form would keep one half of the built-in pair. Both parts have to change together.
	if defID, defHash, ok := config.DefaultAPI(); ok && cur.apiID == defID && cur.hash == defHash && idSet != hashSet {
		writeError(w, http.StatusBadRequest, "enter both an API ID and an API hash to replace the default")
		return
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
			codes, err := lang.List(raw)
			if err != nil || len(codes) == 0 {
				writeError(w, http.StatusBadRequest, "pick a subtitle language from the list")
				return
			}
			language = strings.Join(codes, ",")
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
	apiID          int
	hash           string
	channel        int64
	language       string
	uiLanguage     string
	tokens         []string
	telegramSearch bool
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
	if code, ok := config.NormalizeUILanguage(m["UI_LANGUAGE"]); ok {
		s.uiLanguage = code
	}
	s.telegramSearch = true
	if v, ok := m["TELEGRAM_SEARCH"]; ok && strings.EqualFold(strings.TrimSpace(v), "false") {
		s.telegramSearch = false
	}
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
	s := state{ChannelID: cur.channel, Language: cur.language, UILanguage: cur.uiLanguage, Bots: []botView{}, Seerr: p.o.Seerr, Accounts: []Account{}, TelegramSearch: cur.telegramSearch}
	// The API this build ships with is not a value the person chose. The panel says "default"
	// and never sends that id, even when only the hash was replaced.
	if defID, defHash, ok := config.DefaultAPI(); ok && cur.apiID == defID && cur.hash == defHash {
		s.DefaultAPI = true
	} else if defID, _, ok := config.DefaultAPI(); !ok || cur.apiID != defID {
		s.APIID = cur.apiID
	}
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
			if s.ChannelTitle == "" {
				s.ChannelTitle = b.ChannelTitle
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
	if p.o.Starting != nil && p.o.Starting() {
		s.Starting = true
	}
	if p.o.Paused != nil {
		s.Paused = p.o.Paused()
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
