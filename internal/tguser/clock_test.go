package tguser

import (
	"context"
	"testing"
	"time"
)

func TestClockHoldIsSharedAndNeverShortened(t *testing.T) {
	var c clock
	c.hold(40 * time.Millisecond)
	c.hold(10 * time.Millisecond)
	start := time.Now()
	if err := c.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 30*time.Millisecond {
		t.Fatal("a later short hold cut the shared wait")
	}
}

func TestClockWaitStopsWhenCancelled(t *testing.T) {
	var c clock
	c.hold(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.wait(ctx); err == nil {
		t.Fatal("wait ignored cancel")
	}
}
