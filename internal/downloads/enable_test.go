package downloads

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

func TestPausedDownloadsCannotBeTurnedOn(t *testing.T) {
	if config.DownloadsAvailable {
		t.Skip("downloads are available")
	}
	s := New(false)
	err := s.SetEnabled(context.Background(), true)
	if err == nil || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("%v", err)
	}
}

func TestEnableWithoutSession(t *testing.T) {
	if !config.DownloadsAvailable {
		t.Skip("downloads are paused")
	}
	dir := t.TempDir()
	users, err := tguser.New(filepath.Join(dir, "users"), 1, "hash", 2, tguser.Pace{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := New(false)
	s.Users = users
	s.EnvPath = filepath.Join(dir, ".env")
	err = s.SetEnabled(context.Background(), true)
	if err == nil || !strings.Contains(err.Error(), "sign in") {
		t.Fatalf("%v", err)
	}
}

func TestSearchIsDueOnlyAfterTheInterval(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	hour := time.Hour
	if !searchDue(time.Time{}, now, hour) {
		t.Error("the first search must run at once")
	}
	if searchDue(now.Add(-59*time.Minute), now, hour) {
		t.Error("searched again after 59 minutes")
	}
	if !searchDue(now.Add(-60*time.Minute), now, hour) {
		t.Error("did not search after an hour")
	}
	if searchDue(time.Time{}, now, 0) {
		t.Error("a zero interval must turn the repeat off")
	}
}
