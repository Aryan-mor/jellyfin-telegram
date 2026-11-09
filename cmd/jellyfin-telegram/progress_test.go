package main

import (
	"strings"
	"testing"

	"github.com/Aryan-mor/jellyfin-telegram/internal/setup"
)

func TestFormatSetupLine(t *testing.T) {
	line := formatSetupLine(setup.Status{Stage: "Downloading Jellyfin", Percent: 50})
	if !strings.Contains(line, "50%") || !strings.Contains(line, "Downloading Jellyfin") || !strings.Contains(line, "#") {
		t.Fatalf("bar = %q", line)
	}
	ready := formatSetupLine(setup.Status{Ready: true})
	if !strings.Contains(ready, "100%") || !strings.Contains(ready, "Ready") {
		t.Fatalf("ready = %q", ready)
	}
	bad := formatSetupLine(setup.Status{Stage: "Could not start", Error: "disk full"})
	if !strings.Contains(bad, "disk full") {
		t.Fatalf("error = %q", bad)
	}
}

func TestShowProgressSkipsTinySteps(t *testing.T) {
	prev := setup.Status{Stage: "Downloading Jellyfin", Percent: 10}
	if showProgress(prev, setup.Status{Stage: "Downloading Jellyfin", Percent: 12}) {
		t.Fatal("a couple of percent should stay on the same bar")
	}
	if !showProgress(prev, setup.Status{Stage: "Downloading Jellyfin", Percent: 15}) {
		t.Fatal("the next fifth should print")
	}
	if !showProgress(prev, setup.Status{Stage: "Starting Jellyfin", Percent: 11}) {
		t.Fatal("a new stage should print")
	}
	if !showProgress(prev, setup.Status{Ready: true}) {
		t.Fatal("ready should print")
	}
}
