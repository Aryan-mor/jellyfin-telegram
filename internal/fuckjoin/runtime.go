// Package fuckjoin runs https://github.com/AmirHBuilds/fuckjoin beside this program.
// The checkout lives under the data directory and is not part of this source tree, so a newer
// upstream commit replaces it the way a newer Jellyfin build replaces the managed one.
// This program talks to it the way its own userbot listens: a message in Saved Messages.
package fuckjoin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	// Repo is the upstream project. Nothing from it is copied into this repository.
	Repo = "https://github.com/AmirHBuilds/fuckjoin.git"
	// DefaultCommit is the known-good revision. Update moves the checkout to origin/main.
	DefaultCommit = "1b28f6ee958c8b9b13578ffb224522a510333e6e"
)

// ErrNeedLogin means the upstream session file is not signed in yet.
var ErrNeedLogin = errors.New("sign in fuckjoin with the same Telegram account that reads the channels")

// Runtime owns the checkout, the virtualenv and the running userbot.
type Runtime struct {
	Root    string
	APIID   int
	APIHash string
	Log     *slog.Logger

	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan struct{}
	release func()
	commit  string
	phase   string
	phone   string
	hash    string
	note    string
	ready   bool
	checked bool
}

// New prepares paths. It does not download or start anything.
func New(dataDir string, apiID int, apiHash string, log *slog.Logger) *Runtime {
	return &Runtime{Root: filepath.Join(dataDir, "fuckjoin"), APIID: apiID, APIHash: apiHash, Log: log, phase: "phone"}
}

// Status is what the panel shows about the upstream checkout.
type Status struct {
	Commit  string `json:"commit"`
	Running bool   `json:"running"`
	Ready   bool   `json:"ready"`
	Phase   string `json:"phase"`
	Note    string `json:"note,omitempty"`
}

// View reports the checkout without starting it.
func (r *Runtime) View() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	commit := r.commit
	if commit == "" {
		commit = r.storedCommit()
	}
	running := r.cmd != nil && r.cmd.Process != nil && r.cmd.ProcessState == nil
	return Status{Commit: short(commit), Running: running, Ready: r.ready, Phase: r.phase, Note: r.note}
}

func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

func (r *Runtime) storedCommit() string {
	b, err := os.ReadFile(filepath.Join(r.Root, "COMMIT"))
	if err != nil || strings.TrimSpace(string(b)) == "" {
		return DefaultCommit
	}
	return strings.TrimSpace(string(b))
}

// EnsureRunning makes sure the pinned checkout is present and the userbot is up.
func (r *Runtime) EnsureRunning(ctx context.Context) error {
	r.mu.Lock()
	running := r.cmd != nil && r.cmd.Process != nil && r.cmd.ProcessState == nil
	ready := r.ready
	r.mu.Unlock()
	if running && ready {
		return nil
	}
	if err := r.prepare(ctx); err != nil {
		return err
	}
	if err := r.ensureAuthorized(ctx); err != nil {
		return err
	}
	return r.start()
}

// Update fetches origin/main, checks that out, reinstalls requirements and restarts the userbot.
func (r *Runtime) Update(ctx context.Context) (string, error) {
	if r.APIID == 0 || r.APIHash == "" {
		return "", errors.New("Telegram api_id and api_hash are required before fuckjoin can start")
	}
	src := filepath.Join(r.Root, "src")
	if _, err := os.Stat(filepath.Join(src, ".git")); err != nil {
		if err := r.clone(ctx, DefaultCommit); err != nil {
			return "", err
		}
	}
	if _, err := git(ctx, src, "fetch", "--depth", "1", "origin", "main"); err != nil {
		return "", err
	}
	out, err := git(ctx, src, "rev-parse", "FETCH_HEAD")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(out)
	if err := r.checkout(ctx, sha); err != nil {
		return "", err
	}
	if err := os.Remove(filepath.Join(r.Root, "installed")); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := r.ensureVenv(ctx); err != nil {
		return "", err
	}
	r.stop()
	r.mu.Lock()
	ready := r.ready
	r.mu.Unlock()
	if ready {
		if err := r.start(); err != nil {
			return sha, err
		}
	}
	return sha, nil
}

func (r *Runtime) prepare(ctx context.Context) error {
	if r.APIID == 0 || r.APIHash == "" {
		return errors.New("Telegram api_id and api_hash are required before fuckjoin can start")
	}
	commit := r.storedCommit()
	srcMain := filepath.Join(r.Root, "src", "app", "__main__.py")
	if _, err := os.Stat(srcMain); err != nil {
		if err := r.clone(ctx, commit); err != nil {
			return err
		}
	}
	return r.ensureVenv(ctx)
}

