package mirror

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
	"github.com/Aryan-mor/jellyfin-telegram/internal/sign"
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

func TestReadyOnlyWaitsForFinishedPosts(t *testing.T) {
	c, err := catalog.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	key, err := sign.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	post := func(status string) string {
		meta := sign.Meta{Kind: "m", TMDB: 603, DocID: 7, Size: 1, Status: status}
		return sign.WithHeader(status, "", "", "The Matrix (1999)\n"+key.Sign(meta)+"\n#tm_m_603")
	}
	ctx := context.Background()
	for _, f := range []media.File{
		{MessageID: 1, ChannelID: 9, DocID: 1, Size: 1, MIME: "video/mp4", FileName: "a.mkv", Caption: post(sign.StatusSubtitles), RelPath: "Movies/a/a.strm", Date: time.Unix(1, 0)},
		{MessageID: 2, ChannelID: 9, DocID: 2, Size: 1, MIME: "video/mp4", FileName: "b.mkv", Caption: post(sign.StatusReady), RelPath: "Movies/b/b.strm", Date: time.Unix(2, 0)},
		// A ready status line over a line that still says subtitles is not finished.
		{MessageID: 3, ChannelID: 9, DocID: 3, Size: 1, MIME: "video/mp4", FileName: "c.mkv", Caption: sign.WithHeader(sign.StatusReady, "", "", "The Matrix (1999)\n"+key.Sign(sign.Meta{Kind: "m", TMDB: 603, DocID: 7, Size: 1, Status: sign.StatusSubtitles})), RelPath: "Movies/c/c.strm", Date: time.Unix(3, 0)},
	} {
		if err := c.Put(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.AddMirror(ctx, 50, 7, "Films", []string{"movie"}); err != nil {
		t.Fatal(err)
	}
	var got []int
	on := true
	s := &Service{Cat: c, Log: slog.New(slog.DiscardHandler), ReadyOnly: func() bool { return on },
		Copy: func(_ context.Context, _ int64, message int, _ int64) (int, error) {
			got = append(got, message)
			return message + 100, nil
		}}
	if err := s.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 2 {
		t.Fatalf("copied %v, want only the finished post", got)
	}
	// Once the others are finished, a later pass sends them.
	for _, id := range []int{1, 3} {
		if err := c.SetCaption(ctx, id, post(sign.StatusReady)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("copied %v", got)
	}
	// With the shared library off, nothing is held back.
	on = false
	if err := c.Put(ctx, media.File{MessageID: 4, ChannelID: 9, DocID: 4, Size: 1, MIME: "video/mp4", FileName: "Old.1990.mkv", RelPath: "Movies/o/o.strm", Date: time.Unix(4, 0)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("copied %v", got)
	}
}

// A finished video goes to the mirror together with its subtitle archive, one after the other.
func TestVideoAndItsArchiveGoTogether(t *testing.T) {
	c, err := catalog.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	key, err := sign.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	post := func(doc int64, subs bool) string {
		meta := sign.Meta{Kind: "e", TMDB: 7, Season: 1, Episode: 1, DocID: doc, Size: 1, Status: sign.StatusReady}
		if subs {
			meta.Subs = "yes"
			meta.ArchiveHash = "AAAAAAAAAAAA"
		}
		return sign.WithHeader(sign.StatusReady, "", "", "Show\n"+key.Sign(meta)+"\n#tm_tv_7_1_1")
	}
	ctx := context.Background()
	for _, f := range []media.File{
		{MessageID: 10, ChannelID: 9, DocID: 1, Size: 1, MIME: "video/mp4", FileName: "a.mkv", Caption: post(1, true), RelPath: "Shows/a/S01E01.strm", Date: time.Unix(1, 0)},
		{MessageID: 20, ChannelID: 9, DocID: 2, Size: 1, MIME: "video/mp4", FileName: "b.mkv", Caption: post(2, false), RelPath: "Shows/b/S01E01.strm", Date: time.Unix(2, 0)},
		{MessageID: 30, ChannelID: 9, DocID: 3, Size: 1, MIME: "video/mp4", FileName: "c.mkv", Caption: post(3, true), RelPath: "Shows/c/S01E01.strm", Date: time.Unix(3, 0)},
	} {
		if err := c.Put(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	archive := func(id, reply int, doc int64) {
		if err := c.PutAttachment(ctx, media.Attachment{MessageID: id, DocID: doc, Size: 5, FileName: "jftg-subs-1-1.zip", ReplyTo: reply}); err != nil {
			t.Fatal(err)
		}
	}
	archive(11, 10, 111) // the archive of message 10 is known
	// message 30 names an archive that has not been seen yet
	if _, err := c.AddMirror(ctx, 50, 7, "Shows", []string{"series"}); err != nil {
		t.Fatal(err)
	}
	var got []int
	s := &Service{Cat: c, Log: slog.New(slog.DiscardHandler), ReadyOnly: func() bool { return true },
		Copy: func(_ context.Context, _ int64, message int, _ int64) (int, error) {
			got = append(got, message)
			return message + 1000, nil
		}}
	if err := s.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	// 10 with its archive 11, then 20 which has no archive. 30 waits for its archive.
	want := []int{10, 11, 20}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("copied %v, want %v", got, want)
	}
	// The archive of 30 turns up: the video and then the archive go.
	archive(31, 30, 333)
	if err := s.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || got[3] != 30 || got[4] != 31 {
		t.Fatalf("copied %v", got)
	}
	// An archive that appears after its video was copied is caught up.
	archive(21, 20, 222)
	if err := s.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 || got[5] != 21 {
		t.Fatalf("copied %v", got)
	}
	// Nothing is sent twice.
	if err := s.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 {
		t.Fatalf("copied again: %v", got)
	}
}
