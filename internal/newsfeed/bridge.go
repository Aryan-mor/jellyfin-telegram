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

// botRunner is the fuckjoin process. Interrupt drops one that never reports, so the next link
// is not stuck behind it.
type botRunner interface {
	EnsureRunning(context.Context) error
	Submit(string) error
	View() fuckjoin.Status
	Interrupt()
}

// Bridge asks the running fuckjoin userbot for one link and waits for its report.
type Bridge struct {
	FJ    botRunner
	TG    Saved
	Wait  time.Duration
	Grace time.Duration
	Ack   time.Duration
	Step  func(ctx context.Context, d time.Duration) error
}

// Open sends the link through fuckjoin and collects videos until its report arrives.
func (b *Bridge) Open(ctx context.Context, rawURL string) ([]tguser.Post, error) {
	if err := b.FJ.EnsureRunning(ctx); err != nil {
		return nil, err
	}
	sent, err := b.latest(ctx)
	if err != nil {
		return nil, err
	}
	// A link written while the userbot is still starting is deleted by a process that then
	// dies, and the wait would sit on a message that was never posted. Require the text to
	// show up in Saved Messages, and send it once more if the first process drops it.
	if err := b.hand(ctx, rawURL, sent); err != nil {
		b.FJ.Interrupt()
		if err := b.FJ.EnsureRunning(ctx); err != nil {
			return nil, err
		}
		sent, err = b.latest(ctx)
		if err != nil {
			return nil, err
		}
		if err := b.hand(ctx, rawURL, sent); err != nil {
			b.FJ.Interrupt()
			return nil, err
		}
	}
	wait := b.Wait
	if wait == 0 {
		wait = 8 * time.Minute
	}
	posts, done, err := b.watch(ctx, sent, time.Now().Add(wait))
	if err != nil || done {
		return finish(posts, done, err)
	}
	// The worker handles one link at a time. A link that never reports blocks every link after
	// it, so ask it to stop and, if it still does not, kill the process.
	if err := b.FJ.Submit("cancel"); err != nil && b.FJ.View().Running {
		b.FJ.Interrupt()
		return nil, errors.New("fuckjoin did not finish in time")
	}
	grace := b.Grace
	if grace == 0 {
		grace = 20 * time.Second
	}
	posts, done, err = b.watch(ctx, sent, time.Now().Add(grace))
	if err != nil || done {
		return finish(posts, done, err)
	}
	if b.FJ.View().Running {
		b.FJ.Interrupt()
	}
	return nil, errors.New("fuckjoin did not finish in time")
}

func (b *Bridge) latest(ctx context.Context) (int, error) {
	before, err := b.TG.SavedAfter(ctx, 0)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, item := range before {
		if item.ID > sent {
			sent = item.ID
		}
	}
	return sent, nil
}

func (b *Bridge) hand(ctx context.Context, rawURL string, sent int) error {
	if err := b.FJ.Submit(rawURL); err != nil {
		return err
	}
	ack := b.Ack
	if ack == 0 {
		ack = 30 * time.Second
	}
	deadline := time.Now().Add(ack)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		items, err := b.TG.SavedAfter(ctx, sent)
		if err != nil {
			return err
		}
		for _, item := range items {
			if strings.Contains(item.Text, rawURL) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errors.New("fuckjoin did not receive the link")
		}
		if b.FJ != nil && !b.FJ.View().Running {
			return errors.New("fuckjoin stopped before it received the link")
		}
		if err := b.step(ctx); err != nil {
			return err
		}
	}
}

func finish(posts []tguser.Post, done bool, err error) ([]tguser.Post, error) {
	if err != nil {
		return nil, err
	}
	if done && len(posts) == 0 {
		return nil, errors.New("fuckjoin finished without a video")
	}
	return posts, nil
}

// watch reads Saved Messages until a task report arrives, the deadline passes, or fuckjoin stops.
// posts are the videos seen so far.
func (b *Bridge) watch(ctx context.Context, sent int, deadline time.Time) ([]tguser.Post, bool, error) {
	seen := map[int]bool{}
	var posts []tguser.Post
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		items, err := b.TG.SavedAfter(ctx, sent)
		if err != nil {
			return nil, false, err
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
		if done || time.Now().After(deadline) {
			return posts, done, nil
		}
		if b.FJ != nil && !b.FJ.View().Running {
			return nil, false, errors.New("fuckjoin stopped before it finished the link")
		}
		if err := b.step(ctx); err != nil {
			return nil, false, err
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
