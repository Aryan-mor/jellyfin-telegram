package fuckjoin

import (
	"path/filepath"
	"testing"
)

func TestDefaultCommitIsPinned(t *testing.T) {
	if len(DefaultCommit) != 40 {
		t.Fatalf("commit %q", DefaultCommit)
	}
	r := New(t.TempDir(), 1, "hash", nil)
	if got := r.storedCommit(); got != DefaultCommit {
		t.Fatalf("stored %s", got)
	}
	if !validPhone("+989121234567") || validPhone("0912") {
		t.Fatal("phone check")
	}
	if !filepath.IsAbs(r.venvPython()) {
		t.Fatal("venv python must be absolute so Windows does not resolve it inside the checkout")
	}
}
