package newsfeed

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
	fileresolver "github.com/Aryan-mor/telegram-file-resolver"
)

var _ fileresolver.API = (*tguser.Lent)(nil)

// Lender hands out a signed-in account. *tguser.Manager is one.
type Lender interface {
	Lend(ctx context.Context) (*tguser.Lent, error)
}

// Provider lends the signed-in account to the resolver. It waits while every account is busy
// uploading, so a link is not recorded as failed only because the uploader had the turn.
func Provider(l Lender) fileresolver.Provider {
	return fileresolver.ProviderFunc(func(ctx context.Context, fn func(context.Context, fileresolver.API) error) error {
		deadline := time.Now().Add(3 * time.Minute)
		for {
			lent, err := l.Lend(ctx)
			if err == nil {
				return fn(ctx, lent)
			}
			if !errors.Is(err, tguser.ErrNoFreeSession) || time.Now().After(deadline) {
				return err
			}
			t := time.NewTimer(2 * time.Second)
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
		}
	})
}

// Resolver opens a bot link with the in-process resolver and returns the video messages it copied
// into Saved Messages of the same account.
type Resolver struct {
	Eng *fileresolver.Engine
	// Note shows what the walk is doing, such as a wait for Telegram's join limit. Nil ignores it.
	Note func(string)
	// MaxWait is the longest single join limit the resolver sits out. Zero means 15 minutes.
	MaxWait time.Duration
	// Sleep exists for tests.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Open follows the link and returns the copied videos, newest copy ids in Saved Messages.
// Telegram limits how fast an account may join channels, and one link can need several. That limit
// is the account's, not the link's, so Open waits it out and tries the same link again instead of
// letting it use up one of its few tries.
func (r *Resolver) Open(ctx context.Context, url string) ([]tguser.Post, error) {
	maxWait := r.MaxWait
	if maxWait <= 0 {
		maxWait = 15 * time.Minute
	}
	sleep := r.Sleep
	if sleep == nil {
		sleep = pause
	}
	var res *fileresolver.Result
	var err error
	for range 4 {
		res, err = r.Eng.Submit(ctx, url, fileresolver.Options{Sink: fileresolver.ForwardSink{}})
		var limit *fileresolver.RateLimitError
		if !errors.As(err, &limit) || limit.Wait > maxWait {
			break
		}
		// A little extra, spread at random, so retries do not land on a fixed beat.
		wait := limit.Wait + 5*time.Second + time.Duration(rand.Int64N(int64(45*time.Second)))
		if r.Note != nil {
			r.Note("Telegram limits joining channels; waiting " + wait.Round(time.Second).String())
		}
		if err = sleep(ctx, wait); err != nil {
			return nil, err
		}
		err = &fileresolver.RateLimitError{Wait: limit.Wait}
	}
	if err != nil {
		return nil, err
	}
	var posts []tguser.Post
	for _, m := range res.Delivered.Messages {
		if p, ok := tguser.PostFrom(m); ok && p.DocID != 0 {
			posts = append(posts, p)
		}
	}
	if len(posts) == 0 {
		return nil, errors.New("the bot finished without a video")
	}
	return posts, nil
}

func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// SetNote lets another package show a line in the News channels tab.
func (s *Service) SetNote(text string) { s.setNote(text) }
