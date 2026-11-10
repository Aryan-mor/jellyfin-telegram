package main

import (
	"os"
	"reflect"
	"strconv"
	"testing"

	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
)

func TestThisProcessIsAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("this process looks dead")
	}
	if processAlive(1 << 30) {
		t.Fatal("a made-up pid looks alive")
	}
}

func TestRestartChildReloadsFileAfterWaitClearsFlag(t *testing.T) {
	t.Setenv(restartWaitEnv, strconv.Itoa(os.Getpid()))
	load := settingsLoader()
	waitForPredecessor()
	if os.Getenv(restartWaitEnv) != "" {
		t.Fatal("wait should clear the restart flag")
	}
	if reflect.ValueOf(load).Pointer() != reflect.ValueOf(config.Reload).Pointer() {
		t.Fatal("restart child should reload the settings file")
	}
}

func TestNormalStartKeepsEnvironmentOverFile(t *testing.T) {
	t.Setenv(restartWaitEnv, "")
	load := settingsLoader()
	if reflect.ValueOf(load).Pointer() != reflect.ValueOf(config.Load).Pointer() {
		t.Fatal("a normal start should keep the environment")
	}
}
