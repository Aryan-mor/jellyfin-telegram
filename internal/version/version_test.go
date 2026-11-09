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
