package main

import (
	"context"
	"log/slog"
	"strings"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/hashindex"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgpool"
)

// startHashIndex connects to the hashtag index bot and starts handing it the posts of public
// mirrors. The bot name defaults to tgHashtagIndexBot. A library bot sends the messages. It
// returns nil when that is turned off or no library bot exists. Opening the returned links still
// uses the signed-in account.
func startHashIndex(ctx context.Context, cfg config.Config, cat *catalog.Catalog, pool *tgpool.Pool, log *slog.Logger) *hashindex.Client {
	if cfg.HashtagIndexBot == "" || pool == nil || cat == nil {
		return nil
	}
	idx := &hashindex.Client{Bot: cfg.HashtagIndexBot, TG: pool}
	go (&hashindex.Publisher{Store: cat, Index: idx, Log: log}).Run(ctx)
	log.Info("hashtag index on", "bot", "@"+cfg.HashtagIndexBot)
	return idx
}

// ownMirror reports whether a channel name is one of this library's public mirrors. Posts there
// are already in the library, so the search does not offer them.
func ownMirror(cat *catalog.Catalog) func(string) bool {
	return func(channel string) bool {
		mirrors, err := cat.Mirrors(context.Background())
		if err != nil {
			return false
		}
		for _, m := range mirrors {
			if m.Username != "" && strings.EqualFold(m.Username, channel) {
				return true
			}
		}
		return false
	}
}
