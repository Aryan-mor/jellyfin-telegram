package mirror

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
)

func TestPassCopiesOnlyTheChosenParts(t *testing.T) {
	c, err := catalog.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	ctx := context.Background()
	for _, f := range []media.File{
		{MessageID: 1, ChannelID: 9, DocID: 1, Size: 1, MIME: "video/mp4", FileName: "Heat.1995.mkv", RelPath: "Movies/Heat/Heat.strm", Date: time.Unix(1, 0)},
		{MessageID: 2, ChannelID: 9, DocID: 2, Size: 1, MIME: "video/mp4", FileName: "clip.mp4", Caption: "birthday", RelPath: "Movies/clip/clip.strm", Date: time.Unix(2, 0)},
		{MessageID: 3, ChannelID: 9, DocID: 3, Size: 1, MIME: "audio/mpeg", FileName: "Song.mp3", RelPath: "Music/Song.strm", Date: time.Unix(3, 0)},
	} {
		if err := c.Put(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.AddMirror(ctx, 50, 7, "Films", []string{"movie"}); err != nil {
		t.Fatal(err)
	}
	var got []int
	s := &Service{Cat: c, Log: slog.New(slog.DiscardHandler), Copy: func(_ context.Context, bot int64, message int, channel int64) (int, error) {
		if bot != 7 || channel != 50 {
			t.Fatalf("copy bot %d channel %d", bot, channel)
		}
		got = append(got, message)
		return message + 100, nil
	}}
	if err := s.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("copied %v", got)
	}
	if err := s.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("copied again %v", got)
	}
}

func TestParseTarget(t *testing.T) {
	id, name, err := ParseTarget("https://t.me/c/1234567890/5")
	if err != nil || id != 1234567890 || name != "" {
		t.Fatalf("link %d %q %v", id, name, err)
	}
	id, name, err = ParseTarget("@Films")
	if err != nil || id != 0 || name != "Films" {
		t.Fatalf("user %d %q %v", id, name, err)
	}
	if _, _, err := ParseTarget(""); err == nil {
		t.Fatal("empty was accepted")
	}
}
