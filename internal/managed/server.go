package managed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

// Server runs Jellyfin as a child process, listening on loopback only. Everything from outside
// reaches it through this program's single port, which is why it trusts 127.0.0.1 as a proxy.
type Server struct {
	Paths   Paths
	DataDir string
	Port    int
	Log     *slog.Logger
}

const networkConfig = `<?xml version="1.0" encoding="utf-8"?>
<NetworkConfiguration xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <InternalHttpPort>%d</InternalHttpPort>
  <PublicHttpPort>%d</PublicHttpPort>
  <EnableRemoteAccess>true</EnableRemoteAccess>
  <LocalNetworkAddresses>
    <string>127.0.0.1</string>
  </LocalNetworkAddresses>
  <KnownProxies>
    <string>127.0.0.1</string>
  </KnownProxies>
  <AutoDiscovery>false</AutoDiscovery>
</NetworkConfiguration>
`

// URL returns the address of the running server.
func (s *Server) URL() string { return "http://127.0.0.1:" + strconv.Itoa(s.Port) }

// Prepare ends a copy left over from an earlier run. It must finish before anything waits for the
// server, because the old copy still answers and would pass for the new one until it is stopped.
func (s *Server) Prepare() {
	stopStale(s.pidFile(), s.Paths.Server, s.Log)
}

func (s *Server) pidFile() string { return filepath.Join(s.DataDir, "jellyfin.pid") }

// Run keeps Jellyfin alive until ctx ends.
func (s *Server) Run(ctx context.Context) {
	for ctx.Err() == nil {
		began := time.Now()
		err := s.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		s.Log.Warn("Jellyfin stopped, restarting", "err", err, "ran", time.Since(began).Round(time.Second))
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) runOnce(ctx context.Context) error {
	configDir := filepath.Join(s.DataDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return err
	}
	// Only a missing file is written. Jellyfin rewrites this file itself, and settings changed in
	// its dashboard must survive.
	network := filepath.Join(configDir, "network.xml")
	if _, err := os.Stat(network); errors.Is(err, os.ErrNotExist) {
		content := fmt.Sprintf(networkConfig, s.Port, s.Port)
		if err := os.WriteFile(network, []byte(content), 0o644); err != nil {
			return err
		}
	}

	pidFile := s.pidFile()

	logDir := filepath.Join(s.DataDir, "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	out, err := os.Create(filepath.Join(logDir, "console.log"))
	if err != nil {
		return err
	}
	defer out.Close()

	cmd := exec.CommandContext(ctx, s.Paths.Server,
		"--datadir", s.DataDir,
		"--configdir", configDir,
		"--cachedir", filepath.Join(s.DataDir, "cache"),
		"--logdir", logDir,
		"--webdir", s.Paths.WebDir,
		"--ffmpeg", s.Paths.FFmpeg,
	)
	cmd.Dir = filepath.Dir(s.Paths.Server)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.Env = os.Environ()
	hideWindow(cmd)
	if runtime.GOOS != "windows" {
		// Small Linux systems often lack libicu, without which the server refuses to start.
		cmd.Env = append(cmd.Env, "DOTNET_SYSTEM_GLOBALIZATION_INVARIANT=1")
	}
	cmd.Cancel = func() error { return stopGently(cmd.Process) }
	cmd.WaitDelay = 15 * time.Second
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		s.Log.Warn("cannot record the Jellyfin process id", "err", err)
	}
	err = cmd.Wait()
	_ = os.Remove(pidFile)
	return err
}

// stopStale ends a copy left running by an earlier crash, which would hold the port. The process is
// only touched if it really runs this server's file, because the id may have been reused since.
func stopStale(pidFile, exe string, log *slog.Logger) {
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(string(raw))
	if err != nil || pid <= 0 || !runsFile(pid, exe) {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	log.Info("stopping a Jellyfin left over from an earlier run", "pid", pid)
	_ = proc.Kill()
	// Wait until it is actually gone. A fixed pause let the next start treat the dying process
	// as ready, then lose it mid-setup.
	deadline := time.Now().Add(15 * time.Second)
	for runsFile(pid, exe) && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
}

// WaitReady blocks until the server answers.
func (s *Server) WaitReady(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL()+"/System/Info/Public", nil)
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
			return fmt.Errorf("Jellyfin did not start in time, see %s", filepath.Join(s.DataDir, "log"))
		}
	}
}
