package tguser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBeginLoginSkipsWhenTheAccountIsConnected(t *testing.T) {
	m := &Manager{sessions: map[int64]*live{}}
	lv := &live{}
	lv.connected.Store(true)
	m.sessions[1] = lv
	got, err := m.BeginLogin(context.Background(), "+1")
	if err != nil || got.Step != StepDone {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestAlreadyInRecognisesTheWrappedMessage(t *testing.T) {
	if !alreadyIn(errors.New("callback: this account is already signed in")) {
		t.Fatal("the wrapped text was not recognised")
	}
	if alreadyIn(errors.New("a phone number is required")) {
		t.Fatal("an unrelated error counted as already signed in")
	}
}

func TestLogoutAllDeletesTheSavedSession(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir, 1, "h", 0, Pace{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "42.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.LogoutAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.HasSaved() {
		t.Fatal("the session file was still there")
	}
}

func TestHasSavedIgnoresAPendingLogin(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir, 1, "h", 0, Pace{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.HasSaved() {
		t.Fatal("an empty folder looked signed in")
	}
	if err := os.WriteFile(filepath.Join(dir, "pending-1.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if m.HasSaved() {
		t.Fatal("a pending login counted as a saved account")
	}
	if err := os.WriteFile(filepath.Join(dir, "42.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !m.HasSaved() {
		t.Fatal("a saved session was missed")
	}
}

func TestPaceRefusesNegativeNumbers(t *testing.T) {
	good := Pace{MBPerSec: 8, RestMinutes: 5, LongEveryGB: 20, LongRestMinutes: 60, DailyGB: 50, Hours: "01-08"}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []Pace{{MBPerSec: -1}, {RestMinutes: -5}, {LongEveryGB: -1}, {LongRestMinutes: -1}, {DailyGB: -50}, {Hours: "25-99"}} {
		if p.Validate() == nil {
			t.Errorf("%+v was accepted and would stop the next start", p)
		}
	}
}

func TestAnAbandonedSignInDoesNotBlockTheNextOne(t *testing.T) {
	m := &Manager{}
	p := &pendingLogin{}
	m.pending = p
	m.clearPending(&pendingLogin{})
	if m.pending != p {
		t.Fatal("another sign-in's end removed this one")
	}
	m.clearPending(p)
	if m.pending != nil {
		t.Fatal("the finished sign-in still blocks a new one")
	}
}

func TestCancelLoginStopsTheWaitingSignIn(t *testing.T) {
	m := &Manager{}
	ctx, cancel := context.WithCancel(context.Background())
	m.pending = &pendingLogin{cancel: cancel}
	m.CancelLogin()
	if ctx.Err() == nil {
		t.Fatal("the sign-in that was waiting for a code kept running")
	}
	if m.pending != nil {
		t.Fatal("a cancelled sign-in still blocks the next one")
	}
	m.CancelLogin() // nothing pending is fine
}

func TestAnAccountWithoutAccessIsKeptAndChecked(t *testing.T) {
	sleep, _, stop := reconnectWait(errNotAdmin, reconnectStart)
	if stop || sleep != notAdminEvery {
		t.Fatalf("a missing admin right: sleep %v stop %v, want a steady re-check", sleep, stop)
	}
	// The delay of a dropped connection must not grow because of it.
	if _, next, _ := reconnectWait(errNotAdmin, 20*time.Second); next != 20*time.Second {
		t.Fatalf("delay became %v", next)
	}
	sleep, next, stop := reconnectWait(errors.New("connection reset"), 5*time.Second)
	if stop || sleep != 5*time.Second || next != 10*time.Second {
		t.Fatalf("a dropped connection: %v %v %v", sleep, next, stop)
	}
	if _, next, _ := reconnectWait(errors.New("x"), 4*time.Minute); next != reconnectMax {
		t.Fatalf("delay not capped: %v", next)
	}
	if _, _, stop := reconnectWait(errors.New("rpc error: SESSION_REVOKED"), reconnectStart); !stop {
		t.Fatal("a revoked session must not be retried")
	}
}

func TestConnectRemovesTheLeftoverOfAnInterruptedSignIn(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "pending-123.json")
	if err := os.WriteFile(left, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := New(dir, 1, "hash", 1, Pace{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(left); !os.IsNotExist(err) {
		t.Fatal("the unfinished sign-in file is still there")
	}
}
