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
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Server runs the pinned Seerr image. Docker is the only build Seerr publishes.
type Server struct {
	// Root holds the API key, the config directory and the log. The config directory is
	// what the container writes.
	Root   string
	APIKey string
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

// Pull downloads the pinned image when it is not already present.
func Pull(ctx context.Context, log *slog.Logger) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("Seerr needs Docker. Install Docker, or set SEERR_MANAGED=false")
	}
	if exec.CommandContext(ctx, "docker", "image", "inspect", Image).Run() == nil {
		return nil
	}
	if log != nil {
		log.Info("downloading Seerr", "image", Image)
	}
	cmd := exec.CommandContext(ctx, "docker", "pull", Image)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("download Seerr: %w (%s)", err, tail(out))
	}
	return nil
}

// Prepare removes a container left from the previous process. WaitReady must not run until
// this returns, or it can treat the dying copy as ready.
func (s *Server) Prepare(ctx context.Context) {
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", ContainerName).Run()
}

// Run keeps the container alive until ctx ends.
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
	// The image runs as uid 1000. The parent directory stays private to this user.
	if err := os.MkdirAll(configDir, 0o777); err != nil {
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

	// A container left behind still owns the name and the port.
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", ContainerName).Run()

	cmd := exec.CommandContext(ctx, "docker", dockerArgs(runtime.GOOS, configDir, s.APIKey)...)
	cmd.Stdout, cmd.Stderr = console, console
	cmd.Cancel = func() error {
		stop := exec.Command("docker", "stop", "-t", "10", ContainerName)
		_ = stop.Run()
		if cmd.Process != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = 20 * time.Second
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Wait()
}

// dockerArgs builds the run command. Linux shares the host network so Seerr can call the
// Jellyfin that listens on loopback. Other systems publish the port and use the host name
// Docker gives for this machine.
func dockerArgs(goos, configDir, apiKey string) []string {
	args := []string{
		"run", "--rm", "--name", ContainerName, "--init", "--pull", "never",
		"-e", "API_KEY=" + apiKey,
		"-e", "LOG_LEVEL=info",
		// Node gives each connection attempt 250 ms. Reaching this machine through Docker Desktop is
		// sometimes slower, and Seerr then failed at random with an empty error, even towards Jellyfin.
		// Turning the attempt race off made all of 6 test runs connect, against 5 of 6 before.
		"-e", "NODE_OPTIONS=--no-network-family-autoselection",
		"-v", bindMount(configDir),
	}
	if goos == "linux" {
		args = append(args,
			"--network", "host",
			"-e", "HOST=127.0.0.1",
			"-e", "PORT="+strconv.Itoa(Port),
		)
	} else {
		args = append(args,
			"-p", "127.0.0.1:"+strconv.Itoa(Port)+":5055",
			"--add-host", "host.docker.internal:host-gateway",
		)
	}
	return append(args, Image)
}

// JellyfinHost is the name Seerr, inside its container, uses to reach Jellyfin.
func JellyfinHost(goos string) string {
	if goos == "linux" {
		return "127.0.0.1"
	}
	return "host.docker.internal"
}

func bindMount(configDir string) string {
	return filepath.Clean(configDir) + ":/app/config"
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

func tail(b []byte) string {
	s := strings.TrimSpace(string(b))
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 240 {
		s = s[len(s)-240:]
	}
	return s
}
