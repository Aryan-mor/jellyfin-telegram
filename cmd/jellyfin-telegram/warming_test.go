package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

func TestUsersWarmingWaitsForASavedSignIn(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "7.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	saved, err := tguser.New(dir, 1, "hash", 1, tguser.Pace{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !usersWarming(saved, time.Now()) {
		t.Fatal("a saved sign-in should hold the panel")
	}
	if usersWarming(saved, time.Now().Add(-time.Minute)) {
		t.Fatal("a long wait should give up")
	}
	empty, err := tguser.New(t.TempDir(), 1, "hash", 1, tguser.Pace{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if usersWarming(empty, time.Now()) {
		t.Fatal("nothing saved is a real empty account list")
	}
	if usersWarming(nil, time.Now()) {
		t.Fatal("no manager")
	}
}
