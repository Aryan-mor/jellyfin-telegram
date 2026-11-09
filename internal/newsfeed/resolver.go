package newsfeed

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
	fileresolver "github.com/Aryan-mor/telegram-file-resolver"
)

// ErrSlowDown means Telegram limited this account. The account clock already holds
// every later job, so the caller must not open the next link on a timer of its own.
var ErrSlowDown = errors.New("telegram asked this account to wait")

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
}

// Open follows the link and returns the copied videos, newest copy ids in Saved Messages.
// A rate limit is the account's, not this link's. Open does not sleep on its own: the
// account clock already parks every later call, and the walk stops instead of opening
// the next post while Telegram is still refusing.
func (r *Resolver) Open(ctx context.Context, url string) ([]tguser.Post, error) {
	res, err := r.Eng.Submit(ctx, url, fileresolver.Options{Sink: fileresolver.ForwardSink{}})
	var limit *fileresolver.RateLimitError
	if errors.As(err, &limit) {
		if r.Note != nil && limit.Wait > 0 {
			r.Note("Telegram limits this account; the next check waits " + limit.Wait.Round(time.Second).String())
		}
		return nil, fmt.Errorf("%w: %s", ErrSlowDown, err.Error())
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

// SetNote lets another package show a line in the News channels tab.
func (s *Service) SetNote(text string) { s.setNote(text) }
