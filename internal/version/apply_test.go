package version

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPickAsset(t *testing.T) {
	assets := []Asset{
		{Name: "jellyfin-telegram-linux-arm64.tar.gz", URL: "https://github.com/" + Repo + "/releases/download/v1.0.3/jellyfin-telegram-linux-arm64.tar.gz"},
		{Name: "jellyfin-telegram-windows-amd64.zip", URL: "https://github.com/" + Repo + "/releases/download/v1.0.3/jellyfin-telegram-windows-amd64.zip"},
		{Name: "install.ps1", URL: "https://github.com/" + Repo + "/releases/download/v1.0.3/install.ps1"},
	}
	got, ok := PickAsset(assets, "windows", "amd64")
	if !ok || got.Name != "jellyfin-telegram-windows-amd64.zip" {
		t.Fatalf("windows = %+v ok=%v", got, ok)
	}
	if _, ok := PickAsset(assets, "darwin", "arm64"); ok {
		t.Fatal("darwin was offered a build")
	}
}

func TestAllowedDownload(t *testing.T) {
	ok := "https://github.com/" + Repo + "/releases/download/v1.0.3/jellyfin-telegram-windows-amd64.zip"
	if err := allowedDownload(ok); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"http://github.com/" + Repo + "/releases/download/v1.0.3/jellyfin-telegram-windows-amd64.zip",
		"https://example.com/jellyfin-telegram-windows-amd64.zip",
		"https://github.com/other/repo/releases/download/v1.0.3/jellyfin-telegram-windows-amd64.zip",
	} {
		if err := allowedDownload(bad); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestInstallArchiveReplacesBinary(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "jellyfin-telegram.exe")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "jellyfin-telegram-windows-amd64.zip")
	writeZip(t, archive, "jellyfin-telegram.exe", "new")
	if err := installArchive(exe, archive, "jellyfin-telegram-windows-amd64.zip"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("binary = %q", got)
	}
	prev, err := os.ReadFile(exe + ".previous")
	if err != nil || string(prev) != "old" {
		t.Fatalf("previous = %q err=%v", prev, err)
	}
}

func TestInstallArchiveRejectsNestedName(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "jellyfin-telegram.exe")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "jellyfin-telegram-windows-amd64.zip")
	writeZip(t, archive, "dir/jellyfin-telegram.exe", "new")
	if err := installArchive(exe, archive, "jellyfin-telegram-windows-amd64.zip"); err == nil {
		t.Fatal("nested file was installed")
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "old" {
		t.Fatalf("binary changed to %q", got)
	}
}

func TestInstallArchiveTarGz(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "jellyfin-telegram")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "jellyfin-telegram-linux-amd64.tar.gz")
	writeTarGz(t, archive, "jellyfin-telegram", "linux")
	if err := installArchive(exe, archive, "jellyfin-telegram-linux-amd64.tar.gz"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(exe)
	if err != nil || string(got) != "linux" {
		t.Fatalf("binary = %q err=%v", got, err)
	}
}

func TestInstallRefusesCurrentVersion(t *testing.T) {
	oldVersion, oldFetch := Version, Fetch
	t.Cleanup(func() {
		Version = oldVersion
		Fetch = oldFetch
	})
	Version = "1.2.3"
	Fetch = func(context.Context) (Release, error) {
		return Release{Tag: "v1.2.3"}, nil
	}
	if err := Install(context.Background()); err == nil {
		t.Fatal("the current version was installed again")
	}
}

func writeZip(t *testing.T, path, name, body string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	entry, err := w.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeTarGz(t *testing.T, path, name, body string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}
