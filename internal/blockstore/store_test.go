package blockstore

import (
	"bytes"
	"testing"
)

func block(b byte, n int) []byte { return bytes.Repeat([]byte{b}, n) }

func TestPutGet(t *testing.T) {
	s, err := Open(t.TempDir(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(1, 0); ok {
		t.Fatal("empty store returned a block")
	}
	if err := s.Put(1, 0, block('a', 10)); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Get(1, 0)
	if !ok || !bytes.Equal(got, block('a', 10)) {
		t.Fatalf("got %q, %v", got, ok)
	}
	if !s.Has(1, 0) || s.Has(1, 1) {
		t.Fatal("Has is wrong")
	}
}

func TestEvictsLeastRecentlyUsed(t *testing.T) {
	s, err := Open(t.TempDir(), 25)
	if err != nil {
		t.Fatal(err)
	}
	for i := range int64(2) {
		if err := s.Put(1, i, block('a', 10)); err != nil {
			t.Fatal(err)
		}
	}
	s.Get(1, 0) // block 1 becomes the oldest
	if err := s.Put(1, 2, block('a', 10)); err != nil {
		t.Fatal(err)
	}
	if s.Has(1, 1) {
		t.Error("oldest block was kept")
	}
	if !s.Has(1, 0) || !s.Has(1, 2) {
		t.Error("recent blocks were evicted")
	}
	if s.Used() != 20 {
		t.Errorf("used = %d, want 20", s.Used())
	}
}

func TestPinnedBlocksSurviveEvictionAndRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 15)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(7, 0, block('p', 10)); err != nil {
		t.Fatal(err)
	}
	if err := s.Pin(7, 0); err != nil {
		t.Fatal(err)
	}
	if s.Used() != 0 {
		t.Errorf("pinned block counts toward the limit: used = %d", s.Used())
	}
	for i := int64(1); i < 5; i++ {
		if err := s.Put(7, i, block('x', 10)); err != nil {
			t.Fatal(err)
		}
	}
	if !s.Has(7, 0) {
		t.Fatal("pinned block was evicted")
	}

	reopened, err := Open(dir, 15)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reopened.Get(7, 0)
	if !ok || !bytes.Equal(got, block('p', 10)) {
		t.Fatal("pinned block lost across restart")
	}
	if reopened.Used() > 15 {
		t.Errorf("limit not enforced after restart: used = %d", reopened.Used())
	}
	// Pinning again and pinning a missing block are both harmless.
	if err := reopened.Pin(7, 0); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Pin(99, 99); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveDoc(t *testing.T) {
	s, err := Open(t.TempDir(), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct{ doc, block int64 }{{1, 0}, {1, 1}, {2, 0}} {
		if err := s.Put(p.doc, p.block, block('a', 10)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Pin(1, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveDoc(1); err != nil {
		t.Fatal(err)
	}
	if s.Has(1, 0) || s.Has(1, 1) {
		t.Error("document blocks remain")
	}
	if !s.Has(2, 0) {
		t.Error("other document was removed")
	}
	if s.Used() != 10 {
		t.Errorf("used = %d, want 10", s.Used())
	}
}

func TestNegativeDocumentIDs(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(-55, 3, block('n', 5)); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.Has(-55, 3) {
		t.Fatal("negative document id not reloaded")
	}
}