func (r *Runtime) clone(ctx context.Context, commit string) error {
	src := filepath.Join(r.Root, "src")
	if err := os.MkdirAll(src, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(src, ".git")); err != nil {
		if _, err := git(ctx, src, "init"); err != nil {
			return err
		}
		if _, err := git(ctx, src, "remote", "add", "origin", Repo); err != nil {
			return err
		}
	}
	return r.checkout(ctx, commit)
}

func (r *Runtime) checkout(ctx context.Context, commit string) error {
	src := filepath.Join(r.Root, "src")
	if head, err := git(ctx, src, "rev-parse", "HEAD"); err == nil && strings.TrimSpace(head) == commit {
		return r.remember(commit)
	}
	if _, err := git(ctx, src, "fetch", "--depth", "1", "origin", commit); err != nil {
		return fmt.Errorf("fetch fuckjoin %s: %w", short(commit), err)
	}
	if _, err := git(ctx, src, "checkout", "--detach", "FETCH_HEAD"); err != nil {
		return err
	}
	return r.remember(commit)
}

func (r *Runtime) remember(commit string) error {
	if err := os.WriteFile(filepath.Join(r.Root, "COMMIT"), []byte(commit+"\n"), 0o600); err != nil {
		return err
	}
	r.mu.Lock()
	r.commit = commit
	r.mu.Unlock()
	return nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(buf.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return buf.String(), nil
}

func (r *Runtime) ensureVenv(ctx context.Context) error {
	py := r.venvPython()
	stamp := filepath.Join(r.Root, "installed")
	commit := r.storedCommit()
	if b, err := os.ReadFile(stamp); err == nil && strings.TrimSpace(string(b)) == commit {
		if _, err := os.Stat(py); err == nil {
			return nil
		}
	}
	system, err := systemPython()
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	mk := exec.CommandContext(cctx, system[0], append(system[1:], "-m", "venv", filepath.Join(r.Root, "venv"))...)
	if out, err := mk.CombinedOutput(); err != nil {
		return fmt.Errorf("create virtualenv: %s", oneLine(out))
	}
	pip := exec.CommandContext(cctx, py, "-m", "pip", "install", "-r", absPath(filepath.Join(r.Root, "src", "requirements.txt")))
	if out, err := pip.CombinedOutput(); err != nil {
		return fmt.Errorf("pip install fuckjoin: %s", oneLine(out))
	}
	return os.WriteFile(stamp, []byte(commit+"\n"), 0o600)
}

func oneLine(out []byte) string {
	msg := strings.TrimSpace(string(out))
	if len(msg) > 400 {
		msg = msg[len(msg)-400:]
	}
	return msg
}

func (r *Runtime) venvPython() string {
	name := "bin/python"
	if runtime.GOOS == "windows" {
		name = filepath.Join("Scripts", "python.exe")
	}
	return absPath(filepath.Join(r.Root, "venv", name))
}

// absPath keeps the interpreter and the checkout absolute. On Windows, Go resolves a relative
// executable against Cmd.Dir, so data\fuckjoin\venv\Scripts\python.exe was looked up inside src and never found.
func absPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

func systemPython() ([]string, error) {
	candidates := [][]string{{"python3"}, {"python"}, {"py", "-3"}}
	for _, c := range candidates {
		cmd := exec.Command(c[0], append(c[1:], "-c", "import sys; raise SystemExit(0 if sys.version_info>=(3,10) else 1)")...)
		if cmd.Run() == nil {
			return c, nil
		}
	}
	return nil, errors.New("Python 3.10 or newer is required to run fuckjoin. Install it, or turn news channels off")
}

func (r *Runtime) sessionName() string {
	return absPath(filepath.Join(r.Root, "session", "fuckjoin"))
}

func (r *Runtime) env() []string {
	return append(os.Environ(),
		fmt.Sprintf("API_ID=%d", r.APIID),
		"API_HASH="+r.APIHash,
		"SESSION_NAME="+r.sessionName(),
		"PYTHONUTF8=1",
	)
}

func (r *Runtime) ensureAuthorized(ctx context.Context) error {
	r.mu.Lock()
	if r.ready {
		r.mu.Unlock()
		return nil
	}
	checked := r.checked
	r.mu.Unlock()
	if checked {
		return ErrNeedLogin
	}
	if err := os.MkdirAll(filepath.Dir(r.sessionName()), 0o700); err != nil {
		return err
	}
	out, err := r.runPy(ctx, "check")
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.checked = true
	if strings.TrimSpace(out) == "ok" {
		r.ready = true
		r.phase = "ready"
	}
	ready := r.ready
	r.mu.Unlock()
	if ready {
		return nil
	}
	return ErrNeedLogin
}

// SendCode asks Telegram for a login code. The session is the one fuckjoin will reuse.
func (r *Runtime) SendCode(ctx context.Context, phone string) error {
	phone = strings.TrimSpace(phone)
	if !validPhone(phone) {
		return errors.New("enter the phone number with the country code, such as +98912…")
	}
	if err := r.prepare(ctx); err != nil {
		return err
	}
	out, err := r.runPy(ctx, "send", phone)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.phone, r.hash, r.phase, r.note = phone, strings.TrimSpace(out), "code", ""
	r.mu.Unlock()
	return nil
}

// SignIn finishes the code step. A cloud password switches the phase instead of signing in.
func (r *Runtime) SignIn(ctx context.Context, code string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return errors.New("enter the code Telegram sent")
	}
	r.mu.Lock()
	phone, hash := r.phone, r.hash
	r.mu.Unlock()
	if phone == "" || hash == "" {
		return errors.New("ask for a code first")
	}
	out, err := r.runPy(ctx, "code", phone, hash, code)
	if err != nil {
		return err
	}
	switch strings.TrimSpace(out) {
	case "ok":
		return r.markReady()
	case "password":
		r.mu.Lock()
		r.phase = "password"
		r.mu.Unlock()
		return nil
	default:
		return errors.New(strings.TrimSpace(out))
	}
}

