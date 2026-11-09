//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// backgroundEnv marks the copy that already left the console, so it does not start another.
const backgroundEnv = "JFTG_BACKGROUND"

var freeConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("FreeConsole")

// backgroundIfInteractive leaves serve running with no window.
//
// Started from a terminal or by a double-click, this copy starts a hidden one and exits, so
// closing the window does not stop Jellyfin. Started by the logon task, this copy is the one
// the task restarts, so it only detaches from the console it was given.
func backgroundIfInteractive() {
	if os.Getenv(backgroundEnv) != "" || os.Getenv("JFTG_FOREGROUND") != "" || os.Getenv(restartWaitEnv) != "" {
		return
	}
	if !hasConsoleWindow() {
		return
	}
	if schedulerParent(parentImage()) {
		_, _, _ = freeConsole.Call()
		return
	}
	if !spawnHidden() {
		return
	}
	fmt.Fprintln(os.Stderr, "jellyfin-telegram is running in the background. This window can be closed.")
	fmt.Fprintln(os.Stderr, "It starts again when you sign in to Windows.")
	os.Exit(0)
}

func spawnHidden() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	cmd := hiddenCommand(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow | syscall.CREATE_NEW_PROCESS_GROUP | createBreakawayFromJob,
	}
	if err := cmd.Start(); err == nil {
		return true
	}
	cmd = hiddenCommand(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow | syscall.CREATE_NEW_PROCESS_GROUP,
	}
	return cmd.Start() == nil
}

func hiddenCommand(exe string) *exec.Cmd {
	cmd := exec.Command(exe, os.Args[1:]...)
	if dir, err := os.Getwd(); err == nil {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), backgroundEnv+"=1")
	return cmd
}

// parentImage is the file name of the process that started this one.
func parentImage() string {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(snap)
	var ent windows.ProcessEntry32
	ent.Size = uint32(unsafe.Sizeof(ent))
	if err := windows.Process32First(snap, &ent); err != nil {
		return ""
	}
	self := windows.GetCurrentProcessId()
	var ppid uint32
	found := false
	for {
		if ent.ProcessID == self {
			ppid = ent.ParentProcessID
			found = true
			break
		}
		if windows.Process32Next(snap, &ent) != nil {
			break
		}
	}
	if !found {
		return ""
	}
	ent.Size = uint32(unsafe.Sizeof(ent))
	if windows.Process32First(snap, &ent) != nil {
		return ""
	}
	for {
		if ent.ProcessID == ppid {
			return strings.ToLower(windows.UTF16ToString(ent.ExeFile[:]))
		}
		if windows.Process32Next(snap, &ent) != nil {
			return ""
		}
	}
}
