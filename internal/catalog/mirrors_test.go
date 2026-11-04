package catalog

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
)

func TestMirrorsRoundTrip(t *testing.T) {
	c, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	ctx := context.Background()
	m, err := c.AddMirror(ctx, 50, 7, "Films", []string{"video", "movie"})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Kinds) != 2 || m.Kinds[0] != "movie" || m.Kinds[1] != "video" || !m.Wants("movie") || m.Wants("music") {
		t.Fatalf("kinds %+v", m.Kinds)
	}
	if _, err := c.AddMirror(ctx, 50, 7, "Again", []string{"movie"}); err == nil {
		t.Fatal("duplicate channel was stored")
	}
	if err := c.Put(ctx, media.File{
		MessageID: 3, ChannelID: 1, DocID: 9, Size: 4, MIME: "video/mp4",
		FileName: "Clip.mp4", Date: time.Unix(1, 0), RelPath: "Movies/Clip/Clip.strm",
	}); err != nil {
		t.Fatal(err)
	}
	pending, err := c.Unmirrored(ctx, m.ID)
	if err != nil || len(pending) != 1 || pending[0].MessageID != 3 {
		t.Fatalf("pending %+v %v", pending, err)
	}
	if err := c.MarkSent(ctx, m.ID, 3, 80); err != nil {
		t.Fatal(err)
	}
	n, err := c.MirrorSent(ctx, m.ID)
	if err != nil || n != 1 {
		t.Fatalf("sent %d %v", n, err)
	}
	if again, _ := c.Unmirrored(ctx, m.ID); len(again) != 0 {
		t.Fatalf("still pending %+v", again)
	}
	if _, err := c.UpdateMirror(ctx, m.ID, 8, "Films", []string{"series"}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteMirror(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := c.Mirrors(ctx); len(list) != 0 {
		t.Fatalf("left %+v", list)
	}
}
