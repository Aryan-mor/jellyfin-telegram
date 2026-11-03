//go:build !windows

package managed

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

func runsFile(pid int, exe string) bool {
	got, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	if err != nil {
		return false
	}
	want, err := filepath.Abs(exe)
	if err != nil {
		return false
	}
	return got == want
}

// stopGently asks the server to shut down, so it can close its database cleanly.
func stopGently(p *os.Process) error { return p.Signal(syscall.SIGTERM) }
