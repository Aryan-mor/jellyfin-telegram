package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/indextalk"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgpool"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgsetup"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// indexTalk sends index messages from a library bot, and from the signed-in account only when
// that bot cannot talk to other bots.
func indexTalk(pool *tgpool.Pool, users *tguser.Manager, log *slog.Logger) *indextalk.Fallback {
	f := &indextalk.Fallback{Log: log}
	if pool != nil {
		f.Bot = pool
	}
	if users != nil {
		f.Account = users
	}
	return f
}

// allowBotTalk turns on Bot-to-Bot Communication Mode for the library bots and for the index
// bots this account owns. A name that is already done is left alone. Failure does not stop
// the program: the account remains the fallback sender.
func allowBotTalk(ctx context.Context, pool *tgpool.Pool, users *tguser.Manager, cat *catalog.Catalog, extra []string, log *slog.Logger) {
	if users == nil {
		return
	}
	go func() {
		if pool != nil {
			wait, cancel := context.WithTimeout(ctx, 2*time.Minute)
			pool.WaitSettled(wait)
			cancel()
		}
		if ctx.Err() != nil {
			return
		}
		names := map[string]bool{}
		if pool != nil {
			for _, st := range pool.Statuses() {
				addBotName(names, st.Username)
			}
		}
		for _, name := range extra {
			addBotName(names, name)
		}
		runner := &tgsetup.Runner{S: users}
		for name := range names {
			if ctx.Err() != nil {
				return
			}
			key := "bot_to_bot:" + strings.ToLower(name)
			if cat != nil {
				have, err := cat.StateValue(ctx, key)
				if err == nil && (have == "on" || have == "no") {
					continue
				}
			}
			err := runner.AllowBotTalk(ctx, name)
			switch {
			case err == nil:
				if log != nil {
					log.Info("bot-to-bot mode on", "bot", name)
				}
				rememberBotTalk(ctx, cat, key, "on")
			case errors.Is(err, tgsetup.ErrNotOwner):
				if log != nil {
					log.Info("bot-to-bot mode left alone, this account does not own the bot", "bot", name)
				}
				rememberBotTalk(ctx, cat, key, "no")
			default:
				if log != nil {
					log.Warn("bot-to-bot mode", "bot", name, "err", err)
				}
			}
		}
	}()
}

func addBotName(names map[string]bool, raw string) {
	name := strings.TrimPrefix(strings.TrimSpace(raw), "@")
	if name != "" {
		names[strings.ToLower(name)] = true
	}
}

func rememberBotTalk(ctx context.Context, cat *catalog.Catalog, key, value string) {
	if cat == nil {
		return
	}
	_ = cat.SetStateValue(ctx, key, value)
}
