//go:build windows

package main

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

const (
	createNewConsole       = 0x00000010
	createNoWindow         = 0x08000000
	createBreakawayFromJob = 0x01000000
)

var getConsoleWindow = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")

// hasConsoleWindow reports whether this copy shows a console window. A copy started from a terminal
// has one; a copy started hidden (a scheduled task, a service wrapper) has none.
func hasConsoleWindow() bool {
	if getConsoleWindow.Find() != nil {
		return false
	}
	h, _, _ := getConsoleWindow.Call()
	return h != 0
}

// detach starts the replacement the way this copy runs: with its own console window when this one
// has a window, so its log stays visible, and with no window at all when this one runs hidden.
func detach(cmd *exec.Cmd, breakaway bool) {
	flags := uint32(syscall.CREATE_NEW_PROCESS_GROUP)
	if hasConsoleWindow() {
		flags |= createNewConsole
	} else {
		flags |= createNoWindow
	}
	if breakaway {
		flags |= createBreakawayFromJob
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
}

func retryDetach() bool { return true }

func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	// STILL_ACTIVE (259) means it has not exited. A handle to an exited process can still open.
	return code == 259
}
