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
)

// forwardGap is the rest after copying n messages. Several files from one channel share it.
func forwardGap(n int) time.Duration {
	d := time.Duration(n) * 2 * time.Second
	if d < 8*time.Second {
		return 8 * time.Second
	}
	if d > 45*time.Second {
		return 45 * time.Second
	}
	return d
}

// clock is the one timer for a signed-in account. Every job on that account waits
// here, so a channel that yields several files does not start a timer of its own,
// and a FLOOD_WAIT parks the whole account until Telegram's clock runs out.
type clock struct {
	mu    sync.Mutex
	until time.Time
}

func (c *clock) wait(ctx context.Context) error {
	c.mu.Lock()
	d := time.Until(c.until)
	c.mu.Unlock()
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
	if at.After(c.until) {
		c.until = at
	}
}
