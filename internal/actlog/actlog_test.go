package actlog

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestListPagesPerActor(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "activity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.Note("account", 7, "ada", "search", "#tm_m_1", nil)
	s.Note("account", 7, "ada", "upload", "film.mkv", errors.New("flood"))
	s.Note("account", 7, "ada", "copy", "2 from news", nil)
	s.Note("bot", 9, "librarybot", "send file", "subs.zip", nil)
	s.Note("account", 0, "x", "search", "dropped", nil)

	page, total, err := s.List("account", 7, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(page) != 2 {
		t.Fatalf("page1 total=%d len=%d", total, len(page))
	}
	if page[0].Action != "copy" || !page[0].OK {
		t.Fatalf("newest = %+v", page[0])
	}
	if page[1].Action != "upload" || page[1].OK || page[1].Error == "" {
		t.Fatalf("second = %+v", page[1])
	}
	page, total, err = s.List("account", 7, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(page) != 1 || page[0].Action != "search" {
		t.Fatalf("page2 total=%d items=%+v", total, page)
	}
	bots, total, err := s.List("bot", 9, 1, 40)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(bots) != 1 || bots[0].Detail != "subs.zip" {
		t.Fatalf("bot log = %+v total=%d", bots, total)
	}
}
