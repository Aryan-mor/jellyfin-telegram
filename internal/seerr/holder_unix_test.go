//go:build !windows

package seerr

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func startHolder(t *testing.T, dir string) int {
	t.Helper()
	src, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep not on PATH")
	}
	dst := filepath.Join(dir, "seerr", "node.exe")
	copyFile(t, src, dst)
	if err := os.Chmod(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(dst, "90")
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go cmd.Wait()
	return cmd.Process.Pid
}

func holderAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil
}

func stopHolder(pid int) {
	_ = syscall.Kill(pid, syscall.SIGKILL)
}
