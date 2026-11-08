package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// restartStop ends the running server after a replacement process has been started.
var restartStop context.CancelFunc

const restartWaitEnv = "JFTG_RESTART_WAIT"

var restartGate sync.Mutex
var restartStarted bool

// scheduleRestart starts a new copy of this program, then stops the current one
// once the panel has had a moment to read the reply.
func scheduleRestart(stop context.CancelFunc) error {
	if stop == nil {
		return fmt.Errorf("restart is not available")
	}
	restartGate.Lock()
	defer restartGate.Unlock()
	if restartStarted {
		return nil
	}
	if err := spawnReplacement(); err != nil {
		return err
	}
	restartStarted = true
	go func() {
		time.Sleep(500 * time.Millisecond)
		stop()
	}()
	return nil
}

func replacementCommand() (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	if dir, err := os.Getwd(); err == nil {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), restartWaitEnv+"="+strconv.Itoa(os.Getpid()))
	return cmd, nil
}

func spawnReplacement() error {
	cmd, err := replacementCommand()
	if err != nil {
		return err
	}
	detach(cmd, true)
	if err := cmd.Start(); err == nil || !retryDetach() {
		return err
	}
	cmd, err = replacementCommand()
	if err != nil {
		return err
	}
	detach(cmd, false)
	return cmd.Start()
}

// waitForPredecessor blocks a process that was started by the panel until the
// copy it replaces has exited and released the listen port.
func waitForPredecessor() {
	raw := os.Getenv(restartWaitEnv)
	if raw == "" {
		return
	}
	_ = os.Unsetenv(restartWaitEnv)
	pid, err := strconv.Atoi(raw)
	if err != nil || pid <= 0 || pid == os.Getpid() {
		return
	}
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			time.Sleep(400 * time.Millisecond)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Fprintf(os.Stderr, "the previous process %d is still running\n", pid)
}
