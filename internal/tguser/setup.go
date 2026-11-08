package tguser

import (
	"context"
	"errors"
	"time"

	"github.com/gotd/td/tg"
)

// Run runs fn on a connected session, whether or not it can post in a channel. Setup uses it before
// a channel exists. A running service uses the narrower calls, which need a healthy admin.
func (m *Manager) Run(ctx context.Context, fn func(context.Context, *tg.Client) (any, error)) (any, error) {
	m.mu.Lock()
	var lv *live
	for _, s := range m.sessions {
		if s.connected.Load() {
			lv = s
			break
		}
	}
	m.mu.Unlock()
	if lv == nil {
		return nil, errors.New("no account is signed in")
	}
	return m.call(ctx, lv, fn)
}

// SignedIn reports whether any session is connected.
func (m *Manager) SignedIn() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.signedInLocked()
}

func (m *Manager) signedInLocked() bool {
	for _, s := range m.sessions {
		if s.connected.Load() {
			return true
		}
	}
	return false
}

// Stop ends every connection and any sign-in in progress, and waits up to timeout for them to
// close. Session files stay, so a new Manager started on the same folder picks the accounts up.
// The caller should also cancel the context given to Bind, so a sign-in finishing right now does
// not start a new connection.
func (m *Manager) Stop(timeout time.Duration) {
	m.CancelLogin()
	m.mu.Lock()
	for _, s := range m.sessions {
		s.run()
	}
	m.mu.Unlock()
	done := make(chan struct{})
	go func() {
		m.running.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		m.log.Warn("user sessions did not close in time")
	}
}

// ResolveChannelInput finds the channel a person typed: a username, a link or a numeric id. The
// account must already be a member of a private channel.
func ResolveChannelInput(ctx context.Context, api *tg.Client, input string) (*tg.Channel, error) {
	id, name, err := ParseChannelRef(input)
	if err != nil {
		return nil, err
	}
	return resolveChannel(ctx, api, SourceRef{ChannelID: id, Username: name})
}
