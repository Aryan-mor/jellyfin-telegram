package idscan

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
)

type memStore struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memStore) StateValue(_ context.Context, k string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[k], nil
}

func (s *memStore) SetStateValue(_ context.Context, k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = v
	return nil
}

type channel struct {
	top    int
	holes  map[int]bool
	handle []int
	// post adds messages while the walk runs.
	onLookup func(calls int)
	calls    int
}

func (c *channel) lookup(_ context.Context, ids []int) ([]media.Message, error) {
	c.calls++
	if c.onLookup != nil {
		c.onLookup(c.calls)
	}
	out := make([]media.Message, 0, len(ids))
	for _, id := range ids {
		out = append(out, media.Message{ID: id, Exists: id >= 1 && id <= c.top && !c.holes[id]})
	}
	return out, nil
}

func newScan(c *channel) *Scan {
	return &Scan{
		Lookup: c.lookup,
		Handle: func(_ context.Context, m media.Message) error { c.handle = append(c.handle, m.ID); return nil },
		Store:  &memStore{m: map[string]string{}},
		Name:   "t", Batch: 10,
	}
}

func TestFreshChannelIsReadNewestFirstAndCoveredFully(t *testing.T) {
	c := &channel{top: 437, holes: map[int]bool{50: true, 51: true, 52: true, 300: true}}
	s := newScan(c)
	ctx := context.Background()
	if err := s.Up(ctx, 50); err != nil {
		t.Fatal(err)
	}
	if len(c.handle) == 0 || c.handle[0] < 400 {
		t.Fatalf("the first messages read should be the newest, got %v", c.handle[:min(5, len(c.handle))])
	}
	done, err := s.Down(ctx, -1, 3)
	if err != nil || !done {
		t.Fatalf("down: %v %v", done, err)
	}
	want := 0
	seen := map[int]int{}
	for _, id := range c.handle {
		seen[id]++
	}
	for id := 1; id <= c.top; id++ {
		if c.holes[id] {
			continue
		}
		want++
		if seen[id] == 0 {
			t.Fatalf("message %d was never read", id)
		}
	}
	if len(seen) != want {
		t.Fatalf("read %d distinct, want %d", len(seen), want)
	}
	// Covered: a second pass only looks above the top.
	c.handle = nil
	if err := s.Up(ctx, 50); err != nil {
		t.Fatal(err)
	}
	if len(c.handle) != 0 {
		t.Fatalf("a covered channel was read again: %v", c.handle)
	}
}

func TestNewMessagesComeBeforeOldHistory(t *testing.T) {
	c := &channel{top: 300, holes: map[int]bool{}}
	s := newScan(c)
	ctx := context.Background()
	if err := s.Up(ctx, 50); err != nil {
		t.Fatal(err)
	}
	c.top = 320 // twenty new messages arrive while the old history is being read
	if _, err := s.Down(ctx, 4, 2); err != nil {
		t.Fatal(err)
	}
	pos := func(id int) int { return slices.Index(c.handle, id) }
	if pos(320) < 0 {
		t.Fatalf("the new messages were not picked up: %v", c.handle)
	}
	// 320 must be read before the walk has gone far down.
	if low := slices.Index(c.handle, 250); low >= 0 && pos(320) > low {
		t.Fatalf("new message 320 came after old message 250")
	}
}

func TestOlderVersionStateMeansCovered(t *testing.T) {
	c := &channel{top: 100, holes: map[int]bool{}}
	s := newScan(c)
	s.Legacy = func(context.Context) (int, error) { return 100, nil }
	ctx := context.Background()
	if err := s.Up(ctx, 20); err != nil {
		t.Fatal(err)
	}
	done, err := s.Down(ctx, -1, 3)
	if err != nil || !done || len(c.handle) != 0 {
		t.Fatalf("a channel read by an older version was walked again: %v %v %v", done, err, c.handle)
	}
}

func TestEmptyChannel(t *testing.T) {
	c := &channel{top: 0}
	s := newScan(c)
	ctx := context.Background()
	if err := s.Up(ctx, 20); err != nil {
		t.Fatal(err)
	}
	if done, err := s.Down(ctx, -1, 3); err != nil || !done {
		t.Fatalf("%v %v", done, err)
	}
}
