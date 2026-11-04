package main

import (
	"context"
	"log/slog"
	"time"
)

// retry runs fn up to attempts times, waiting between them, and stops early when ctx ends. A step that
// talks to another program on this machine can fail once at random right after that program starts.
func retry(ctx context.Context, attempts int, wait time.Duration, log *slog.Logger, what string, fn func() error) error {
	var err error
	for i := 1; i <= attempts; i++ {
		if err = fn(); err == nil || ctx.Err() != nil {
			return err
		}
		if i == attempts {
			break
		}
		if log != nil {
			log.Warn(what+" failed, trying again", "attempt", i, "of", attempts, "err", err)
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}
