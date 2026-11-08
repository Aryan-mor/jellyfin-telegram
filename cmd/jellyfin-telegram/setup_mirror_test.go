package main

import (
	"strings"
	"testing"

	"github.com/Aryan-mor/jellyfin-telegram/internal/tgsetup"
)

func TestFallbackMirrorTitleMakesAUsableName(t *testing.T) {
	title := fallbackMirrorTitle()
	if !strings.HasPrefix(title, "Films ") {
		t.Fatalf("title = %q", title)
	}
	name := "films_" + strings.TrimPrefix(title, "Films ")
	if !tgsetup.ValidChannelUsername(name) {
		t.Fatalf("%q is not a usable username", name)
	}
}
