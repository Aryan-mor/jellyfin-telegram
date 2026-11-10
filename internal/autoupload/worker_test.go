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

func addLocal(t *testing.T, cat *catalog.Catalog, id int, path string, size int64, name string) catalog.Upload {
	t.Helper()
	row, err := cat.UpsertUpload(context.Background(), catalog.Upload{
		Kind: catalog.KindLocal, RemoteID: id, Path: path, Size: size,
		FileName: name, Caption: name, State: catalog.UploadQueued,
	})
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func TestTwoSessionsTakeTwoFiles(t *testing.T) {
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
	addLocal(t, cat, 1, path, int64(len(body)), "Sintel (2010) [tmdbid-45745] 1080p.mkv")
	addLocal(t, cat, 2, path, int64(len(body)), "Other (2011) [tmdbid-2] 1080p.mkv")
	sessions := &memSessions{views: []tguser.View{
		{UserID: 1, Admin: true, Connected: true, MaxBytes: tguser.MaxBytes(false), Pace: tguser.Pace{DailyGB: 50}},
		{UserID: 2, Admin: true, Connected: true, MaxBytes: tguser.MaxBytes(true), Pace: tguser.Pace{DailyGB: 50}},
	}}
	w := &Worker{Cat: cat, Sessions: sessions, Verify: memVerify{body: body}, Now: func() time.Time { return time.Date(2026, 11, 3, 3, 0, 0, 0, time.Local) }}
	if err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		sessions.mu.Lock()
		defer sessions.mu.Unlock()
		return len(sessions.sent) >= 2
	})
}

func TestProbedWaitsForTheProbe(t *testing.T) {
	ctx := context.Background()
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	row, err := cat.UpsertUpload(ctx, catalog.Upload{
		Kind: catalog.KindLocal, RemoteID: 1, OwnerID: 2, Path: "x", Size: 3,
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
	w := &Worker{Cat: cat}
	if err := w.markProbed(ctx, row); err != nil {
		t.Fatal(err)
	}
	got, _, err := cat.FindUpload(ctx, catalog.KindLocal, 1)
	if err != nil || got.State != catalog.UploadIndexed {
		t.Fatalf("probed early: %+v %v", got, err)
	}
	if err := cat.MarkProbed(ctx, 8, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := w.markProbed(ctx, row); err != nil {
		t.Fatal(err)
	}
	got, _, _ = cat.FindUpload(ctx, catalog.KindLocal, 1)
	if got.State != catalog.UploadProbed {
		t.Fatalf("state %s", got.State)
	}
}

func TestLocalWorkerKeepsTheFile(t *testing.T) {
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

	local := &Worker{Cat: cat, Sessions: sessions, Verify: memVerify{body: body}, Now: now}
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