// Password finishes a two-step login.
func (r *Runtime) Password(ctx context.Context, password string) error {
	if strings.TrimSpace(password) == "" {
		return errors.New("enter the Telegram cloud password")
	}
	out, err := r.runPy(ctx, "password", password)
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "ok" {
		return errors.New(strings.TrimSpace(out))
	}
	return r.markReady()
}

func (r *Runtime) markReady() error {
	r.mu.Lock()
	r.ready, r.phase, r.checked, r.note = true, "ready", true, ""
	r.mu.Unlock()
	return r.start()
}

func (r *Runtime) runPy(ctx context.Context, action string, args ...string) (string, error) {
	if err := os.MkdirAll(r.Root, 0o700); err != nil {
		return "", err
	}
	// Telethon does not create the parent of the session file. Without this, sqlite says it cannot open the database.
	if err := os.MkdirAll(filepath.Dir(r.sessionName()), 0o700); err != nil {
		return "", err
	}
	script := absPath(filepath.Join(r.Root, "login.py"))
	if err := os.WriteFile(script, []byte(loginScript), 0o600); err != nil {
		return "", err
	}
	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	argv := append([]string{script, action}, args...)
	env := r.env()
	if action == "password" {
		argv = []string{script, action}
		env = append(env, "FUCKJOIN_PASSWORD="+args[0])
	}
	cmd := exec.CommandContext(cctx, r.venvPython(), argv...)
	cmd.Dir = absPath(filepath.Join(r.Root, "src"))
	cmd.Env = env
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(buf.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return buf.String(), nil
}

func (r *Runtime) inboxPath() string {
	return absPath(filepath.Join(r.Root, "inbox.txt"))
}

func (r *Runtime) driveScript() string {
	return absPath(filepath.Join(r.Root, "drive.py"))
}

// Submit hands one bot link to the running fuckjoin process. It posts the link itself, because a
// message sent by the other Telegram session does not wake that process.
func (r *Runtime) Submit(text string) error {
	return os.WriteFile(r.inboxPath(), []byte(strings.TrimSpace(text)+"\n"), 0o600)
}

func (r *Runtime) start() error {
	r.mu.Lock()
	if r.cmd != nil && r.cmd.Process != nil && r.cmd.ProcessState == nil {
		r.mu.Unlock()
		return nil
	}
	if !r.ready {
		r.mu.Unlock()
		return ErrNeedLogin
	}
	if err := os.WriteFile(r.driveScript(), []byte(driveScript), 0o600); err != nil {
		r.mu.Unlock()
		return err
	}
	cmd := exec.Command(r.venvPython(), r.driveScript())
	cmd.Dir = absPath(filepath.Join(r.Root, "src"))
	cmd.Env = append(r.env(), "FUCKJOIN_INBOX="+r.inboxPath(), "PYTHONUNBUFFERED=1")
	ready := make(chan struct{})
	var once sync.Once
	sink := logWriter{log: r.Log, ready: func() { once.Do(func() { close(ready) }) }}
	cmd.Stdout = sink
	cmd.Stderr = sink
	if err := cmd.Start(); err != nil {
		r.mu.Unlock()
		return err
	}
	done := make(chan struct{})
	r.cmd, r.done, r.release = cmd, done, bindLifetime(cmd.Process.Pid)
	r.mu.Unlock()
	go func() {
		_ = cmd.Wait()
		close(done)
		r.mu.Lock()
		if r.cmd == cmd {
			r.cmd = nil
		}
		r.mu.Unlock()
	}()
	if r.Log != nil {
		r.Log.Info("fuckjoin running", "commit", short(r.commit))
	}
	select {
	case <-ready:
		return nil
	case <-done:
		return errors.New("fuckjoin exited before it was ready")
	case <-time.After(45 * time.Second):
		r.stop()
		return errors.New("fuckjoin did not become ready")
	}
}

const driveScript = `"""Run fuckjoin and post queued links from this same session."""
import asyncio
import logging
import os
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / "src"))

from telethon import TelegramClient, events
import runpy

_orig = TelegramClient.start


async def _inbox(client):
    path = Path(os.environ["FUCKJOIN_INBOX"])
    while True:
        await asyncio.sleep(0.5)
        try:
            text = path.read_text(encoding="utf-8").strip()
        except (FileNotFoundError, OSError):
            continue
        if not text:
            continue
        try:
            path.unlink()
        except OSError:
            pass
        try:
            msg = await client.send_message("me", text)
            # The short update for a message this client just sent has no text,
            # so the handler would never see the link. Give it the result directly.
            await client._dispatch_event(events.NewMessage.Event(msg))
        except Exception:
            logging.exception("inbox could not post the link")
        else:
            logging.info("inbox handed a link to Saved Messages")


async def _start(self, *args, **kwargs):
    result = await _orig(self, *args, **kwargs)
    asyncio.create_task(_inbox(self))
    return result


TelegramClient.start = _start
runpy.run_module("app", run_name="__main__")
`

// Interrupt kills a userbot that is stuck on one link, so the next link is not queued behind it.
func (r *Runtime) Interrupt() { r.stop() }

func (r *Runtime) stop() {
	r.mu.Lock()
	cmd, done, release := r.cmd, r.done, r.release
	r.release = nil
	r.mu.Unlock()
	if release != nil {
		defer release()
	}
	if cmd == nil || cmd.Process == nil {
		return
	}
	killTree(cmd.Process.Pid)
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
}

type logWriter struct {
	log   *slog.Logger
	ready func()
}

func (w logWriter) Write(p []byte) (int, error) {
	line := strings.TrimSpace(string(p))
	if line != "" && w.ready != nil && strings.Contains(line, "Ready. Forward") {
		w.ready()
	}
	if w.log != nil && line != "" {
		w.log.Info("fuckjoin", "line", line)
	}
	return len(p), nil
}

func validPhone(s string) bool {
	if len(s) < 8 || len(s) > 16 || s[0] != '+' {
		return false
	}
	for _, r := range s[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

const loginScript = `"""Sign the upstream fuckjoin session in. Delivery stays in the upstream program."""
import os, sys
from telethon import TelegramClient
from telethon.errors import SessionPasswordNeededError

async def main():
    client = TelegramClient(os.environ["SESSION_NAME"], int(os.environ["API_ID"]), os.environ["API_HASH"])
    await client.connect()
    action = sys.argv[1]
    try:
        if action == "check":
            print("ok" if await client.is_user_authorized() else "no")
        elif action == "send":
            sent = await client.send_code_request(sys.argv[2])
            print(sent.phone_code_hash)
        elif action == "code":
            try:
                await client.sign_in(phone=sys.argv[2], phone_code_hash=sys.argv[3], code=sys.argv[4])
                print("ok")
            except SessionPasswordNeededError:
                print("password")
        elif action == "password":
            await client.sign_in(password=os.environ["FUCKJOIN_PASSWORD"])
            print("ok")
        else:
            raise SystemExit("unknown action")
    finally:
        await client.disconnect()

if __name__ == "__main__":
    import asyncio
    asyncio.run(main())
`
