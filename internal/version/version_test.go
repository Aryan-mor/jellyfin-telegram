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
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q,%q)=%v", c.a, c.b, got)
		}
	}
}

func TestDevBuildNeverOffersAnUpdate(t *testing.T) {
	old := Fetch
	t.Cleanup(func() { Fetch = old })
	Fetch = func(context.Context) (string, string, string, error) { return "v9.9.9", "u", "", nil }
	if Check(context.Background()).Available {
		t.Fatal("a dev build offered an update")
	}
}
