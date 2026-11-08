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
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

// LoginStep is the next thing the panel must ask for.
type LoginStep string

const (
	StepCode     LoginStep = "code"
	StepPassword LoginStep = "password"
	StepDone     LoginStep = "done"
)

// LoginResult is one step of signing a user account in.
type LoginResult struct {
	Step LoginStep `json:"step"`
	// Warning is set when the account signed in but cannot post in the channel yet.
	Warning string `json:"warning,omitempty"`
	// Note says where Telegram put the login code.
	Note string `json:"note,omitempty"`
}

type pendingLogin struct {
	phone    string
	code     chan string
	password chan string
	step     chan LoginStep
	resend   chan struct{}
	done     chan error
	cancel   context.CancelFunc
	log      *slog.Logger
	again    func(context.Context) (*tg.AuthSentCode, error)
	// warning is written before done is sent, so the reader of done sees it.
	warning string
	// note and hash are written before step is sent.
	note string
	hash string
}

type codeAuth struct {
	phone string
	p     *pendingLogin
}

func (a codeAuth) Phone(context.Context) (string, error) { return a.phone, nil }

func (a codeAuth) Code(ctx context.Context, sent *tg.AuthSentCode) (string, error) {
	if err := a.announce(ctx, sent); err != nil {
		return "", err
	}
	for {
		select {
		case code := <-a.p.code:
			return strings.TrimSpace(code), nil
		case <-a.p.resend:
			if a.p.again == nil {
				a.p.note = "Telegram cannot send the code another way."
				if err := a.signal(ctx); err != nil {
					return "", err
				}
				continue
			}
			next, err := a.p.again(ctx)
			if err != nil {
				a.p.note = err.Error()
				if err := a.signal(ctx); err != nil {
					return "", err
				}
				continue
			}
			if err := a.announce(ctx, next); err != nil {
				return "", err
			}
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

func (a codeAuth) announce(ctx context.Context, sent *tg.AuthSentCode) error {
	if sent == nil {
		return errors.New("Telegram did not send a code")
	}
	if _, ok := sent.Type.(*tg.AuthSentCodeTypeSetUpEmailRequired); ok {
		return errors.New("Telegram will not send a code until this account has a login email. Add one in the Telegram app, then try again.")
	}
	a.p.hash = sent.PhoneCodeHash
	a.p.note = deliveryNote(sent)
	if a.p.log != nil {
		a.p.log.Info("telegram login code", "type", fmt.Sprintf("%T", sent.Type), "next", fmt.Sprintf("%T", sent.NextType))
	}
	return a.signal(ctx)
}

func (a codeAuth) signal(ctx context.Context) error {
	select {
	case a.p.step <- StepCode:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// deliveryNote says where the code went, in words a person can follow.
func deliveryNote(sent *tg.AuthSentCode) string {
	if sent == nil || sent.Type == nil {
		return "Telegram accepted the number. Check the Telegram app and your text messages."
	}
	switch sent.Type.(type) {
	case *tg.AuthSentCodeTypeApp:
		return "No text message was sent. Telegram put the code in the app, on a device already signed in with this number. Open Telegram and look at the chat named Telegram."
	case *tg.AuthSentCodeTypeSMS, *tg.AuthSentCodeTypeFragmentSMS, *tg.AuthSentCodeTypeFirebaseSMS, *tg.AuthSentCodeTypeSMSWord, *tg.AuthSentCodeTypeSMSPhrase:
		return "Telegram sent the code by text message."
	case *tg.AuthSentCodeTypeCall:
		return "Telegram is calling this number. The code is spoken in the call."
	case *tg.AuthSentCodeTypeFlashCall, *tg.AuthSentCodeTypeMissedCall:
		return "Telegram is placing a call. The code is the last digits of the caller number."
	case *tg.AuthSentCodeTypeEmailCode:
		return "Telegram sent the code to the email address on this account."
	default:
		return "Telegram sent a code. Check the Telegram app and your text messages."
	}
}

func (a codeAuth) Password(ctx context.Context) (string, error) {
	select {
	case a.p.step <- StepPassword:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	select {
	case pass := <-a.p.password:
		return pass, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (a codeAuth) AcceptTermsOfService(context.Context, tg.HelpTermsOfService) error {
	return nil
}

func (a codeAuth) SignUp(context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errors.New("this phone number is not registered on Telegram")
}

// Bind keeps new sessions alive until ctx ends. The HTTP request that signs in must not be that context.
func (m *Manager) Bind(ctx context.Context) {
	m.mu.Lock()
	m.root = ctx
	m.mu.Unlock()
}

func (m *Manager) rootCtx() context.Context {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.root != nil {
		return m.root
	}
	return context.Background()
}

// BeginLogin sends a code to phone. The panel then calls SubmitCode.
func (m *Manager) BeginLogin(ctx context.Context, phone string) (LoginResult, error) {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return LoginResult{}, errors.New("a phone number is required")
	}
	m.mu.Lock()
	if m.signedInLocked() {
		m.mu.Unlock()
		return LoginResult{Step: StepDone}, nil
	}
	if m.pending != nil {
		m.mu.Unlock()
		return LoginResult{}, errors.New("a sign-in is already in progress")
	}
	parent := m.root
	if parent == nil {
		parent = context.Background()
	}
	pctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	p := &pendingLogin{
		phone: phone, code: make(chan string, 1), password: make(chan string, 1),
		step: make(chan LoginStep, 1), resend: make(chan struct{}, 1),
		done: make(chan error, 1), cancel: cancel, log: m.log,
	}
	m.pending = p
	m.mu.Unlock()

	path := filepath.Join(m.dir, "pending-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".json")
	go m.runLogin(pctx, &fileStore{path: path}, p)
	return m.waitStep(ctx, p)
}

// CancelLogin ends a sign-in the user walked away from, so the next one can start at once.
func (m *Manager) CancelLogin() {
	p := m.current()
	if p == nil {
		return
	}
	if p.cancel != nil {
		p.cancel()
	}
	m.clearPending(p)
}

// ResendLogin asks Telegram to send the code by the next method, usually a text message.
func (m *Manager) ResendLogin(ctx context.Context) (LoginResult, error) {
	p := m.current()
	if p == nil {
		return LoginResult{}, errors.New("start with the phone number")
	}
	select {
	case p.resend <- struct{}{}:
	case <-ctx.Done():
		return LoginResult{}, ctx.Err()
	}
	return m.waitStep(ctx, p)
}

// SubmitCode continues a sign-in that is waiting for the Telegram code.
func (m *Manager) SubmitCode(ctx context.Context, code string) (LoginResult, error) {
	return m.submit(ctx, code, false)
}

// SubmitPassword continues a sign-in that is waiting for the two-step password.
func (m *Manager) SubmitPassword(ctx context.Context, password string) (LoginResult, error) {
	return m.submit(ctx, password, true)
}

func (m *Manager) submit(ctx context.Context, value string, password bool) (LoginResult, error) {
	p := m.current()
	if p == nil {
		return LoginResult{}, errors.New("start with the phone number")
	}
	ch := p.code
	if password {
		ch = p.password
	}
	select {
	case ch <- value:
	case <-ctx.Done():
		return LoginResult{}, ctx.Err()
	}
	return m.waitStep(ctx, p)
}

func (m *Manager) current() *pendingLogin {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pending
}

func (m *Manager) waitStep(ctx context.Context, p *pendingLogin) (LoginResult, error) {
	select {
	case step := <-p.step:
		return LoginResult{Step: step, Note: p.note}, nil
	case err := <-p.done:
		m.mu.Lock()
		if m.pending == p {
			m.pending = nil
		}
		m.mu.Unlock()
		if err != nil {
			return LoginResult{}, err
		}
		return LoginResult{Step: StepDone, Warning: p.warning}, nil
	case <-ctx.Done():
		return LoginResult{}, ctx.Err()
	}
}

// clearPending lets a new sign-in start. An abandoned one ends by its timeout and must not block the next.
func (m *Manager) clearPending(p *pendingLogin) {
	m.mu.Lock()
	if m.pending == p {
		m.pending = nil
	}
	m.mu.Unlock()
}

func (m *Manager) runLogin(ctx context.Context, store *fileStore, p *pendingLogin) {
	defer m.clearPending(p)
	defer p.cancel()
	pendingPath := store.path
	defer os.Remove(pendingPath)
	client := telegram.NewClient(m.apiID, m.apiHash, telegram.Options{SessionStorage: store, NoUpdates: true})
	err := client.Run(ctx, func(ctx context.Context) error {
		p.again = func(ctx context.Context) (*tg.AuthSentCode, error) {
			if p.hash == "" {
				return nil, errors.New("no code was sent yet")
			}
			sent, err := client.Auth().ResendCode(ctx, p.phone, p.hash)
			if err != nil {
				return nil, err
			}
			code, ok := sent.(*tg.AuthSentCode)
			if !ok {
				return nil, errors.New("Telegram did not send another code")
			}
			return code, nil
		}
		flow := auth.NewFlow(codeAuth{phone: p.phone, p: p}, auth.SendCodeOptions{})
		if err := client.Auth().IfNecessary(ctx, flow); err != nil {
			return err
		}
		api := client.API()
		self, err := clientStatus(ctx, api)
		if err != nil {
			return err
		}
		finalPath := filepath.Join(m.dir, strconv.FormatInt(self.ID, 10)+".json")
		if m.hasUser(self.ID) || exists(finalPath) {
			// The new login is a duplicate. Drop it and keep the session that is already running.
			_, _ = api.AuthLogOut(ctx)
			return errAlreadySignedIn
		}
		// An account without access to the channel stays signed in. The session is kept with the
		// reason, so being made an admin later fixes it, and the panel says what is missing.
		meta := Meta{
			UserID: self.ID, Phone: firstNonEmpty(p.phone, self.Phone), Username: self.Username,
			Premium: self.Premium, ChannelID: m.channelID,
		}
		// Setup signs in before any channel exists (channelID 0). The channel is then checked when
		// the service starts and reads this session again.
		if m.channelID != 0 {
			if reason := m.channelCheck(ctx, api, &meta); reason != "" {
				p.warning = reason
				meta.Error = reason
			}
		}
		if err := store.writeMeta(meta); err != nil {
			return err
		}
		if err := os.Rename(pendingPath, finalPath); err != nil {
			return err
		}
		store.path = finalPath
		return nil
	})
	if alreadyIn(err) {
		p.done <- nil
		return
	}
	if err != nil {
		p.done <- err
		return
	}
	rec, err := store.read()
	if err != nil {
		p.done <- err
		return
	}
	m.start(m.rootCtx(), store, rec.Meta)
	p.done <- nil
}

// channelCheck fills in the account's access to the channel. It returns why the account cannot
// post there, or "" when it can.
func (m *Manager) channelCheck(ctx context.Context, api *tg.Client, meta *Meta) string {
	hash, err := channelAccess(ctx, api, m.channelID)
	if err != nil {
		return err.Error()
	}
	meta.AccessHash = hash
	ok, err := canPost(ctx, api, m.channelID, hash)
	switch {
	case err != nil:
		return err.Error()
	case !ok:
		return errNotAdmin.Error()
	}
	meta.Admin = true
	return ""
}

// errAlreadySignedIn means this phone is already one of the saved sessions. The duplicate login is
// dropped and the existing session stays.
var errAlreadySignedIn = errors.New("this account is already signed in")

func alreadyIn(err error) bool {
	return err != nil && (errors.Is(err, errAlreadySignedIn) || strings.Contains(err.Error(), errAlreadySignedIn.Error()))
}

func (m *Manager) hasUser(id int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.sessions[id]
	return ok
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
