package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/gotd/td/tg"

	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/setup"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgsetup"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// autoSetup is the Telegram side of the automatic setup page. It signs the account in before any
// channel exists, with a user Manager that has no channel. The session it saves in the users folder
// is the one the running service picks up afterwards.
type autoSetup struct {
	ctx context.Context
	dir string
	log *slog.Logger

	mu      sync.Mutex
	users   *tguser.Manager
	cancel  context.CancelFunc
	apiID   int
	apiHash string
}

func newAutoSetup(ctx context.Context, dir string, log *slog.Logger) *autoSetup {
	return &autoSetup{ctx: ctx, dir: dir, log: log}
}

// manager returns the Manager for this api_id, replacing the old one when the person changed it.
func (a *autoSetup) manager(apiID int, apiHash string) (*tguser.Manager, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.users != nil && a.apiID == apiID && a.apiHash == apiHash {
		return a.users, nil
	}
	a.stopLocked()
	users, err := tguser.New(a.dir, apiID, apiHash, 0, tguser.Pace{}, a.log)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(a.ctx)
	users.Bind(ctx)
	if err := users.Connect(ctx); err != nil {
		cancel()
		return nil, err
	}
	a.users, a.cancel, a.apiID, a.apiHash = users, cancel, apiID, apiHash
	return users, nil
}

func (a *autoSetup) current() (*tguser.Manager, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.users == nil {
		return nil, errors.New("sign in first")
	}
	return a.users, nil
}

func loginResult(r tguser.LoginResult) setup.AutoLogin {
	return setup.AutoLogin{Step: string(r.Step), Warning: r.Warning, Note: r.Note}
}

func (a *autoSetup) BeginLogin(ctx context.Context, apiID int, apiHash, phone string) (setup.AutoLogin, error) {
	users, err := a.manager(apiID, apiHash)
	if err != nil {
		return setup.AutoLogin{}, err
	}
	r, err := users.BeginLogin(ctx, phone)
	return loginResult(r), err
}

func (a *autoSetup) SubmitCode(ctx context.Context, code string) (setup.AutoLogin, error) {
	users, err := a.current()
	if err != nil {
		return setup.AutoLogin{}, err
	}
	r, err := users.SubmitCode(ctx, code)
	return loginResult(r), err
}

func (a *autoSetup) SubmitPassword(ctx context.Context, password string) (setup.AutoLogin, error) {
	users, err := a.current()
	if err != nil {
		return setup.AutoLogin{}, err
	}
	r, err := users.SubmitPassword(ctx, password)
	return loginResult(r), err
}

func (a *autoSetup) Resend(ctx context.Context) (setup.AutoLogin, error) {
	users, err := a.current()
	if err != nil {
		return setup.AutoLogin{}, err
	}
	r, err := users.ResendLogin(ctx)
	return loginResult(r), err
}

func (a *autoSetup) CancelLogin() {
	if users, err := a.current(); err == nil {
		users.CancelLogin()
	}
}

func (a *autoSetup) SignedIn() bool {
	users, err := a.current()
	return err == nil && users.SignedIn()
}

// Resume connects a session left on disk by an earlier sign-in and waits until it is up.
// No saved session returns false at once, and the page asks for the phone number.
func (a *autoSetup) Resume(ctx context.Context) (bool, error) {
	if a.SignedIn() {
		a.noteLibrary(ctx)
		return true, nil
	}
	id, hash, ok := config.DefaultAPI()
	if !ok {
		return false, nil
	}
	users, err := a.manager(id, hash)
	if err != nil {
		return false, err
	}
	if !users.HasSaved() {
		if users.SignedIn() {
			a.noteLibrary(ctx)
		}
		return users.SignedIn(), nil
	}
	deadline := time.NewTimer(12 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		if users.SignedIn() {
			a.noteLibrary(ctx)
			return true, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-deadline.C:
			if users.SignedIn() {
				a.noteLibrary(ctx)
			}
			return users.SignedIn(), nil
		case <-tick.C:
		}
	}
}

// noteLibrary writes which library channel exists and which bots are admins. A failure here does
// not stop sign-in.
func (a *autoSetup) noteLibrary(ctx context.Context) {
	users, err := a.current()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	val, err := users.Run(ctx, func(ctx context.Context, api *tg.Client) (any, error) {
		return tgsetup.LibraryReport(ctx, api)
	})
	if err != nil {
		tgsetup.Record("library check: " + err.Error())
		return
	}
	lines, _ := val.([]string)
	if len(lines) == 0 {
		tgsetup.Record("library check: no Jellyfin channel yet")
		return
	}
	for _, line := range lines {
		tgsetup.Record(line)
	}
}

// Logout ends the account saved on disk. A session that is still connecting gets a few seconds,
// then the file is removed either way so the next sign-in can use a different number.
func (a *autoSetup) Logout(ctx context.Context) error {
	a.mu.Lock()
	users := a.users
	a.mu.Unlock()
	if users == nil {
		id, hash, ok := config.DefaultAPI()
		if !ok {
			return errors.New("sign in first")
		}
		var err error
		users, err = a.manager(id, hash)
		if err != nil {
			return err
		}
	}
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for users.HasSaved() && !users.SignedIn() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return users.LogoutAll(ctx)
		case <-tick.C:
		}
	}
	return users.LogoutAll(ctx)
}

func (a *autoSetup) runner() (*tgsetup.Runner, error) {
	users, err := a.current()
	if err != nil {
		return nil, err
	}
	return &tgsetup.Runner{S: users, Pause: 20 * time.Second}, nil
}

func foundOf(f tgsetup.Found) setup.AutoFound {
	out := setup.AutoFound{ChannelID: f.ChannelID, Title: f.Title, Tokens: f.Tokens, Bots: f.Bots, Skipped: f.Skipped}
	for _, b := range f.Owned {
		out.Owned = append(out.Owned, setup.OwnedBot{Username: b.Username, Name: b.Name})
	}
	return out
}

func (a *autoSetup) Existing(ctx context.Context, channel string) (setup.AutoFound, error) {
	r, err := a.runner()
	if err != nil {
		return setup.AutoFound{}, err
	}
	f, err := r.Existing(tguser.Prioritise(ctx), channel)
	return foundOf(f), err
}

func (a *autoSetup) Create(ctx context.Context, o setup.AutoCreate, progress func(string, string)) (setup.AutoFound, error) {
	r, err := a.runner()
	if err != nil {
		return setup.AutoFound{}, err
	}
	f, err := r.Create(tguser.Prioritise(ctx), tgsetup.CreateOptions{Title: o.Title, Channel: o.Channel, Bots: o.Bots}, progress)
	return foundOf(f), err
}

func (a *autoSetup) Reuse(ctx context.Context, o setup.AutoReuse, progress func(string, string)) (setup.AutoFound, error) {
	r, err := a.runner()
	if err != nil {
		return setup.AutoFound{}, err
	}
	f, err := r.Reuse(tguser.Prioritise(ctx), tgsetup.ReuseOptions{
		ChannelID: o.ChannelID, Title: o.Title, Tokens: o.Tokens, Bots: o.Bots, Usernames: o.Usernames,
	}, progress)
	return foundOf(f), err
}

// Stop closes the account's connection and waits for it, so the service that starts next can use
// the same session without two connections sharing one login.
func (a *autoSetup) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopLocked()
}

func (a *autoSetup) stopLocked() {
	if a.users == nil {
		return
	}
	a.cancel()
	a.users.Stop(10 * time.Second)
	a.users, a.cancel = nil, nil
}
