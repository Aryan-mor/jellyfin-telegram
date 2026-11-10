package tguser

import (
	"context"
	"sync"
	"time"
)

const (
	// historyGap is the rest after one page of a channel, so a long history is not read in a burst.
	historyGap = 4 * time.Second
	// resolverGap is the rest between two calls while a bot link is opened.
	resolverGap = 3 * time.Second
	// searchGap is the rest after one global search.
	searchGap = 8 * time.Second
	// callGap is the rest after every job on an account. The session loop applies it,
	// so a caller that forgets its own hold still cannot start the next job immediately.
	// A longer hold, including a FLOOD_WAIT, wins.
	callGap = 2 * time.Second
)

// forwardGap is the rest after copying n messages. Several files from one channel share it.
// The daily copy cap is off by default, so this rest is what keeps a long run from looking like a flood.
func forwardGap(n int) time.Duration {
	d := time.Duration(n) * 3 * time.Second
	if d < 15*time.Second {
		return 15 * time.Second
	}
	if d > 60*time.Second {
		return 60 * time.Second
	}
	return d
}

// clock is the one timer for a signed-in account. Every job on that account waits
// here, so a channel that yields several files does not start a timer of its own,
// and a FLOOD_WAIT parks the whole account until Telegram's clock runs out.
type clock struct {
	mu         sync.Mutex
	gapUntil   time.Time
	floodUntil time.Time
}

// remaining is how long a background job still has to wait. Zero means it can run.
func (c *clock) remaining() time.Duration { return c.blockFor(false) }

// blockFor is the wait before the next job. A job the person asked for skips the
// voluntary rest and still waits out a FLOOD_WAIT.
func (c *clock) blockFor(user bool) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	flood := time.Until(c.floodUntil)
	if flood < 0 {
		flood = 0
	}
	if user {
		return flood
	}
	gap := time.Until(c.gapUntil)
	if gap < 0 {
		gap = 0
	}
	if gap > flood {
		return gap
	}
	return flood
}

func (c *clock) wait(ctx context.Context) error {
	d := c.remaining()
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		t.Stop()
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// hold pushes the next job out to at least d from now. A shorter hold never
// shortens a wait that is already in force.
func (c *clock) hold(d time.Duration) {
	if d <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	at := time.Now().Add(d)
	if at.After(c.gapUntil) {
		c.gapUntil = at
	}
}

// holdFlood parks every job, including one the person asked for, until Telegram's wait ends.
func (c *clock) holdFlood(d time.Duration) {
	if d <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	at := time.Now().Add(d)
	if at.After(c.floodUntil) {
		c.floodUntil = at
	}
}
