//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func detach(cmd *exec.Cmd, _ bool) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func retryDetach() bool { return false }

func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
