package newsfeed

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/fuckjoin"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// Saved is the account chat fuckjoin reads and writes.
type Saved interface {
	SendSaved(ctx context.Context, text string) (int, error)
	SavedAfter(ctx context.Context, afterID int) ([]tguser.Saved, error)
}

// Bridge asks the running fuckjoin userbot for one link and waits for its report.
type Bridge struct {
	FJ   *fuckjoin.Runtime
	TG   Saved
	Wait time.Duration
	Step func(ctx context.Context, d time.Duration) error
}

// Open sends the link through fuckjoin and collects videos until its report arrives.
func (b *Bridge) Open(ctx context.Context, rawURL string) ([]tguser.Post, error) {
	if err := b.FJ.EnsureRunning(ctx); err != nil {
		return nil, err
	}
	before, err := b.TG.SavedAfter(ctx, 0)
	if err != nil {
		return nil, err
	}
	sent := 0
	for _, item := range before {
		if item.ID > sent {
			sent = item.ID
		}
	}
	if err := b.FJ.Submit(rawURL); err != nil {
		return nil, err
	}
	wait := b.Wait
	if wait == 0 {
		wait = 8 * time.Minute
	}
	deadline := time.Now().Add(wait)
	seen := map[int]bool{}
	var posts []tguser.Post
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		items, err := b.TG.SavedAfter(ctx, sent)
		if err != nil {
			return nil, err
		}
		done := false
		for _, item := range items {
			if item.Post.DocID != 0 && !seen[item.ID] {
				seen[item.ID] = true
				posts = append(posts, item.Post)
			}
			if strings.Contains(item.Text, "Task Report") {
				done = true
			}
		}
		if done {
			if len(posts) == 0 {
				return nil, errors.New("fuckjoin finished without a video")
			}
			return posts, nil
		}
		if time.Now().After(deadline) {
			return nil, errors.New("fuckjoin did not finish in time")
		}
		if err := b.step(ctx); err != nil {
			return nil, err
		}
	}
}

func (b *Bridge) step(ctx context.Context) error {
	if b.Step != nil {
		return b.Step(ctx, 3*time.Second)
	}
	t := time.NewTimer(3 * time.Second)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
