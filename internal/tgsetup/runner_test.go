package tgsetup

import (
	"regexp"
	"strings"
	"testing"
)

func TestUsernameShape(t *testing.T) {
	re := regexp.MustCompile(`^[a-z][a-z0-9]{7,11}bot$`)
	seen := map[string]bool{}
	for i := 0; i < 30; i++ {
		got := username()
		if !re.MatchString(got) {
			t.Fatalf("got %q", got)
		}
		if strings.Contains(got, "jelly") || strings.Contains(got, "_") {
			t.Fatalf("signature in %q", got)
		}
		if seen[got] {
			t.Fatalf("duplicate %q", got)
		}
		seen[got] = true
	}
}

func TestBotNameHasNoFixedWord(t *testing.T) {
	re := regexp.MustCompile(`^[A-Z][a-z]{5,9}$`)
	seen := map[string]bool{}
	for i := 0; i < 30; i++ {
		got := botName()
		if !re.MatchString(got) || strings.Contains(strings.ToLower(got), "jelly") {
			t.Fatalf("got %q", got)
		}
		seen[got] = true
	}
	if len(seen) < 2 {
		t.Fatal("names did not vary")
	}
}

func TestExcludeOwnedDropsBotsAlreadyTaken(t *testing.T) {
	got := excludeOwned([]OwnedBot{{Username: "Keep_Bot"}, {Username: "old_bot"}}, []string{"keep_bot"})
	if len(got) != 1 || got[0].Username != "old_bot" {
		t.Fatalf("got %+v", got)
	}
}

func TestSlugNeverStartsWithADigit(t *testing.T) {
	if got := slug("42"); got[0] < 'a' || got[0] > 'z' {
		t.Errorf("got %q", got)
	}
}
