package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/gotd/td/tg"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgpool"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgsetup"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// replaceBannedMirror creates a channel with the same bot and the same parts, and points the
// mirror at it. A public mirror gets a new public name. The library is copied to the new channel.
func replaceBannedMirror(ctx context.Context, cat *catalog.Catalog, pool *tgpool.Pool, users *tguser.Manager, m catalog.Mirror, log *slog.Logger) error {
	if users == nil {
		return errors.New("sign in a Telegram account before a banned mirror can be replaced")
	}
	title := m.Title
	if title == "" {
		title = "Library mirror"
	}
	val, err := users.Run(ctx, func(ctx context.Context, api *tg.Client) (any, error) {
		return tgsetup.CreateChannel(ctx, api, title, "Mirror of the library. Managed by jellyfin-telegram.")
	})
	if err != nil {
		return err
	}
	ch := val.(tgsetup.Created)
	saved := false
	defer func() {
		if saved {
			return
		}
		dctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = users.Run(dctx, func(ctx context.Context, api *tg.Client) (any, error) {
			return nil, tgsetup.DeleteChannel(ctx, api, ch.ID, ch.AccessHash)
		})
	}()

	username := ""
	if m.Username != "" {
		named, err := users.Run(ctx, func(ctx context.Context, api *tg.Client) (any, error) {
			return tgsetup.SetPublicUsername(ctx, api, ch.ID, ch.AccessHash, "", title)
		})
		if err != nil {
			return err
		}
		username, _ = named.(string)
	}
	_, _ = users.Run(ctx, func(ctx context.Context, api *tg.Client) (any, error) {
		return nil, tgsetup.MuteChannel(ctx, api, ch.ID, ch.AccessHash)
	})
	if err := promoteAllBots(ctx, users, pool, ch, nil); err != nil {
		return err
	}
	acceptBot, err := pool.ReadyBot()
	if err != nil {
		return err
	}
	got, titleGot, err := pool.AcceptMirror(ctx, acceptBot, ch.ID, username)
	if err != nil {
		return err
	}
	if err := cat.RetargetMirror(ctx, m.ID, got, titleGot, username); err != nil {
		return err
	}
	saved = true
	if log != nil {
		log.Info("banned mirror replaced", "old", m.ChannelID, "channel", got, "username", username)
	}
	return nil
}
