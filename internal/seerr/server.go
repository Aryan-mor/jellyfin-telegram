package seerr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/managed"
)

// Server runs the installed Seerr as an ordinary child process.
type Server struct {
	// Root holds the API key, the config directory, the log and the installed bundle.
	Root   string
	APIKey string
	Bundle Bundle
	Log    *slog.Logger
}

// URL is how this machine reaches Seerr.
func (s *Server) URL() string { return "http://127.0.0.1:" + strconv.Itoa(Port) }

// EnsureAPIKey returns the key Seerr must keep using. A new key on a later start would
// replace the one already stored in Seerr's settings.
func EnsureAPIKey(root string) (string, error) {
	path := filepath.Join(root, "api.key")
	raw, err := os.ReadFile(path)
	if err == nil {
		if key := strings.TrimSpace(string(raw)); key != "" {
			return key, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	key := hex.EncodeToString(buf)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	return key, os.WriteFile(path, []byte(key+"\n"), 0o600)
}

func (s *Server) pidFile() string { return filepath.Join(s.Root, "seerr.pid") }

// Prepare ends a copy left running by an earlier crash. It must finish before WaitReady, or the
// dying copy can pass for the new one.
func (s *Server) Prepare() {
	managed.StopStale(s.pidFile(), s.Bundle.Node, s.Log)
}

// Run keeps Seerr alive until ctx ends.
func (s *Server) Run(ctx context.Context) {
	log := s.Log
	if log == nil {
		log = slog.Default()
	}
	for ctx.Err() == nil {
		began := time.Now()
		err := s.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		log.Warn("Seerr stopped, restarting", "err", err, "ran", time.Since(began).Round(time.Second))
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) runOnce(ctx context.Context) error {
	configDir := filepath.Join(s.Root, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return err
	}
	logDir := filepath.Join(s.Root, "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	console, err := os.Create(filepath.Join(logDir, "console.log"))
	if err != nil {
		return err
	}
	defer console.Close()

	cmd := exec.CommandContext(ctx, s.Bundle.Node, "dist/index.js")
	cmd.Dir = s.Bundle.App
	cmd.Stdout, cmd.Stderr = console, console
	cmd.Env = env(configDir, s.APIKey)
	managed.HideWindow(cmd)
	cmd.Cancel = func() error { return managed.StopGently(cmd.Process) }
	cmd.WaitDelay = 20 * time.Second
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := os.WriteFile(s.pidFile(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil && s.Log != nil {
		s.Log.Warn("cannot record the Seerr process id", "err", err)
	}
	err = cmd.Wait()
	_ = os.Remove(s.pidFile())
	return err
}

// env is this program's environment with Seerr's settings added. HOST keeps it on loopback, so
// only this program reaches it, and the panel serves it to others through its own port.
func env(configDir, apiKey string) []string {
	own := map[string]string{
		"NODE_ENV":         "production",
		"API_KEY":          apiKey,
		"LOG_LEVEL":        "info",
		"HOST":             "127.0.0.1",
		"PORT":             strconv.Itoa(Port),
		"CONFIG_DIRECTORY": configDir,
	}
	var out []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		// A setting of the user's own shell must not change how Seerr runs here.
		if _, ours := own[strings.ToUpper(name)]; ours || strings.EqualFold(name, "NODE_OPTIONS") {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range own {
		out = append(out, k+"="+v)
	}
	return out
}

// WaitReady blocks until Seerr answers.
func (s *Server) WaitReady(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL()+"/api/v1/settings/public", nil)
		if err != nil {
			return err
		}
		if res, err := http.DefaultClient.Do(req); err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			return fmt.Errorf("Seerr did not start in time, see %s", filepath.Join(s.Root, "log"))
		}
	}
}
