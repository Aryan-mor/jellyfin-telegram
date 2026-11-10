package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/chanindex"
	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/newsfeed"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgpool"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgsource"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// startChannelIndex connects to the channel index bot. The bot name defaults to tgChannelIndexBot.
// A library bot sends the messages. It returns nil when that is turned off or no library bot exists.
func startChannelIndex(ctx context.Context, cfg config.Config, cat *catalog.Catalog, pool *tgpool.Pool, sources *tgsource.Service, news *newsfeed.Service, users *tguser.Manager, log *slog.Logger) *chanindex.Client {
	if cfg.ChannelIndexBot == "" || pool == nil || cat == nil {
		return nil
	}
	idx := &chanindex.Client{Bot: cfg.ChannelIndexBot, TG: pool}
	go watchChannelIndex(ctx, cat, idx, log)
	go autoAddChannels(ctx, cat, sources, news, users, idx, log)
	log.Info("channel index on", "bot", "@"+cfg.ChannelIndexBot)
	return idx
}

// watchChannelIndex sends one upvote after a channel added from the index has brought in 3 files.
func watchChannelIndex(ctx context.Context, cat *catalog.Catalog, idx *chanindex.Client, log *slog.Logger) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		if err := voteReadyChannels(ctx, cat, idx, log); err != nil && ctx.Err() == nil {
			log.Warn("channel index vote", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func voteReadyChannels(ctx context.Context, cat *catalog.Catalog, idx *chanindex.Client, log *slog.Logger) error {
	rows, err := cat.ChannelIndexPending(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Copied < 3 {
			continue
		}
		if err := idx.Vote(ctx, row.Ref, row.Tag, 1); err != nil {
			log.Warn("channel index vote", "ref", row.Ref, "err", err)
			continue
		}
		if err := cat.MarkChannelIndexVoted(ctx, row.Ref, row.Tag); err != nil {
			return err
		}
	}
	return nil
}
