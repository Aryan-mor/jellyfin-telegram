//go:build windows

package managed

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func runsFile(pid int, exe string) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return false
	}
	want, err := filepath.Abs(exe)
	if err != nil {
		return false
	}
	return strings.EqualFold(windows.UTF16ToString(buf[:size]), want)
}

// stopGently ends the server. Windows offers no signal a console-less child can act on.
func stopGently(p *os.Process) error { return p.Kill() }

// hideWindow keeps Jellyfin from opening its own console. A console app started with no window
// of its own would otherwise flash a terminal every time it starts.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
}
