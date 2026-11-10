package fladder

import (
	"context"
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

	"github.com/Aryan-mor/jellyfin-telegram/internal/proc"
)

// Server runs the pinned Fladder image. The browser talks to Jellyfin and Seerr, so the
// container only serves the web app.
type Server struct {
	Root        string
	JellyfinURL string
	SeerrURL    string
	Log         *slog.Logger
}

// URL is how this machine reaches Fladder.
func (s *Server) URL() string { return "http://127.0.0.1:" + strconv.Itoa(Port) }

// Pull downloads the pinned image when it is not already present.
func Pull(ctx context.Context, log *slog.Logger) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("Fladder needs Docker. Install Docker, or set FLADDER_MANAGED=false")
	}
	if proc.Command(ctx, "docker", "image", "inspect", Image).Run() == nil {
		return nil
	}
	if log != nil {
		log.Info("downloading Fladder", "image", Image)
	}
	out, err := proc.Command(ctx, "docker", "pull", Image).CombinedOutput()
	if err != nil {
		return fmt.Errorf("download Fladder: %w (%s)", err, tail(out))
	}
	return nil
}

// Prepare removes a container left from the previous process before anything waits for it.
func (s *Server) Prepare(ctx context.Context) {
	_ = proc.Command(ctx, "docker", "rm", "-f", ContainerName).Run()
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
		log.Warn("Fladder stopped, restarting", "err", err, "ran", time.Since(began).Round(time.Second))
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) runOnce(ctx context.Context) error {
	logDir := filepath.Join(s.Root, "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	console, err := os.Create(filepath.Join(logDir, "console.log"))
	if err != nil {
		return err
	}
	defer console.Close()
	_ = proc.Command(ctx, "docker", "rm", "-f", ContainerName).Run()
	cmd := proc.Command(ctx, "docker", dockerArgs(s.JellyfinURL, s.SeerrURL)...)
	cmd.Stdout, cmd.Stderr = console, console
	cmd.Cancel = func() error {
		_ = proc.Command(context.Background(), "docker", "stop", "-t", "10", ContainerName).Run()
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

// dockerArgs builds the run command. BASE_URL and SEERR_BASE_URL are what the browser uses.
func dockerArgs(jellyfinURL, seerrURL string) []string {
	return []string{
		"run", "--rm", "--name", ContainerName, "--init", "--pull", "never",
		"-e", "BASE_URL=" + jellyfinURL,
		"-e", "SEERR_BASE_URL=" + seerrURL,
		"-e", "FLADDER_WEBPATH=" + WebPath,
		"-p", "127.0.0.1:" + strconv.Itoa(Port) + ":80",
		Image,
	}
}

// WaitReady blocks until Fladder answers.
func (s *Server) WaitReady(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL()+WebPath+"/", nil)
		if err != nil {
			return err
		}
		if res, err := http.DefaultClient.Do(req); err == nil {
			res.Body.Close()
			if res.StatusCode < 400 {
				return nil
			}
		}
		select {
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			return fmt.Errorf("Fladder did not start in time, see %s", filepath.Join(s.Root, "log"))
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
