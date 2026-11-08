package catalog

import (
	"context"
	"fmt"
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

func TestUnsharedMirrorPosts(t *testing.T) {
	c, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	ctx := context.Background()
	public, _ := c.AddMirror(ctx, 50, 7, "Public", []string{"movie"})
	private, _ := c.AddMirror(ctx, 51, 7, "Private", []string{"movie"})
	if err := c.SetMirrorUsername(ctx, public.ID, "pubmirror"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{3, 4} {
		if err := c.Put(ctx, media.File{
			MessageID: id, ChannelID: 1, DocID: int64(id), Size: 4, MIME: "video/mp4", Caption: "T\n#tm_m_603",
			FileName: "Clip.mp4", Date: time.Unix(1, 0), RelPath: fmt.Sprintf("Movies/Clip%d/Clip.strm", id),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Message 3 reached both mirrors, message 4 only the public one but without a new id.
	for _, m := range []Mirror{public, private} {
		if err := c.MarkSent(ctx, m.ID, 3, 80); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.MarkSent(ctx, public.ID, 4, 0); err != nil {
		t.Fatal(err)
	}
	posts, err := c.UnsharedMirrorPosts(ctx, 10)
	if err != nil || len(posts) != 1 {
		t.Fatalf("posts %+v, %v", posts, err)
	}
	if p := posts[0]; p.Username != "pubmirror" || p.DestID != 80 || p.MessageID != 3 || p.Caption != "T\n#tm_m_603" {
		t.Errorf("post %+v", p)
	}
	if err := c.MarkShared(ctx, posts[0].MirrorID, posts[0].MessageID); err != nil {
		t.Fatal(err)
	}
	if err := c.MarkShared(ctx, posts[0].MirrorID, posts[0].MessageID); err != nil {
		t.Fatal("marking twice failed:", err)
	}
	if again, _ := c.UnsharedMirrorPosts(ctx, 10); len(again) != 0 {
		t.Errorf("still queued: %+v", again)
	}
}
