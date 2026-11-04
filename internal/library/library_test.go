package library

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteStrmReportsChanges(t *testing.T) {
	root := t.TempDir()
	l := New(root, "http://gw/", "t", 5)
	changed, err := l.WriteStrm("Movies/A/A.strm", 1)
	if err != nil || !changed {
		t.Fatalf("first write: %v, %v", changed, err)
	}
	changed, err = l.WriteStrm("Movies/A/A.strm", 1)
	if err != nil || changed {
		t.Fatalf("identical write: %v, %v", changed, err)
	}
	b, err := os.ReadFile(filepath.Join(root, "Movies", "A", "A.strm"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "http://gw/s/t/5/1\n" {
		t.Errorf("content = %q", b)
	}
	entries, err := os.ReadDir(filepath.Join(root, "Movies", "A"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("temp files left behind: %v", entries)
	}
}

func TestNewAddressKeepsTheModificationTime(t *testing.T) {
	root := t.TempDir()
	rel := "Movies/A/A.strm"
	if _, err := New(root, "http://old", "t", 5).WriteStrm(rel, 1); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(root, "Movies", "A", "A.strm")
	stamp := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(full, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	changed, err := New(root, "http://new", "t2", 5).WriteStrm(rel, 1)
	if err != nil || !changed {
		t.Fatalf("changed %v, %v", changed, err)
	}
	b, err := os.ReadFile(full)
	if err != nil || string(b) != "http://new/s/t2/5/1\n" {
		t.Fatalf("content %q, %v", b, err)
	}
	st, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	if !st.ModTime().Equal(stamp) {
		t.Errorf("modification time moved to %v, which makes Jellyfin delete extracted subtitles", st.ModTime())
	}
}

func TestPathsCannotEscapeTheLibrary(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "keep.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)
	l := New(root, "http://gw", "t", 5)
	if _, err := l.WriteStrm("../evil.strm", 1); err == nil {
		t.Error("write escaped the library")
	}
	if err := l.Remove("../keep.txt"); err == nil {
		t.Error("remove escaped the library")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Error("file outside the library was removed")
	}
}

func TestRemoveKeepsFoldersWithOtherVersions(t *testing.T) {
	root := t.TempDir()
	l := New(root, "http://gw", "t", 5)
	if err := l.Prepare(); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"Movies/A/A - 720p.strm", "Movies/A/A - 1080p.strm"} {
		if _, err := l.WriteStrm(rel, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.WriteUnmatchedSidecars("Movies/A/A - 720p.strm", "A", "", []byte("img")); err != nil {
		t.Fatal(err)
	}
	if err := l.Remove("Movies/A/A - 720p.strm"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "Movies", "A", "movie.nfo")); err != nil {
		t.Error("sidecar removed while a version remains")
	}
	if err := l.Remove("Movies/A/A - 1080p.strm"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "Movies", "A")); !os.IsNotExist(err) {
		t.Error("empty folder and sidecars remain")
	}
	if _, err := os.Stat(filepath.Join(root, "Movies")); err != nil {
		t.Error("Movies folder removed")
	}
	if err := l.Remove("Movies/A/A - 1080p.strm"); err != nil {
		t.Errorf("removing twice should be harmless: %v", err)
	}
}
