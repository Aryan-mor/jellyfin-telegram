package autoupload

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
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
	row, err := cat.UpsertUpload(ctx, catalog.Upload{Kind: "movie", RemoteID: 1, OwnerID: 2, Path: path,
		Size: int64(len(body)), FileName: "A (2010).mkv", State: catalog.UploadSent})
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.SetUpload(ctx, row.ID, catalog.UploadSent, 1, 0, ""); err != nil {
		t.Fatal(err)
	}
	row.MessageID = 1
	w := &Worker{Cat: cat, Lib: &memLib{}, Verify: channelFake{other}}
	if err := w.verify(ctx, row); err != nil {
		t.Fatal(err)
	}
	got, _, _ := cat.FindUpload(ctx, "movie", 1)
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
	again, _, _ := cat.FindUpload(ctx, "movie", 1)
	if again.State != catalog.UploadMismatch {
		t.Fatalf("a mismatch moved on to %s", again.State)
	}
}

type guardLib struct {
	memLib
	mu      sync.Mutex
	paused  int
	resumed int
}

func (g *guardLib) Pause(context.Context) error  { g.mu.Lock(); g.paused++; g.mu.Unlock(); return nil }
func (g *guardLib) Resume(context.Context) error { g.mu.Lock(); g.resumed++; g.mu.Unlock(); return nil }

func TestGuardResumesOnlyWhatItStopped(t *testing.T) {
	dir := t.TempDir()
	lib := &guardLib{}
	// The folder always has less free space than this, so the guard must stop the torrents.
	w := &Worker{Lib: lib, Dir: dir, MinFree: 1 << 60}
	w.Guard(context.Background())
	if lib.paused != 1 {
		t.Fatalf("paused %d", lib.paused)
	}
	if _, err := os.Stat(filepath.Join(dir, pausedMarker)); err != nil {
		t.Fatal("the pause was not remembered")
	}
	w.MinFree = 1
	w.Guard(context.Background())
	w.Guard(context.Background())
	if lib.resumed != 1 {
		t.Fatalf("resumed %d times, want once", lib.resumed)
	}
}

func TestGuardLeavesTorrentsAloneWhenItNeverStoppedThem(t *testing.T) {
	lib := &guardLib{}
	w := &Worker{Lib: lib, Dir: t.TempDir(), MinFree: 1}
	for i := 0; i < 3; i++ {
		w.Guard(context.Background())
	}
	if lib.resumed != 0 {
		t.Fatalf("resumed %d torrents that this program never stopped", lib.resumed)
	}
}
