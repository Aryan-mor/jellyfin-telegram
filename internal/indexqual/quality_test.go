package indexqual

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanTreatsEverySizeAsAll(t *testing.T) {
	all, err := Clean(nil)
	if err != nil || !all.All() || all.Key() != "all" || all.Env() != "" {
		t.Fatalf("empty %+v %v", all, err)
	}
	five, err := Clean([]string{"4k", "2K", "1080", "720", "480"})
	if err != nil || !five.All() {
		t.Fatalf("five %+v %v", five, err)
	}
	one, err := Clean([]string{"1080", "4K", "1080"})
	if err != nil || one.Key() != "1080,4k" {
		t.Fatalf("one %+v %v", one, err)
	}
	if _, err := Clean([]string{"360"}); err == nil {
		t.Fatal("360 was accepted")
	}
}

func TestBucketMapsReleaseNames(t *testing.T) {
	cases := []struct {
		name, caption, want string
	}{
		{"Film.480p.mkv", "", "480"},
		{"Film.720p.mkv", "", "720"},
		{"Film.1080p.mkv", "", "1080"},
		{"Film.1440p.mkv", "", "2k"},
		{"Film.2K.mkv", "", "2k"},
		{"Film.2160p.mkv", "", "4k"},
		{"Film.mkv", "4K", "4k"},
		{"Film.2020.mkv", "", ""},
		{"Film.1080p.2160p.mkv", "", "1080"},
	}
	for _, c := range cases {
		if got := Bucket(c.name, c.caption); got != c.want {
			t.Errorf("%s / %s = %q, want %q", c.name, c.caption, got, c.want)
		}
	}
	only, _ := Parse("1080,4k")
	if !only.Allows("A.1080p.mkv", "") || !only.Allows("A.2160p.mkv", "") || only.Allows("A.720p.mkv", "") || only.Allows("A.mkv", "") {
		t.Fatal("allows")
	}
	all, err := Parse("")
	if err != nil || !all.Allows("A.mkv", "") {
		t.Fatal("all rejected an unmarked file")
	}
	if !only.Keeps("Song.mp3", "", "audio/mpeg") || !only.Keeps("A.1080p.mkv", "", "video/mp4") || only.Keeps("A.720p.mkv", "", "video/mp4") {
		t.Fatal("keeps")
	}
}

func TestLoadPrefersTheFile(t *testing.T) {
	t.Setenv(Key, "720")
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(Key+"=1080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Load(path).Key(); got != "1080" {
		t.Fatalf("file = %s", got)
	}
	if got := Load(filepath.Join(t.TempDir(), "missing")).Key(); got != "720" {
		t.Fatalf("env = %s", got)
	}
}
