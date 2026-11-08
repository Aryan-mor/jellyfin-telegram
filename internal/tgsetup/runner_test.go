package tgsetup

import (
	"regexp"
	"testing"
)

func TestUsernameShape(t *testing.T) {
	re := regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{4,31}$`)
	for _, title := range []string{"Jellyfin library", "ختم", "123 films", "A very long channel title indeed"} {
		got := username(slug(title))
		if !re.MatchString(got) || got[len(got)-3:] != "bot" {
			t.Errorf("%q gave %q", title, got)
		}
	}
	if a, b := username("x"), username("x"); a == b {
		t.Error("two usernames were equal")
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
