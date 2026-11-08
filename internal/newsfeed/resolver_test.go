package newsfeed

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
	fileresolver "github.com/Aryan-mor/telegram-file-resolver"
)

type lender func(ctx context.Context) (*tguser.Lent, error)

func (l lender) Lend(ctx context.Context) (*tguser.Lent, error) { return l(ctx) }

func TestProviderReturnsOtherErrorsAtOnce(t *testing.T) {
	boom := errors.New("that account is not signed in")
	calls := 0
	p := Provider(lender(func(context.Context) (*tguser.Lent, error) { calls++; return nil, boom }))
	err := p.Run(context.Background(), func(context.Context, fileresolver.API) error { t.Fatal("fn must not run"); return nil })
	if !errors.Is(err, boom) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestProviderStopsWaitingWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := Provider(lender(func(context.Context) (*tguser.Lent, error) { return nil, tguser.ErrNoFreeSession }))
	if err := p.Run(ctx, func(context.Context, fileresolver.API) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenDoesNotSleepForOrdinaryErrors(t *testing.T) {
	// The engine here has no account to borrow, so Submit fails with a plain error: Open must
	// return it at once and never sleep for it.
	eng, err := fileresolver.New(Provider(lender(func(context.Context) (*tguser.Lent, error) {
		return nil, errors.New("that account is not signed in")
	})), fileresolver.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	slept := false
	r := &Resolver{Eng: eng, Sleep: func(context.Context, time.Duration) error { slept = true; return nil }}
	if _, err := r.Open(context.Background(), "https://t.me/somebot?start=x"); err == nil || slept {
		t.Fatalf("err=%v slept=%v", err, slept)
	}
}
