package localmedia

import (
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

func write(t *testing.T, root, rel string, size int) string {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

func TestScanSortsFilmsSeriesAndOthers(t *testing.T) {
	root := t.TempDir()
	skipped := t.TempDir()
	write(t, root, "Sintel (2010)/Sintel (2010).mkv", 10)
	write(t, root, "Fringe (2008)/Season 01/Fringe - S01E02.mkv", 10)
	write(t, root, "Fringe (2008)/Season 01/Fringe - S01E01.mkv", 10)
	write(t, root, "video_2024.mp4", 10)
	write(t, root, "Heat (1995)/Extras/Making of.mkv", 10)
	write(t, root, "Heat (1995)/Heat (1995)-trailer.mkv", 10)
	write(t, root, "Heat (1995)/Heat (1995).mkv", 10)
	write(t, root, "Heat (1995)/poster.jpg", 10)
	write(t, root, "cache/segment.mkv", 10)
	write(t, root, "stream/clip.ts", 10)
	write(t, skipped, "Other (2000).mkv", 10)

	files, folders := Scan(context.Background(), []string{root, filepath.Join(root, "missing")}, []string{skipped})
	var got []string
	for _, f := range files {
		got = append(got, f.Kind+":"+f.Title+":"+f.UploadName)
	}
	want := []string{
		"movie:Heat:Heat (1995).mkv",
		"movie:Sintel:Sintel (2010).mkv",
		"episode:Fringe:Fringe (2008) S01E01.mkv",
		"episode:Fringe:Fringe (2008) S01E02.mkv",
		"other::video_2024.mp4",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("files:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(folders) != 2 || !folders[0].Exists || folders[0].Files != 5 || folders[1].Exists || folders[1].Error == "" {
		t.Fatalf("folders: %+v", folders)
	}
	if ID(files[0].Path) != files[0].ID || ID(files[0].Path) == ID(files[1].Path) {
		t.Fatal("ids are not stable and distinct")
	}
	if files[0].ID >= 1<<53 {
		t.Fatalf("id %d is too large for a browser", files[0].ID)
	}
}

type fakeAccounts struct {
	views []tguser.View
}

func (f fakeAccounts) Healthy() []tguser.View { return f.views }
func (f fakeAccounts) LargestCap() int64 {
	var max int64
	for _, v := range f.views {
		if v.MaxBytes > max {
			max = v.MaxBytes
		}
	}
	return max
}

func newService(t *testing.T, root string, accounts Accounts) (*Service, *catalog.Catalog) {
	t.Helper()
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cat.Close() })
	s := New([]string{root})
	s.Cat = cat
	s.Accounts = accounts
	s.EnvPath = filepath.Join(t.TempDir(), ".env")
	s.scan(context.Background())
	return s, cat
}

func healthy() fakeAccounts {
	return fakeAccounts{views: []tguser.View{{UserID: 7, Username: "me", Admin: true, Connected: true, MaxBytes: 2000}}}
}

func find(t *testing.T, v View, rel string) FileView {
	t.Helper()
	for _, c := range v.Categories {
		for _, g := range c.Groups {
			for _, f := range g.Files {
				if f.Rel == rel {
					return f
				}
			}
		}
	}
	t.Fatalf("%s is not listed", rel)
	return FileView{}
}

func TestNothingIsQueuedByScanning(t *testing.T) {
	root := t.TempDir()
	write(t, root, "A (2001)/A (2001).mkv", 10)
	s, cat := newService(t, root, healthy())
	rows, err := cat.ListUploads(context.Background())
	if err != nil || len(rows) != 0 {
		t.Fatalf("rows %v err %v", rows, err)
	}
	v, err := s.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f := find(t, v, "A (2001)/A (2001).mkv"); f.Status != StatusLocal {
		t.Fatalf("status %s", f.Status)
	}
	if v.Totals[StatusLocal].Count != 1 || v.Totals[StatusSynced].Count != 0 {
		t.Fatalf("totals %+v", v.Totals)
	}
}

func TestQueueNeedsAnAccountAndRespectsTheCap(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	write(t, root, "Small (2001)/Small (2001).mkv", 10)
	write(t, root, "Big (2002)/Big (2002).mkv", 5000)

	none, _ := newService(t, root, fakeAccounts{})
	v, _ := none.State(ctx)
	if _, err := none.Queue(ctx, []int64{find(t, v, "Small (2001)/Small (2001).mkv").ID}); err == nil {
		t.Fatal("queued with no account")
	}
	if v.Problem == "" {
		t.Fatal("the tab does not say that no account is signed in")
	}

	s, _ := newService(t, root, healthy())
	v, _ = s.State(ctx)
	small, big := find(t, v, "Small (2001)/Small (2001).mkv"), find(t, v, "Big (2002)/Big (2002).mkv")
	res, err := s.Queue(ctx, []int64{small.ID, big.ID, 12345})
	if err != nil {
		t.Fatal(err)
	}
	if res.Done != 2 || res.Skipped != 1 {
		t.Fatalf("result %+v", res)
	}
	v, _ = s.State(ctx)
	if f := find(t, v, small.Rel); f.Status != StatusWaiting {
		t.Fatalf("small is %s", f.Status)
	}
	if f := find(t, v, big.Rel); f.Status != StatusProblem || !strings.Contains(f.Note, "larger") {
		t.Fatalf("big is %s %q", f.Status, f.Note)
	}
	// A second press changes nothing.
	if res, _ := s.Queue(ctx, []int64{small.ID}); res.Done != 0 || res.Skipped != 1 {
		t.Fatalf("again %+v", res)
	}
	// Taking it back out makes it local again.
	if res, _ := s.Unqueue(ctx, []int64{small.ID, big.ID}); res.Done != 2 {
		t.Fatalf("unqueue %+v", res)
	}
	v, _ = s.State(ctx)
	if f := find(t, v, small.Rel); f.Status != StatusLocal {
		t.Fatalf("after unqueue %s", f.Status)
	}
}

// synced queues a file and walks its row to the probed state, as the worker would.
func synced(t *testing.T, s *Service, cat *catalog.Catalog, f FileView, message int, channelSize int64) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Queue(ctx, []int64{f.ID}); err != nil {
		t.Fatal(err)
	}
	row, ok, err := cat.FindUpload(ctx, catalog.KindLocal, int(f.ID))
	if err != nil || !ok {
		t.Fatalf("row %v %v", ok, err)
	}
	if err := cat.Put(ctx, media.File{MessageID: message, Size: channelSize, FileName: f.UploadName, RelPath: fmt.Sprintf("Movies/m%d/m%d.strm", message, message), Date: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	if err := cat.SetUpload(ctx, row.ID, catalog.UploadProbed, message, 7, ""); err != nil {
		t.Fatal(err)
	}
}

func TestOnlyASyncedFileCanBeDeleted(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	a := write(t, root, "A (2001)/A (2001).mkv", 10)
	b := write(t, root, "B (2002)/B (2002).mkv", 12)
	write(t, root, "C (2003)/C (2003).mkv", 14)
	write(t, root, "C (2003)/C (2003).nfo", 3)
	s, cat := newService(t, root, healthy())
	v, _ := s.State(ctx)
	fa, fb, fc := find(t, v, "A (2001)/A (2001).mkv"), find(t, v, "B (2002)/B (2002).mkv"), find(t, v, "C (2003)/C (2003).mkv")

	// Not sent at all: refused.
	if res, _ := s.Remove(ctx, []int64{fa.ID}); res.Done != 0 || res.Skipped != 1 {
		t.Fatalf("remove local only: %+v", res)
	}
	if _, err := os.Stat(a); err != nil {
		t.Fatal("a file that was never sent was deleted")
	}

	synced(t, s, cat, fa, 11, 10)
	synced(t, s, cat, fb, 12, 999) // the channel holds a different size
	synced(t, s, cat, fc, 13, 14)
	v, _ = s.State(ctx)
	if v.Totals[StatusSynced].Count != 3 {
		t.Fatalf("totals %+v", v.Totals)
	}

	// The channel file of B has another size, so B stays.
	res, err := s.RemoveSynced(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Done != 2 || res.Skipped != 1 || res.Bytes != 24 || len(res.Errors) != 1 {
		t.Fatalf("remove synced: %+v", res)
	}
	if _, err := os.Stat(a); !os.IsNotExist(err) {
		t.Fatal("a was kept")
	}
	if _, err := os.Stat(b); err != nil {
		t.Fatal("b was deleted although the channel file differs")
	}
	// The folder of A held nothing else and goes. The folder of C keeps its .nfo, so it stays.
	if _, err := os.Stat(filepath.Dir(a)); !os.IsNotExist(err) {
		t.Fatal("an empty folder was left behind")
	}
	if _, err := os.Stat(filepath.Join(root, "C (2003)", "C (2003).nfo")); err != nil {
		t.Fatal("a sidecar file was deleted")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal("the media folder itself was deleted")
	}
	v, _ = s.State(ctx)
	if v.Totals[StatusSynced].Count != 1 {
		t.Fatalf("after: %+v", v.Totals)
	}
}

func TestAChangedFileIsNotSyncedAnyMore(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	p := write(t, root, "A (2001)/A (2001).mkv", 10)
	s, cat := newService(t, root, healthy())
	v, _ := s.State(ctx)
	f := find(t, v, "A (2001)/A (2001).mkv")
	synced(t, s, cat, f, 11, 10)

	// Somebody replaces the video with other bytes.
	if err := os.WriteFile(p, make([]byte, 20), 0o644); err != nil {
		t.Fatal(err)
	}
	s.scan(ctx)
	v, _ = s.State(ctx)
	if got := find(t, v, f.Rel); got.Status != StatusLocal {
		t.Fatalf("status %s", got.Status)
	}
	if res, _ := s.Remove(ctx, []int64{f.ID}); res.Done != 0 {
		t.Fatalf("deleted a file that changed: %+v", res)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
}

func TestFoldersAreSavedAndGuarded(t *testing.T) {
	root := t.TempDir()
	library := t.TempDir()
	s := New(nil)
	s.EnvPath = filepath.Join(t.TempDir(), ".env")
	s.Exclude = []string{library}
	for _, bad := range []string{"", filepath.Join(root, "nope"), library, filepath.Join(library, "Movies"), root + "|x"} {
		if err := s.AddFolder(bad); err == nil {
			t.Fatalf("%q was accepted", bad)
		}
	}
	if err := s.AddFolder(root); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFolder(root); err == nil {
		t.Fatal("added twice")
	}
	raw, _ := os.ReadFile(s.EnvPath)
	if !strings.Contains(string(raw), EnvKey+"="+root) {
		t.Fatalf("env: %s", raw)
	}
	if err := s.RemoveFolder(root); err != nil || len(s.Dirs()) != 0 {
		t.Fatalf("remove: %v %v", err, s.Dirs())
	}
}
