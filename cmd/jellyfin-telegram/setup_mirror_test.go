package main

import "testing"

func TestChannelUsernameShape(t *testing.T) {
	if !usableChannelUsername("films_ab12cd") {
		t.Fatal("a generated username was refused")
	}
	for _, bad := range []string{"", "ab", "1films", "Films_ab", "has space", "way_too_long_for_a_telegram_username_ok"} {
		if usableChannelUsername(bad) {
			t.Errorf("%q was accepted", bad)
		}
	}
}
