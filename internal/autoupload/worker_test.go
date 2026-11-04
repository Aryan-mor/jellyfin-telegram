package autoupload

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

type memLib struct {
	items []Item
	mu    sync.Mutex
	scans int
	gone  bool
}

func (m *memLib) List(context.Context) ([]Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Item(nil), m.items...), nil
}
func (m *memLib) Scan(context.Context) error                       { m.scans++; return nil }
func (m *memLib) DownloadID(context.Context, Item) (string, error) { return "", nil }
func (m *memLib) Unmonitor(context.Context, Item) error            { return nil }
func (m *memLib) DeleteFile(context.Context, Item) error           { m.gone = true; return nil }
func (m *memLib) DeleteTorrent(context.Context, string) error      { return nil }
func (m *memLib) Pause(context.Context) error                      { return nil }
func (m *memLib) Resume(context.Context) error                     { return nil }

type memSessions struct {
	views []tguser.View
	mu    sync.Mutex
	sent  []int64
	// hold, when set, keeps an upload running until it is closed.
	hold chan struct{}
}

func (m *memSessions) Healthy() []tguser.View { return m.views }
func (m *memSessions) LargestCap() int64 {
	var max int64
	for _, v := range m.views {
		if v.MaxBytes > max {
			max = v.MaxBytes
		}
	}
	return max
}
func (m *memSessions) Gate(id int64, size int64, now time.Time) tguser.Gate {
	for _, v := range m.views {
		if v.UserID == id {
			return tguser.Allow(v.Pace, now, size, 0, 0, time.Time{}, v.MaxBytes)
		}
	}
	return tguser.Gate{Reason: "missing"}
}
func (m *memSessions) Upload(ctx context.Context, userID int64, path, name, caption string, _ func(int64, int64)) (int, error) {
	m.mu.Lock()
	m.sent = append(m.sent, userID)
	m.mu.Unlock()
	if m.hold != nil {
		<-m.hold
	}
	return int(userID), nil
}
func (m *memSessions) FindMessage(context.Context, string, int64) (int, bool, error) {
	return 0, false, nil
}
func (m *memSessions) MarkSent(int64, int64, time.Time) {}

type memVerify struct{ body []byte }

func (m memVerify) Messages(context.Context, []int) ([]media.Message, error) {
	return []media.Message{{ID: 1, Exists: true, File: &media.File{MessageID: 1, DocID: 9, Size: int64(len(m.body))}}}, nil
}
func (m memVerify) FetchBlock(context.Context, int, int64, int64, int64) ([]byte, error) {
	return m.body, nil
}

