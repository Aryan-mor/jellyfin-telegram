package autoupload

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// channelFake answers like tgpool.Pool: the last argument is the size of the whole file.
type channelFake struct{ body []byte }

func (c channelFake) Messages(context.Context, []int) ([]media.Message, error) {
	return []media.Message{{ID: 1, Exists: true, File: &media.File{MessageID: 1, DocID: 9, Size: int64(len(c.body))}}}, nil
}

func (c channelFake) FetchBlock(_ context.Context, _ int, _, block, size int64) ([]byte, error) {
	off := block * media.BlockSize
	want := min(media.BlockSize, size-off)
	if want <= 0 {
		return nil, fmt.Errorf("block %d is past the end of a %d byte file", block, size)
	}
	return c.body[off : off+want], nil
}

func bigFile(t *testing.T) (string, []byte) {
	t.Helper()
	body := bytes.Repeat([]byte("0123456789abcdef"), int(2*media.BlockSize+500)/16)
	path := filepath.Join(t.TempDir(), "big.mkv")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, body
}

func TestSameBlocksReadsTheLastBlockOfALargeFile(t *testing.T) {
	path, body := bigFile(t)
	if err := sameBlocks(context.Background(), channelFake{body}, path, 1, 9, int64(len(body))); err != nil {
		t.Fatalf("a matching 2 MiB file failed: %v", err)
	}
}

func TestDifferentBytesAreAMismatchAndKeepTheFile(t *testing.T) {
	ctx := context.Background()
	path, body := bigFile(t)
	other := append([]byte(nil), body...)
	other[len(other)-1] ^= 0xff
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	row, err := cat.UpsertUpload(ctx, catalog.Upload{Kind: catalog.KindLocal, RemoteID: 1, OwnerID: 2, Path: path,
		Size: int64(len(body)), FileName: "A (2010).mkv", State: catalog.UploadSent})
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.SetUpload(ctx, row.ID, catalog.UploadSent, 1, 0, ""); err != nil {
		t.Fatal(err)
	}
	row.MessageID = 1
	w := &Worker{Cat: cat, Verify: channelFake{other}}
	if err := w.verify(ctx, row); err != nil {
		t.Fatal(err)
	}
	got, _, _ := cat.FindUpload(ctx, catalog.KindLocal, 1)
	if got.State != catalog.UploadMismatch {
		t.Fatalf("state %s", got.State)
	}
	// A mismatch must never be picked up again, found in the catalog as "already uploaded", or cleaned.
	if err := cat.Put(ctx, media.File{MessageID: 1, Size: int64(len(body)), FileName: "A (2010).mkv", RelPath: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := w.advance(ctx, got); err != nil {
		t.Fatal(err)
	}
	w.dispatch(ctx, nil)
	again, _, _ := cat.FindUpload(ctx, catalog.KindLocal, 1)
	if again.State != catalog.UploadMismatch {
		t.Fatalf("a mismatch moved on to %s", again.State)
	}
}

func TestAnUploadOutlivesThePassThatStartedIt(t *testing.T) {
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.mkv")
	if err := os.WriteFile(path, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	addLocal(t, cat, 1, path, 5, "A (2010).mkv")
	sessions := &memSessions{hold: make(chan struct{}), views: []tguser.View{
		{UserID: 1, Username: "u", Admin: true, Connected: true, MaxBytes: tguser.MaxBytes(false), Pace: tguser.Pace{DailyGB: 50}},
	}}
	forever, stop := context.WithCancel(context.Background())
	defer stop()
	w := &Worker{Cat: cat, Sessions: sessions, Ctx: forever}

	pass, endPass := context.WithCancel(context.Background())
	if err := w.RunOnce(pass); err != nil {
		t.Fatal(err)
	}
	// The pass is over while the file is still going up, as in the service, where a pass is short.
	endPass()
	close(sessions.hold)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		row, _, _ := cat.FindUpload(context.Background(), catalog.KindLocal, 1)
		if row.State == catalog.UploadSent {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	row, _, _ := cat.FindUpload(context.Background(), catalog.KindLocal, 1)
	t.Fatalf("the upload died with its pass, state %s", row.State)
}

func TestAWaitingFileSaysWhatItWaitsFor(t *testing.T) {
	ctx := context.Background()
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	addLocal(t, cat, 1, "x", 5, "A (2010).mkv")
	// This account is only allowed to send between 01:00 and 02:00, and it is 12:00.
	sessions := &memSessions{views: []tguser.View{
		{UserID: 1, Username: "u", Admin: true, Connected: true, MaxBytes: tguser.MaxBytes(false), Pace: tguser.Pace{Hours: "01-02"}},
	}}
	noon := func() time.Time { return time.Date(2026, 11, 3, 12, 0, 0, 0, time.Local) }
	w := &Worker{Cat: cat, Sessions: sessions, Now: noon}
	if err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	row, _, _ := cat.FindUpload(ctx, catalog.KindLocal, 1)
	if row.State != catalog.UploadQueued || !strings.Contains(row.Error, "@u rests for") {
		t.Fatalf("state %s note %q", row.State, row.Error)
	}
}
