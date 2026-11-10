package version

import (
	"context"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"1.0.10", "1.0.9", true},
		{"1.0.9", "1.0.10", false},
		{"v1.1.0", "1.0.99", true},
		{"1.0.1", "1.0.1", false},
		{"1.0", "1.0.0", false},
		{"nope", "1.0.0", false},
		{"1.0.10-nightly.3", "1.0.10-nightly.2", true},
		{"1.0.10-nightly.2", "1.0.10-nightly.3", false},
		{"1.0.11-nightly.1", "1.0.10-nightly.9", true},
		{"1.0.10-nightly.nope", "1.0.10", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q,%q)=%v", c.a, c.b, got)
		}
	}
}

func TestChooseStaysOnOneChannel(t *testing.T) {
	list := []Release{
		{Tag: "v1.0.51-nightly.1", Pre: true},
		{Tag: "v1.0.50", Pre: false},
		{Tag: "v1.0.49-nightly.4", Pre: true},
		{Tag: "v1.0.40", Pre: false},
	}
	stable, ok := Choose("stable", list)
	if !ok || stable.Tag != "v1.0.50" {
		t.Fatalf("stable = %+v ok=%v", stable, ok)
	}
	nightly, ok := Choose("nightly", list)
	if !ok || nightly.Tag != "v1.0.51-nightly.1" {
		t.Fatalf("nightly = %+v ok=%v", nightly, ok)
	}
}

func TestDevBuildNeverOffersAnUpdate(t *testing.T) {
	old := Fetch
	t.Cleanup(func() { Fetch = old })
	Fetch = func(context.Context) (Release, error) { return Release{Tag: "v9.9.9", URL: "u"}, nil }
	if Check(context.Background()).Available {
		t.Fatal("a dev build offered an update")
	}
}

func TestEachCheckLooksAgain(t *testing.T) {
	old := Fetch
	t.Cleanup(func() {
		Fetch = old
		Version = "dev"
		Channel = "stable"
		flight = nil
	})
	Version = "1.0.3-nightly.06"
	Channel = "nightly"
	calls := 0
	Fetch = func(context.Context) (Release, error) {
		calls++
		if calls == 1 {
			return Release{}, context.DeadlineExceeded
		}
		return Release{Tag: "v1.0.3-nightly.07", Pre: true, URL: "https://example.test/nightly"}, nil
	}
	if got := Check(context.Background()); got.Available || got.Latest != "" {
		t.Fatalf("offline = %+v", got)
	}
	got := Check(context.Background())
	if !got.Available || got.Latest != "1.0.3-nightly.07" || calls != 2 {
		t.Fatalf("second check = %+v calls=%d", got, calls)
	}
}

func TestReleasesFromFeed(t *testing.T) {
	feed := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <title>v1.0.3-nightly.07</title>
    <link rel="alternate" href="https://github.com/Aryan-mor/jellyfin-telegram/releases/tag/v1.0.3-nightly.07"/>
    <content type="html">nightly notes</content>
  </entry>
  <entry>
    <title>v1.0.3</title>
    <link rel="alternate" href="https://github.com/Aryan-mor/jellyfin-telegram/releases/tag/v1.0.3"/>
  </entry>
</feed>`)
	list, err := releasesFromFeed(feed)
	if err != nil {
		t.Fatal(err)
	}
	nightly, ok := Choose("nightly", list)
	if !ok || nightly.Tag != "v1.0.3-nightly.07" || !nightly.Pre {
		t.Fatalf("nightly = %+v ok=%v", nightly, ok)
	}
	asset, ok := PickAsset(nightly.Assets, "windows", "amd64")
	if !ok || asset.URL != "https://github.com/Aryan-mor/jellyfin-telegram/releases/download/v1.0.3-nightly.07/jellyfin-telegram-windows-amd64.zip" {
		t.Fatalf("asset = %+v ok=%v", asset, ok)
	}
	stable, ok := Choose("stable", list)
	if !ok || stable.Tag != "v1.0.3" || stable.Pre {
		t.Fatalf("stable = %+v ok=%v", stable, ok)
	}
}