func TestTooBigAndTwoSessions(t *testing.T) {
	ctx := context.Background()
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	dir := t.TempDir()
	body := []byte("hello video")
	path := filepath.Join(dir, "a.mkv")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big.mkv")
	if err := os.WriteFile(big, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	lib := &memLib{items: []Item{
		{Kind: "movie", RemoteID: 1, OwnerID: 10, Path: path, Size: int64(len(body)), FileName: "Sintel (2010) [tmdbid-45745] 1080p.mkv", Caption: "Sintel (2010)"},
		{Kind: "movie", RemoteID: 2, OwnerID: 11, Path: path, Size: int64(len(body)), FileName: "Other (2011) [tmdbid-2] 1080p.mkv", Caption: "Other (2011)"},
		{Kind: "movie", RemoteID: 3, OwnerID: 12, Path: big, Size: tguser.MaxBytes(true) + 1, FileName: "Huge (2020).mkv", Caption: "Huge (2020)"},
	}}
	sessions := &memSessions{views: []tguser.View{
		{UserID: 1, Admin: true, Connected: true, MaxBytes: tguser.MaxBytes(false), Pace: tguser.Pace{DailyGB: 50}},
		{UserID: 2, Admin: true, Connected: true, MaxBytes: tguser.MaxBytes(true), Pace: tguser.Pace{DailyGB: 50}},
	}}
	w := &Worker{Cat: cat, Lib: lib, Sessions: sessions, Verify: memVerify{body: body}, Now: func() time.Time { return time.Date(2026, 11, 3, 3, 0, 0, 0, time.Local) }}
	if err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sessions.mu.Lock()
		n := len(sessions.sent)
		sessions.mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	sessions.mu.Lock()
	if len(sessions.sent) < 2 {
		t.Fatalf("uploads started: %v", sessions.sent)
	}
	sessions.mu.Unlock()
	rows, err := cat.ListUploads(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var huge string
	for _, row := range rows {
		if row.FileName == "Huge (2020).mkv" {
			huge = row.State
		}
	}
	if huge != catalog.UploadTooBig {
		t.Fatalf("huge state %s", huge)
	}
}

func TestCleanupWaitsForProbe(t *testing.T) {
	ctx := context.Background()
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	row, err := cat.UpsertUpload(ctx, catalog.Upload{
		Kind: "movie", RemoteID: 1, OwnerID: 2, Path: "x", Size: 3,
		FileName: "Sintel (2010).mkv", Caption: "Sintel (2010)", State: catalog.UploadIndexed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.Put(ctx, media.File{MessageID: 8, Size: 3, FileName: "Sintel (2010).mkv", RelPath: "a", Date: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	if err := cat.SetUpload(ctx, row.ID, catalog.UploadIndexed, 8, 0, ""); err != nil {
		t.Fatal(err)
	}
	row.MessageID = 8
	lib := &memLib{}
	w := &Worker{Cat: cat, Lib: lib}
	if err := w.markProbed(ctx, row); err != nil {
		t.Fatal(err)
	}
	got, _, err := cat.FindUpload(ctx, "movie", 1)
	if err != nil || got.State != catalog.UploadIndexed {
		t.Fatalf("probed early: %+v %v", got, err)
	}
	if err := cat.MarkProbed(ctx, 8, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := w.markProbed(ctx, row); err != nil {
		t.Fatal(err)
	}
	got, _, _ = cat.FindUpload(ctx, "movie", 1)
	if err := w.cleanup(ctx, got); err != nil {
		t.Fatal(err)
	}
	got, _, _ = cat.FindUpload(ctx, "movie", 1)
	if got.State != catalog.UploadCleaned || !lib.gone || lib.scans != 1 {
		t.Fatalf("cleaned %+v scans %d gone %v", got, lib.scans, lib.gone)
	}
}

func TestLocalWorkerKeepsTheFileAndTheOtherWorkerIgnoresIt(t *testing.T) {
	ctx := context.Background()
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	body := []byte("hello video")
	path := filepath.Join(t.TempDir(), "a.mkv")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	row, err := cat.UpsertUpload(ctx, catalog.Upload{
		Kind: catalog.KindLocal, RemoteID: 5, Path: path, Size: int64(len(body)),
		FileName: "Sintel (2010).mkv", Caption: "Sintel (2010)", State: catalog.UploadQueued,
	})
	if err != nil {
		t.Fatal(err)
	}
	sessions := &memSessions{views: []tguser.View{{UserID: 1, Admin: true, Connected: true, MaxBytes: tguser.MaxBytes(false), Pace: tguser.Pace{DailyGB: 50}}}}
	now := func() time.Time { return time.Date(2026, 11, 3, 3, 0, 0, 0, time.Local) }

	// The Radarr and Sonarr worker must not send a file that a person chose.
	lib := &memLib{}
	arr := &Worker{Cat: cat, Lib: lib, Sessions: sessions, Verify: memVerify{body: body}, Now: now}
	if err := arr.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	sessions.mu.Lock()
	n := len(sessions.sent)
	sessions.mu.Unlock()
	if n != 0 {
		t.Fatalf("the other worker uploaded %d local files", n)
	}

	local := &Worker{Cat: cat, Sessions: sessions, Verify: memVerify{body: body}, Local: true, Now: now}
	if err := local.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		got, _, _ := cat.FindUpload(ctx, catalog.KindLocal, 5)
		return got.State == catalog.UploadSent
	})
	// Walk the row forward as the indexer and the prober would.
	if err := cat.Put(ctx, media.File{MessageID: 1, Size: int64(len(body)), FileName: "Sintel (2010).mkv", RelPath: "a", Date: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := local.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := cat.MarkProbed(ctx, 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := local.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	got, _, _ := cat.FindUpload(ctx, catalog.KindLocal, 5)
	if got.State != catalog.UploadProbed || got.ID != row.ID {
		t.Fatalf("state %s", got.State)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("the local file was deleted without being asked")
	}
	if lib.gone || lib.scans != 0 {
		t.Fatal("Radarr or Sonarr was touched for a local file")
	}
	// Even the other worker leaves a probed local row alone.
	if err := arr.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if lib.gone {
		t.Fatal("the other worker cleaned up a local file")
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}
