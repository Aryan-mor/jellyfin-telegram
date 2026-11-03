package tguser

import "testing"

func TestPaceRefusesNegativeNumbers(t *testing.T) {
	good := Pace{MBPerSec: 8, RestMinutes: 5, LongEveryGB: 20, LongRestMinutes: 60, DailyGB: 50, Hours: "01-08"}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []Pace{{MBPerSec: -1}, {RestMinutes: -5}, {LongEveryGB: -1}, {LongRestMinutes: -1}, {DailyGB: -50}, {Hours: "25-99"}} {
		if p.Validate() == nil {
			t.Errorf("%+v was accepted and would stop the next start", p)
		}
	}
}

func TestAnAbandonedSignInDoesNotBlockTheNextOne(t *testing.T) {
	m := &Manager{}
	p := &pendingLogin{}
	m.pending = p
	m.clearPending(&pendingLogin{})
	if m.pending != p {
		t.Fatal("another sign-in's end removed this one")
	}
	m.clearPending(p)
	if m.pending != nil {
		t.Fatal("the finished sign-in still blocks a new one")
	}
}
