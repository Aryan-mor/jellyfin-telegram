//go:build windows

package main

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

const (
	createNewConsole       = 0x00000010
	createBreakawayFromJob = 0x01000000
)

func detach(cmd *exec.Cmd, breakaway bool) {
	flags := uint32(syscall.CREATE_NEW_PROCESS_GROUP | createNewConsole)
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
	_ = windows.CloseHandle(h)
	return true
}
