//go:build !windows

package fuckjoin

import "syscall"

func killTree(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

func bindLifetime(int) func() { return func() {} }
