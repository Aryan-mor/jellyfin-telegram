package main

import (
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
)

func TestAutoDueRaw(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	page, ready := autoDueRaw("", now)
	if page != 1 || !ready {
		t.Fatalf("empty: %d %v", page, ready)
	}
	page, ready = autoDueRaw("1700000000 3", now)
	if page != 3 || ready {
		t.Fatalf("same day: %d %v", page, ready)
	}
	page, ready = autoDueRaw("1699900000 4", now)
	if page != 4 || !ready {
		t.Fatalf("next day: %d %v", page, ready)
	}
}

func TestListedChannel(t *testing.T) {
	list := []catalog.Source{{Username: "News"}, {ChannelID: 42}}
	if !listedChannel(list, "@news") || !listedChannel(list, "c/42") {
		t.Fatal("known channel was treated as new")
	}
	if listedChannel(list, "@other") {
		t.Fatal("new channel was treated as known")
	}
}
