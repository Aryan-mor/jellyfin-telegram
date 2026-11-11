//go:build !windows

package managed

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

func imagePath(pid int) (string, bool) {
	got, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	if err != nil {
		return "", false
	}
	return got, true
}

func runsFile(pid int, exe string) bool {
	got, ok := imagePath(pid)
	if !ok {
		return false
	}
	want, err := filepath.Abs(exe)
	if err != nil {
		return false
	}
	return got == want
}

func runsInside(pid int, dir string) bool {
	got, ok := imagePath(pid)
	if !ok {
		return false
	}
	return pathInside(got, dir)
}

// stopGently asks the server to shut down, so it can close its database cleanly.
func stopGently(p *os.Process) error { return p.Signal(syscall.SIGTERM) }

func hideWindow(cmd *exec.Cmd) {}
