package main

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSetupFilesFollowTheChosenFolder(t *testing.T) {
	root := t.TempDir()
	from, to := filepath.Join(root, "old", "data"), filepath.Join(root, "new", "data")
	write(t, filepath.Join(from, "users", "1", "session.json"), "signed in")
	write(t, filepath.Join(from, "sessions", "bot"), "bot")
	write(t, filepath.Join(from, "catalog.db"), "not moved")
	if err := moveSetupFiles(from, to, nil); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(to, "users", "1", "session.json")); err != nil || string(b) != "signed in" {
		t.Fatalf("sign-in not moved: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(to, "sessions", "bot")); err != nil {
		t.Errorf("bot session not moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(from, "users")); !os.IsNotExist(err) {
		t.Error("the old sign-in is still there")
	}
	if _, err := os.Stat(filepath.Join(to, "catalog.db")); !os.IsNotExist(err) {
		t.Error("something other than the sign-in was moved")
	}
}

func TestSetupFilesStayWhenTheFolderDoesNotChange(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	write(t, filepath.Join(dir, "users", "a"), "x")
	if err := moveSetupFiles(dir, dir, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "users", "a")); err != nil {
		t.Fatal(err)
	}
}

func TestSetupFilesNeverReplaceNewerOnes(t *testing.T) {
	root := t.TempDir()
	from, to := filepath.Join(root, "a"), filepath.Join(root, "b")
	write(t, filepath.Join(from, "users", "f"), "old")
	write(t, filepath.Join(to, "users", "f"), "new")
	if err := moveSetupFiles(from, to, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(to, "users", "f")); string(b) != "new" {
		t.Fatalf("overwrote: %q", b)
	}
}

func TestCopyTreeKeepsEveryFile(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "s", "a", "b.txt"), "deep")
	if err := copyTree(filepath.Join(root, "s"), filepath.Join(root, "d")); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(root, "d", "a", "b.txt")); err != nil || string(b) != "deep" {
		t.Fatalf("%q %v", b, err)
	}
}
