package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/managed"
	"github.com/Aryan-mor/jellyfin-telegram/internal/setup"
)

// statusOf reads what the page would receive.
func statusOf(t *testing.T, w *setup.Wizard) (setup.Status, []string) {
	t.Helper()
	var got setup.Status
	w.Notify(func(s setup.Status) { got = s })
	w.Notify(nil)
	return got, w.LogLines()
}

func TestDownloadAndUnpackShareOneBar(t *testing.T) {
	w, _ := setup.New(t.TempDir()+"/.env", nil)
	r := newInstallReporter(w, 55, 70, 85)
	clock := time.Unix(1000, 0)
	r.now = func() time.Time { return clock }

	r.Progress("Seerr", 0, 100<<20)
	clock = clock.Add(2 * time.Second)
	r.Progress("Seerr", 50<<20, 100<<20)
	s, _ := statusOf(t, w)
	if s.Stage != "Downloading Seerr" || s.Percent < 55 || s.Percent > 70 {
		t.Fatalf("download: %+v", s)
	}
	if !strings.Contains(s.Detail, "50.0 MB of 100.0 MB") || !strings.Contains(s.Detail, "/s") {
		t.Errorf("detail %q", s.Detail)
	}

	clock = clock.Add(time.Second)
	r.Progress("Seerr", 100<<20, 100<<20)
	r.Progress(managed.UnpackPrefix+"Seerr", 0, 200)
	clock = clock.Add(time.Second)
	r.Progress(managed.UnpackPrefix+"Seerr", 100, 200)
	s, _ = statusOf(t, w)
	if s.Stage != "Unpacking Seerr" || s.Percent < 70 || s.Percent > 85 {
		t.Fatalf("unpack: %+v", s)
	}
	if !strings.Contains(s.Detail, "50%") {
		t.Errorf("detail %q", s.Detail)
	}
}

func TestTheBarNeverMovesBack(t *testing.T) {
	w, _ := setup.New(t.TempDir()+"/.env", nil)
	r := newInstallReporter(w, 5, 30, 40)
	clock := time.Unix(1000, 0)
	r.now = func() time.Time { return clock }
	r.Progress("Jellyfin", 90, 100)
	high, _ := statusOf(t, w)
	// A second file in the same install starts again from zero.
	clock = clock.Add(time.Second)
	r.Progress("ffmpeg", 1, 100)
	low, _ := statusOf(t, w)
	if low.Percent < high.Percent {
		t.Errorf("bar went back from %d to %d", high.Percent, low.Percent)
	}
}

func TestTheLogTellsWhatHappened(t *testing.T) {
	w, _ := setup.New(t.TempDir()+"/.env", nil)
	r := newInstallReporter(w, 5, 30, 40)
	clock := time.Unix(1000, 0)
	r.now = func() time.Time { return clock }
	for _, done := range []int64{0, 30, 60, 80, 100} {
		clock = clock.Add(time.Second)
		r.Progress("Jellyfin", done, 100)
	}
	_, lines := statusOf(t, w)
	text := strings.Join(lines, "\n")
	for _, want := range []string{"Downloading Jellyfin (100 B)", "Downloading Jellyfin: 25%", "Downloading Jellyfin: 50%", "Downloading Jellyfin: 75%", "Downloading Jellyfin: done"} {
		if !strings.Contains(text, want) {
			t.Errorf("log has no %q:\n%s", want, text)
		}
	}
	if n := strings.Count(text, "Downloading Jellyfin: 25%"); n != 1 {
		t.Errorf("25%% logged %d times", n)
	}
}

func TestUpdatesAreThrottled(t *testing.T) {
	w, _ := setup.New(t.TempDir()+"/.env", nil)
	r := newInstallReporter(w, 5, 30, 40)
	clock := time.Unix(1000, 0)
	r.now = func() time.Time { return clock }
	count := 0
	w.Notify(func(setup.Status) { count++ })
	count = 0
	for i := int64(1); i <= 50; i++ {
		clock = clock.Add(time.Millisecond)
		r.Progress("Jellyfin", i, 1000)
	}
	if count > 3 {
		t.Errorf("%d status updates in 50 ms", count)
	}
}

func TestHumanBytes(t *testing.T) {
	for in, want := range map[int64]string{12: "12 B", 2048: "2 KB", 5 << 20: "5.0 MB", 3 << 30: "3.0 GB"} {
		if got := humanBytes(in); got != want {
			t.Errorf("%d = %q, want %q", in, got, want)
		}
	}
}
