package stack

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPasswordHash(t *testing.T) {
	got, err := PasswordHash("adminadmin")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "@ByteArray(") || !strings.HasSuffix(got, ")") || !strings.Contains(got, ":") {
		t.Fatalf("%s", got)
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(got, "@ByteArray("), ")")
	salt, key, ok := strings.Cut(inner, ":")
	if !ok {
		t.Fatal(got)
	}
	sb, err := base64.StdEncoding.DecodeString(salt)
	if err != nil || len(sb) != 16 {
		t.Fatalf("salt %d %v", len(sb), err)
	}
	kb, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(kb) != 64 {
		t.Fatalf("key %d %v", len(kb), err)
	}
}

func TestQbitConfigKeepsPassword(t *testing.T) {
	dir := t.TempDir()
	if err := WriteQbitConfig(dir, "admin", "secret"); err != nil {
		t.Fatal(err)
	}
	if err := WriteQbitConfig(dir, "admin", "other"); err != nil {
		t.Fatal(err)
	}
	raw := mustRead(t, filepath.Join(dir, "qBittorrent", "qBittorrent.conf"))
	if !strings.Contains(raw, `Session\MaxRatioAction=0`) || strings.Contains(raw, "MaxRatioAction=1") {
		t.Fatalf("ratio action must stop, not remove:\n%s", raw)
	}
	if !strings.Contains(raw, "WebUI\\Username=admin") {
		t.Fatal(raw)
	}
}

func TestImagesArePinnedVersions(t *testing.T) {
	for _, image := range Images() {
		if strings.HasSuffix(image, ":latest") || !strings.Contains(image, ":") {
			t.Fatalf("unpinned image %s", image)
		}
	}
	if !strings.Contains(ImageSonarr, "sonarr:4") {
		t.Fatal(ImageSonarr)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
