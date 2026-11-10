// Package indextalk sends one message to an index bot.
// A library bot goes first. The signed-in account is only the fallback, and only when that bot
// is not allowed to talk to other bots.
package indextalk

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/tgerr"
)

// Talker sends a message to a bot and returns its answer.
type Talker interface {
	TalkToBot(ctx context.Context, username, text string, wait time.Duration) (string, error)
}

// Fallback tries Bot, then Account. Account is used when no library bot is connected, or when
// Telegram refuses the bot with USER_BOT_TO_BOT_DISABLED. Any other error is returned as it is,
// because the message may already have been sent.
type Fallback struct {
	Bot     Talker
	Account Talker
	Log     *slog.Logger

	once sync.Once
}

// TalkToBot sends text. See Fallback.
func (f *Fallback) TalkToBot(ctx context.Context, username, text string, wait time.Duration) (string, error) {
	if f == nil {
		return "", errors.New("no bot is connected")
	}
	if f.Bot != nil {
		reply, err := f.Bot.TalkToBot(ctx, username, text, wait)
		if err == nil || f.Account == nil || !useAccount(err) {
			return reply, err
		}
		f.once.Do(func() {
			if f.Log != nil {
				f.Log.Warn("library bot cannot talk to other bots, using the signed-in account", "err", err)
			}
		})
		return f.Account.TalkToBot(ctx, username, text, wait)
	}
	if f.Account == nil {
		return "", errors.New("no bot is connected")
	}
	return f.Account.TalkToBot(ctx, username, text, wait)
}

func useAccount(err error) bool {
	if err == nil {
		return false
	}
	if tgerr.Is(err, "USER_BOT_TO_BOT_DISABLED") || strings.Contains(err.Error(), "USER_BOT_TO_BOT_DISABLED") {
		return true
	}
	return strings.Contains(err.Error(), "no bot is connected")
}
