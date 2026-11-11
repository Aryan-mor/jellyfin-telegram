//go:build windows

package seerr

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func startHolder(t *testing.T, dir string) int {
	t.Helper()
	src := filepath.Join(os.Getenv("SystemRoot"), "System32", "ping.exe")
	dst := filepath.Join(dir, "seerr", "node.exe")
	copyFile(t, src, dst)
	cmd := exec.Command(dst, "-n", "90", "127.0.0.1")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go cmd.Wait()
	return cmd.Process.Pid
}

func holderAlive(pid int) bool {
	out, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), strconv.Itoa(pid))
}

func stopHolder(pid int) {
	_ = exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run()
}
