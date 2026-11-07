package tguser

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

// View is one session as the panel shows it.
type View struct {
	UserID    int64  `json:"user_id"`
	Phone     string `json:"phone,omitempty"`
	Username  string `json:"username,omitempty"`
	Premium   bool   `json:"premium"`
	Admin     bool   `json:"admin"`
	Connected bool   `json:"connected"`
	MaxBytes  int64  `json:"max_bytes"`
	Error     string `json:"error,omitempty"`
	Pace      Pace   `json:"pace"`
	SentBytes int64  `json:"sent_bytes"`
	RestUntil int64  `json:"rest_until,omitempty"`
	Progress  int64  `json:"progress,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Busy      bool   `json:"busy"`
}

// Manager owns every user session and one login at a time.
type Manager struct {
	dir       string
	apiID     int
	apiHash   string
	channelID int64
	defaults  Pace
	log       *slog.Logger

	mu       sync.Mutex
	root     context.Context
	sessions map[int64]*live
	pending  *pendingLogin
	// running counts the session loops, so Stop can wait until every connection is closed.
	running sync.WaitGroup
}

type live struct {
	store *fileStore
	meta  Meta
	ops   chan call
	run   context.CancelFunc

	connected atomic.Bool
	busy      atomic.Bool
	done      atomic.Int64
	total     atomic.Int64
	errText   atomic.Value // string
	// hashes caches channel access hashes for this account. Only the session loop touches it.
	hashes map[int64]int64
}

type call struct {
	fn   func(context.Context, *tg.Client) (any, error)
	resp chan result
}

type result struct {
	val any
	err error
}

// New prepares the directory. Connect starts the sessions already on disk.
func New(dir string, apiID int, apiHash string, channelID int64, defaults Pace, log *slog.Logger) (*Manager, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		dir: dir, apiID: apiID, apiHash: apiHash, channelID: channelID,
		defaults: defaults, log: log, sessions: map[int64]*live{},
	}, nil
}

// SetDefaults replaces the pace inherited by sessions that have no override.
func (m *Manager) SetDefaults(p Pace) {
	m.mu.Lock()
	m.defaults = p
	m.mu.Unlock()
}

// Connect starts every saved session. A bad session is reported on the panel and does not stop the others.
func (m *Manager) Connect(ctx context.Context) error {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		// A sign-in cannot continue after a restart. What it left may hold a login key, so it goes.
		if !e.IsDir() && strings.HasPrefix(name, "pending-") {
			_ = os.Remove(filepath.Join(m.dir, name))
			continue
		}
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".tmp") {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimSuffix(name, ".json"), 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		path := filepath.Join(m.dir, name)
		store := &fileStore{path: path}
		rec, err := store.read()
		if err != nil {
			m.log.Warn("cannot read user session", "path", path, "err", err)
			continue
		}
		if rec.Meta.UserID == 0 {
			rec.Meta.UserID = id
		}
		m.start(ctx, store, rec.Meta)
	}
	return nil
}

func (m *Manager) start(parent context.Context, store *fileStore, meta Meta) {
	ctx, cancel := context.WithCancel(parent)
	lv := &live{store: store, meta: meta, ops: make(chan call), run: cancel}
	lv.errText.Store("")
	m.mu.Lock()
	if old := m.sessions[meta.UserID]; old != nil {
		old.run()
	}
	m.sessions[meta.UserID] = lv
	m.mu.Unlock()
	m.running.Add(1)
	go func() {
		defer m.running.Done()
		m.serve(ctx, lv)
	}()
}

// errNotAdmin means the account cannot post in the channel right now. The session is kept and
// checked again, because the account can be made an admin at any time.
var errNotAdmin = errors.New("this account is not a channel admin with permission to post")

const (
	reconnectStart = 5 * time.Second
	reconnectMax   = 5 * time.Minute
	// notAdminEvery is how often an account without access is checked again.
	notAdminEvery = 2 * time.Minute
)

// reconnectWait says how long to wait before the next try, what the delay becomes, and whether to
// give up. Only a revoked session is final. A dropped connection is retried with a growing delay,
// and a missing admin right is checked again at a steady pace until someone grants it.
func reconnectWait(err error, delay time.Duration) (sleep, next time.Duration, stop bool) {
	switch {
	case fatalAuth(err):
		return 0, delay, true
	case errors.Is(err, errNotAdmin):
		return notAdminEvery, delay, false
	}
	return delay, min(delay*2, reconnectMax), false
}

// serve keeps one account connected. The session stays on the panel with its error while it waits.
func (m *Manager) serve(ctx context.Context, lv *live) {
	delay := reconnectStart
	for ctx.Err() == nil {
		began := time.Now()
		err := m.serveOnce(ctx, lv)
		if ctx.Err() != nil {
			return
		}
		if time.Since(began) > time.Minute {
			delay = reconnectStart
		}
		sleep, next, stop := reconnectWait(err, delay)
		if stop {
			return
		}
		delay = next
		select {
		case <-ctx.Done():
			return
		case <-time.After(sleep):
		}
	}
}

func (m *Manager) serveOnce(ctx context.Context, lv *live) error {
	client := telegram.NewClient(m.apiID, m.apiHash, telegram.Options{
		SessionStorage: lv.store,
		NoUpdates:      true,
		Middlewares: []telegram.Middleware{floodWait(func() {
			m.noteFlood(lv)
		})},
	})
	err := client.Run(ctx, func(ctx context.Context) error {
		api := client.API()
		if err := m.prepare(ctx, api, lv); err != nil {
			return err
		}
		lv.connected.Store(true)
		lv.errText.Store("")
		_ = lv.store.writeMeta(lv.meta)
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case op := <-lv.ops:
				val, err := op.fn(ctx, api)
				op.resp <- result{val, err}
			}
		}
	})
	lv.connected.Store(false)
	if ctx.Err() == nil && err != nil {
		msg := err.Error()
		lv.errText.Store(msg)
		lv.meta.Error = msg
		_ = lv.store.writeMeta(lv.meta)
		m.log.Warn("user session stopped", "user", lv.meta.UserID, "err", err)
	}
	return err
}

func (m *Manager) prepare(ctx context.Context, api *tg.Client, lv *live) error {
	status, err := clientStatus(ctx, api)
	if err != nil {
		return err
	}
	lv.meta.UserID = status.ID
	lv.meta.Username = status.Username
	lv.meta.Premium = status.Premium
	lv.meta.Phone = firstNonEmpty(lv.meta.Phone, status.Phone)
	if m.channelID == 0 {
		// Setup has no channel yet. The session only needs to be signed in.
		lv.meta.Error = ""
		return lv.store.writeMeta(lv.meta)
	}
	if lv.meta.ChannelID != m.channelID || lv.meta.AccessHash == 0 {
		hash, err := channelAccess(ctx, api, m.channelID)
		if err != nil {
			return err
		}
		lv.meta.AccessHash = hash
		lv.meta.ChannelID = m.channelID
	}
	ok, err := canPost(ctx, api, m.channelID, lv.meta.AccessHash)
	if err != nil {
		return err
	}
	lv.meta.Admin = ok
	if !ok {
		return errNotAdmin
	}
	lv.meta.Error = ""
	return lv.store.writeMeta(lv.meta)
}

type selfUser struct {
	ID       int64
	Username string
	Phone    string
	Premium  bool
}

func clientStatus(ctx context.Context, api *tg.Client) (selfUser, error) {
	raw, err := api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
	if err != nil {
		return selfUser{}, err
	}
	if len(raw) == 0 {
		return selfUser{}, errors.New("telegram did not return the signed-in user")
	}
	user, ok := raw[0].(*tg.User)
	if !ok {
		return selfUser{}, errors.New("telegram did not return the signed-in user")
	}
	return selfUser{ID: user.ID, Username: user.Username, Phone: user.Phone, Premium: user.Premium}, nil
}

func (m *Manager) noteFlood(lv *live) {
	m.mu.Lock()
	defer m.mu.Unlock()
	today := time.Now().Format("2006-01-02")
	if lv.meta.FloodDay != today {
		lv.meta.FloodMul = 1
		lv.meta.FloodDay = today
	}
	if lv.meta.FloodMul < 1 {
		lv.meta.FloodMul = 1
	}
	lv.meta.FloodMul *= 2
	_ = lv.store.writeMeta(lv.meta)
	m.log.Warn("telegram asked this account to slow down; its rest is doubled for the rest of the day", "user", lv.meta.UserID, "factor", lv.meta.FloodMul)
}

// Views lists sessions for the panel.
func (m *Manager) Views() []View {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]View, 0, len(m.sessions))
	for _, lv := range m.sessions {
		errText, _ := lv.errText.Load().(string)
		out = append(out, View{
			UserID: lv.meta.UserID, Phone: lv.meta.Phone, Username: lv.meta.Username,
			Premium: lv.meta.Premium, Admin: lv.meta.Admin, Connected: lv.connected.Load(),
			MaxBytes: MaxBytes(lv.meta.Premium), Error: errText, Pace: lv.meta.pace(m.defaults),
			SentBytes: lv.meta.sentToday(time.Now()), RestUntil: lv.meta.RestUntil,
			Progress: lv.done.Load(), Total: lv.total.Load(), Busy: lv.busy.Load(),
		})
	}
	return out
}

// Healthy is a connected admin session with no error.
func (m *Manager) Healthy() []View {
	var out []View
	for _, v := range m.Views() {
		if v.Connected && v.Admin && v.Error == "" {
			out = append(out, v)
		}
	}
	return out
}

// LargestCap is the biggest file any healthy session can send. Zero means there is no such session.
func (m *Manager) LargestCap() int64 {
	var max int64
	for _, v := range m.Healthy() {
		if v.MaxBytes > max {
			max = v.MaxBytes
		}
	}
	return max
}

func (m *Manager) get(id int64) (*live, Pace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lv := m.sessions[id]
	if lv == nil {
		return nil, Pace{}, fmt.Errorf("no session for user %d", id)
	}
	return lv, m.defaults, nil
}

// Gate reports whether this session can take a file now.
func (m *Manager) Gate(id int64, size int64, now time.Time) Gate {
	lv, def, err := m.get(id)
	if err != nil || !lv.connected.Load() || lv.busy.Load() {
		return Gate{Reason: "session is not ready"}
	}
	if text, _ := lv.errText.Load().(string); text != "" || !lv.meta.Admin {
		return Gate{Reason: "session is not an admin, or it failed"}
	}
	return Allow(lv.meta.pace(def), now, size, lv.meta.sentToday(now), lv.meta.SinceLong, time.Unix(lv.meta.RestUntil, 0), MaxBytes(lv.meta.Premium))
}

func (meta Meta) sentToday(now time.Time) int64 {
	if meta.SentDay != now.Format("2006-01-02") {
		return 0
	}
	return meta.SentBytes
}

// MarkSent stores the rest and the daily total after a successful upload.
func (m *Manager) MarkSent(id int64, size int64, now time.Time) {
	lv, def, err := m.get(id)
	if err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	day := now.Format("2006-01-02")
	if lv.meta.SentDay != day {
		lv.meta.SentDay = day
		lv.meta.SentBytes = 0
	}
	if lv.meta.FloodDay != day {
		lv.meta.FloodDay = day
		lv.meta.FloodMul = 1
	}
	lv.meta.SentBytes += size
	lv.meta.SinceLong += size
	mul := lv.meta.FloodMul
	if mul < 1 {
		mul = 1
	}
	rest, reset := RestAfter(lv.meta.pace(def), lv.meta.SinceLong, mul)
	if reset {
		lv.meta.SinceLong = 0
	}
	lv.meta.RestUntil = now.Add(rest).Unix()
	_ = lv.store.writeMeta(lv.meta)
}

// SetPace stores per-session overrides. Nil fields inside the pointers are not used; pass the values to keep.
func (m *Manager) SetPace(id int64, p Pace, hoursSet bool) error {
	lv, _, err := m.get(id)
	if err != nil {
		return err
	}
	if err := p.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	lv.meta.MBPerSec = &p.MBPerSec
	lv.meta.RestMinutes = &p.RestMinutes
	lv.meta.LongEveryGB = &p.LongEveryGB
	lv.meta.LongRestMinutes = &p.LongRestMinutes
	lv.meta.DailyGB = &p.DailyGB
	if hoursSet {
		h := p.Hours
		lv.meta.Hours = &h
	}
	return lv.store.writeMeta(lv.meta)
}

// Logout ends the Telegram session and deletes its file.
func (m *Manager) Logout(ctx context.Context, id int64) error {
	lv, _, err := m.get(id)
	if err != nil {
		return err
	}
	if lv.connected.Load() {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, _ = m.call(cctx, lv, func(ctx context.Context, api *tg.Client) (any, error) {
			_, err := api.AuthLogOut(ctx)
			return nil, err
		})
		cancel()
	}
	lv.run()
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	return os.Remove(lv.store.path)
}

func (m *Manager) call(ctx context.Context, lv *live, fn func(context.Context, *tg.Client) (any, error)) (any, error) {
	if !lv.connected.Load() {
		return nil, errors.New("session is not connected")
	}
	op := call{fn: fn, resp: make(chan result, 1)}
	// Only a running connection reads lv.ops. If it ended after the check above, nobody ever will,
	// so the wait is bounded instead of holding the caller for good.
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case lv.ops <- op:
	case <-timer.C:
		return nil, errors.New("session is not answering")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case res := <-op.resp:
		return res.val, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
