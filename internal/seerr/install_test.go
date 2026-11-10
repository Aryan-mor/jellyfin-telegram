package seerr

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Aryan-mor/jellyfin-telegram/internal/managed"
)

// fakeBundle is an archive laid out like the ones the seerr-bundles repository builds.
func fakeBundle(t *testing.T) ([]byte, string) {
	t.Helper()
	node := "node"
	if runtime.GOOS == "windows" {
		node = "node.exe"
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{
		"seerr/" + node:           "binary",
		"seerr/app/dist/index.js": "console.log(1)",
		"seerr/app/package.json":  "{}",
	} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

func TestInstallUnpacksOnceAndDropsOlderVersions(t *testing.T) {
	data, sum := fakeBundle(t)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write(data)
	}))
	defer srv.Close()

	root := t.TempDir()
	old := filepath.Join(root, "bundle", "0.0.1")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	art := managed.Artifact{URL: srv.URL + "/seerr.tar.gz", SHA256: sum}
	b, err := install(context.Background(), root, art, srv.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(b.Node); err != nil {
		t.Errorf("no node: %v", err)
	}
	if _, err := os.Stat(b.entry()); err != nil {
		t.Errorf("no entry: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("an older version stayed: %v", err)
	}
	if _, err := install(context.Background(), root, art, srv.Client(), nil); err != nil || hits != 1 {
		t.Errorf("second install downloaded again: hits=%d err=%v", hits, err)
	}
}

func TestInstallRefusesAChangedFile(t *testing.T) {
	data, _ := fakeBundle(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer srv.Close()
	root := t.TempDir()
	art := managed.Artifact{URL: srv.URL + "/seerr.tar.gz", SHA256: strings.Repeat("0", 64)}
	if _, err := install(context.Background(), root, art, srv.Client(), nil); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "bundle", Version)); !os.IsNotExist(err) {
		t.Error("a refused download left files behind")
	}
}

func TestInstallSaysWhenTheBuildIsNotPublished(t *testing.T) {
	_, err := install(context.Background(), t.TempDir(), managed.Artifact{URL: "http://x/y.tar.gz"}, http.DefaultClient, nil)
	if err == nil || !strings.Contains(err.Error(), "not been published") {
		t.Fatalf("err = %v", err)
	}
}

func TestEveryPlatformHasABundle(t *testing.T) {
	for _, p := range []string{"windows/amd64", "linux/amd64", "linux/arm64"} {
		goos, goarch, _ := strings.Cut(p, "/")
		a, err := bundleFor(goos, goarch)
		if err != nil || !strings.HasSuffix(a.URL, ".tar.gz") || !strings.Contains(a.URL, Version) {
			t.Errorf("%s: %+v, %v", p, a, err)
		}
	}
}

func TestSeerrEnvironmentKeepsItOnLoopbackAndIgnoresTheShell(t *testing.T) {
	t.Setenv("PORT", "9999")
	t.Setenv("NODE_OPTIONS", "--inspect")
	got := map[string]string{}
	for _, kv := range env("/data/config", "secret") {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	for k, want := range map[string]string{
		"HOST": "127.0.0.1", "PORT": "15055", "API_KEY": "secret",
		"CONFIG_DIRECTORY": "/data/config", "NODE_ENV": "production",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	if _, set := got["NODE_OPTIONS"]; set {
		t.Error("the shell's NODE_OPTIONS reached Seerr")
	}
}

func TestARebuiltBundleReplacesTheOldOne(t *testing.T) {
	data, sum := fakeBundle(t)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write(data)
	}))
	defer srv.Close()
	root := t.TempDir()
	// An install made before the marker held a hash, as the first released builds wrote it.
	old := filepath.Join(root, "bundle", Version)
	if err := os.MkdirAll(filepath.Join(old, "seerr", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, ".installed"), []byte(Version+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "seerr", "app", "committag.json"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	art := managed.Artifact{URL: srv.URL + "/seerr.tar.gz", SHA256: sum}
	b, err := install(context.Background(), root, art, srv.Client(), nil)
	if err != nil || hits != 1 {
		t.Fatalf("hits=%d err=%v", hits, err)
	}
	if _, err := os.Stat(filepath.Join(b.App, "committag.json")); !os.IsNotExist(err) {
		t.Error("the stale file of the old bundle stayed")
	}
	if _, err := install(context.Background(), root, art, srv.Client(), nil); err != nil || hits != 1 {
		t.Errorf("the same bundle was downloaded again: hits=%d err=%v", hits, err)
	}
}
