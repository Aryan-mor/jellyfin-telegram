package tguser

import (
	"testing"
	"time"
)

func TestMaxBytes(t *testing.T) {
	if MaxBytes(false) != 4000*PartSize || MaxBytes(true) != 8000*PartSize {
		t.Fatalf("caps %d %d", MaxBytes(false), MaxBytes(true))
	}
}

func TestAllow(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.Local)
	p := Pace{MBPerSec: 8, RestMinutes: 5, LongEveryGB: 20, LongRestMinutes: 60, DailyGB: 50}
	if g := Allow(p, now, 10, 0, 0, time.Time{}, MaxBytes(false)); !g.Ready {
		t.Fatalf("ready: %+v", g)
	}
	if g := Allow(p, now, MaxBytes(false)+1, 0, 0, time.Time{}, MaxBytes(false)); g.Ready || g.Reason == "" {
		t.Fatalf("too big: %+v", g)
	}
	rest := now.Add(time.Minute)
	if g := Allow(p, now, 10, 0, 0, rest, MaxBytes(false)); g.Ready || g.Wait < time.Second {
		t.Fatalf("rest: %+v", g)
	}
	night := Pace{Hours: "01-08", DailyGB: 50}
	if g := Allow(night, now, 10, 0, 0, time.Time{}, MaxBytes(true)); g.Ready || g.Wait == 0 {
		t.Fatalf("hours: %+v", g)
	}
	if g := Allow(p, now, 10, int64(50)<<30, 0, time.Time{}, MaxBytes(true)); g.Ready {
		t.Fatal("daily cap should hold the file")
	}
	d, reset := RestAfter(p, 20<<30, 2)
	if !reset || d != 120*time.Minute {
		t.Fatalf("long rest %s %v", d, reset)
	}
}

func TestPaceOverride(t *testing.T) {
	zero := 0
	m := Meta{MBPerSec: &zero}
	got := m.pace(Pace{MBPerSec: 8, RestMinutes: 5})
	if got.MBPerSec != 0 || got.RestMinutes != 5 {
		t.Fatalf("%+v", got)
	}
}
