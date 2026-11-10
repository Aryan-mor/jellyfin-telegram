package tgsearch

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func TestNextTryGrowsAndResetsForADigitalRelease(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	if got := nextTry(schedRec{}, now, false); !got.Equal(now) {
		t.Fatalf("first try %s", got)
	}
	cases := []struct {
		age  time.Duration
		wait time.Duration
	}{
		{time.Hour, 6 * time.Hour},
		{23 * time.Hour, 6 * time.Hour},
		{24 * time.Hour, 24 * time.Hour},
		{3 * 24 * time.Hour, 24 * time.Hour},
		{4 * 24 * time.Hour, 48 * time.Hour},
		{11 * 24 * time.Hour, 48 * time.Hour},
		{12 * 24 * time.Hour, 6 * 24 * time.Hour},
		{40 * 24 * time.Hour, 6 * 24 * time.Hour},
	}
	for _, c := range cases {
		first := now.Add(-c.age)
		rec := schedRec{First: first.Unix(), Last: now.Unix()}
		got := nextTry(rec, now, false)
		if want := now.Add(c.wait); !got.Equal(want) {
			t.Fatalf("age %s: got %s want %s", c.age, got, want)
		}
	}
	recent := schedRec{
		First: now.Add(-20 * 24 * time.Hour).Unix(), Last: now.Unix(),
		Digital: true, Released: now.Add(-24 * time.Hour).Unix(),
	}
	if got, want := nextTry(recent, now, true), now.Add(6*time.Hour); !got.Equal(want) {
		t.Fatalf("fresh digital release waits %s", got.Sub(now))
	}
	old := recent
	old.Released = now.Add(-10 * 24 * time.Hour).Unix()
	if got, want := nextTry(old, now, true), now.Add(6*24*time.Hour); !got.Equal(want) {
		t.Fatalf("old digital release waits %s", got.Sub(now))
	}
}

func TestDigitalFlipSearchesAgain(t *testing.T) {
	cat := openCat(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	q := Query{TMDB: 7, Movie: true}
	s := &Service{Store: cat, Now: func() time.Time { return now }}
	s.noteSearched(ctx, q)
	now = now.Add(2 * time.Hour)
	if !s.coolingDown(ctx, q) {
		t.Fatal("day 0 should wait 6 hours")
	}
	q.DigitalOut = true
	q.Release = now.Add(-time.Hour)
	if s.coolingDown(ctx, q) {
		t.Fatal("a new digital release should be searched now")
	}
	s.noteSearched(ctx, q)
	now = now.Add(time.Hour)
	if !s.coolingDown(ctx, q) {
		t.Fatal("the short cadence should apply after the reset")
	}
}

func TestOldSearchTimeIsKeptOnce(t *testing.T) {
	cat := openCat(t)
	ctx := context.Background()
	q := Query{TMDB: 3, Movie: true}
	now := time.Now().UTC()
	if err := cat.SetStateValue(ctx, "tgsearch.q."+q.Text(), strconv.FormatInt(now.Add(-time.Hour).Unix(), 10)); err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: cat, Now: func() time.Time { return now }}
	if !s.coolingDown(ctx, q) {
		t.Fatal("migrated stamp should still be cooling")
	}
	raw, err := cat.StateValue(ctx, schedKey(q))
	if err != nil || raw == "" {
		t.Fatal(raw, err)
	}
}
