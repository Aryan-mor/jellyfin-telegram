package catalog

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
)

func TestSwitchSubtitleLanguage(t *testing.T) {
	ctx := context.Background()
	c, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Put(ctx, media.File{MessageID: 1, RelPath: "a", Date: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetSubtitleOutcome(ctx, 1, "online"); err != nil {
		t.Fatal(err)
	}
	outcome := func() string {
		f, err := c.Get(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		return f.SubtitleOutcome
	}

	// A first setting keeps the work done before the setting existed.
	if changed, err := c.SwitchSubtitleLanguage(ctx, "fa"); err != nil || changed || outcome() != "online" {
		t.Fatalf("first: %v, %v, %q", changed, err, outcome())
	}
	if changed, err := c.SwitchSubtitleLanguage(ctx, "fa"); err != nil || changed {
		t.Fatalf("same: %v, %v", changed, err)
	}
	if changed, err := c.SwitchSubtitleLanguage(ctx, "de"); err != nil || !changed || outcome() != "" {
		t.Fatalf("changed: %v, %v, %q", changed, err, outcome())
	}
}
