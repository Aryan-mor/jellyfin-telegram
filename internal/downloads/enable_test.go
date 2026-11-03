package downloads

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

func TestEnableWithoutSession(t *testing.T) {
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
