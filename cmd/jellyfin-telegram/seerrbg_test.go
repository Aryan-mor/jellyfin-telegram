package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/managed"
	"github.com/Aryan-mor/jellyfin-telegram/internal/setup"
)

func TestSeerrPageFollowsTheInstall(t *testing.T) {
	w, _ := setup.New(t.TempDir()+"/.env", nil)
	p := &seerrProgress{}
	r := newInstallReporter(seerrSink{progress: p, wizard: w}, 0, 50, 100)
	clock := time.Unix(1000, 0)
	r.now = func() time.Time { return clock }

	r.Progress("Seerr", 50<<20, 100<<20)
	got := p.State()
	if got.Ready || got.Failed || !strings.Contains(got.Text, "Downloading Seerr") || !strings.Contains(got.Text, "50.0 MB of 100.0 MB") {
		t.Fatalf("download: %+v", got)
	}
	clock = clock.Add(time.Second)
	r.Progress(managed.UnpackPrefix+"Seerr", 1, 2)
	if got := p.State(); !strings.Contains(got.Text, "Unpacking Seerr") {
		t.Fatalf("unpack: %+v", got)
	}
	p.ready()
	if got := p.State(); !got.Ready {
		t.Fatalf("ready: %+v", got)
	}
}

func TestSeerrProgressNeverTouchesTheSetupBar(t *testing.T) {
	w, _ := setup.New(t.TempDir()+"/.env", nil)
	w.SetStatus(setup.Status{Stage: "Setting up Jellyfin", Percent: 50})
	p := &seerrProgress{}
	r := newInstallReporter(seerrSink{progress: p, wizard: w}, 0, 50, 100)
	r.Progress("Seerr", 10, 100)
	var now setup.Status
	w.Notify(func(s setup.Status) { now = s })
	w.Notify(nil)
	if now.Stage != "Setting up Jellyfin" || now.Percent != 50 {
		t.Errorf("Seerr changed the setup bar: %+v", now)
	}
	// Its lines still reach the log the setup page shows.
	if !strings.Contains(strings.Join(w.LogLines(), "\n"), "Downloading Seerr") {
		t.Errorf("log: %v", w.LogLines())
	}
}

func TestSeerrFailureIsShownNotFatal(t *testing.T) {
	p := &seerrProgress{}
	p.fail(errors.New("no Seerr build for plan9/amd64"))
	got := p.State()
	if !got.Failed || got.Ready || !strings.Contains(got.Text, "plan9") {
		t.Fatalf("%+v", got)
	}
}
