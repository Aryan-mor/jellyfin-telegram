package newsfeed

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

type fakeTG struct {
	page      tguser.Page
	forwarded []int
}

func (f *fakeTG) Lookup(context.Context, string) (tguser.Channel, error) {
	return tguser.Channel{}, nil
}
func (f *fakeTG) History(context.Context, tguser.SourceRef, int, int) (tguser.Page, error) {
	return f.page, nil
}
func (f *fakeTG) HasFreeSession() bool { return true }
func (f *fakeTG) HasAccount() bool     { return true }
func (f *fakeTG) ForwardSaved(_ context.Context, ids []int) error {
	f.forwarded = append(f.forwarded, ids...)
	return nil
}

type fakeOpen struct {
	posts []tguser.Post
	calls int
}

func (f *fakeOpen) Open(context.Context, string) ([]tguser.Post, error) {
	f.calls++
	return f.posts, nil
}

func TestABotLinkIsReviewedThenForwardedOnce(t *testing.T) {
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cat.Close() })
	ctx := context.Background()
	if _, err := cat.AddNews(ctx, catalog.Source{ChannelID: 50, Title: "Fans"}); err != nil {
		t.Fatal(err)
	}
	tg := &fakeTG{page: tguser.Page{
		Highest: 9, Lowest: 9,
		Links: []tguser.StartHit{{
			ID: 9, Date: time.Now(), URL: "https://t.me/cinamafansbot?start=87813508",
		}},
	}}
	video := tguser.Post{ID: 40, DocID: 7, Name: "Primetime.2026.mkv", Size: 800 << 20, MIME: "video/x-matroska", Duration: 100 * time.Minute}
	op := &fakeOpen{posts: []tguser.Post{video, {ID: 41, DocID: 8, Name: "note.txt", Size: 10, MIME: "text/plain"}}}
	svc := New()
	svc.Cat, svc.TG, svc.Open = cat, tg, op
	set := Defaults()
	set.Enabled = true
	set.PauseSeconds = 0
	if err := svc.Save(ctx, set); err != nil {
		t.Fatal(err)
	}
	svc.Pass(ctx)
	if op.calls != 1 || len(tg.forwarded) != 1 || tg.forwarded[0] != 40 {
		t.Fatalf("calls %d forwarded %v", op.calls, tg.forwarded)
	}
	svc.Pass(ctx)
	if op.calls != 1 {
		t.Fatalf("opened again: %d", op.calls)
	}
	same, err := cat.CopiedFile(ctx, video.Name, video.Size)
	if err != nil || !same {
		t.Fatalf("copied %v %v", same, err)
	}
}

func TestAFloodStopsTheChannelInsteadOfOpeningEveryLink(t *testing.T) {
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cat.Close() })
	ctx := context.Background()
	src, err := cat.AddNews(ctx, catalog.Source{ChannelID: 50, Title: "Cinema Fans"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tg := &fakeTG{page: tguser.Page{
		Highest: 3, Lowest: 1,
		Links: []tguser.StartHit{
			{ID: 3, Date: now, URL: "https://t.me/bot?start=a"},
			{ID: 2, Date: now, URL: "https://t.me/bot?start=b"},
			{ID: 1, Date: now, URL: "https://t.me/bot?start=c"},
		},
	}}
	op := &failOpen{}
	svc := New()
	svc.Cat, svc.TG, svc.Open = cat, tg, op
	set := Defaults()
	set.Enabled = true
	set.PauseSeconds = 0
	if err := svc.Save(ctx, set); err != nil {
		t.Fatal(err)
	}
	svc.Pass(ctx)
	if op.calls != 1 {
		t.Fatalf("opened %d links after a flood, want 1", op.calls)
	}
	row, ok, err := cat.GetNewsLink(ctx, "https://t.me/bot?start=a")
	if err != nil || !ok || row.Tries != 0 || row.Detail != "" {
		t.Fatalf("link %+v ok %v err %v", row, ok, err)
	}
	got, err := cat.NewsChannels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range got {
		if ch.ID == src.ID && ch.Error != "" {
			t.Fatalf("flood was stored on the channel: %s", ch.Error)
		}
	}
}

type failOpen struct{ calls int }

func (f *failOpen) Open(context.Context, string) ([]tguser.Post, error) {
	f.calls++
	return nil, ErrSlowDown
}
