package main

import (
	"context"
	"log/slog"
	"strings"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/hashindex"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// startHashIndex connects to the hashtag index bot and starts handing it the posts of public
// mirrors. It returns nil when HASHTAG_INDEX_BOT is empty or no account can be used.
func startHashIndex(ctx context.Context, cfg config.Config, cat *catalog.Catalog, users *tguser.Manager, log *slog.Logger) *hashindex.Client {
	if cfg.HashtagIndexBot == "" || users == nil || cat == nil {
		return nil
	}
	idx := &hashindex.Client{Bot: cfg.HashtagIndexBot, TG: users}
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
