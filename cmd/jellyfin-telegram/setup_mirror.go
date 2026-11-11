package main

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/gotd/td/tg"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/mirror"
	"github.com/Aryan-mor/jellyfin-telegram/internal/setup"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgpool"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgsetup"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// setupMirror creates the public films-and-series channel requested on the setup page.
// It runs once: the request is cleared whether the channel is made or the attempt fails.
func setupMirror(ctx context.Context, cfg config.Config, envPath string, cat *catalog.Catalog, pool *tgpool.Pool, users *tguser.Manager, wizard *setup.Wizard, log *slog.Logger) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("MIRROR_SETUP")), "true") {
		return
	}
	clear := func() {
		if err := setup.UpdateEnv(envPath, map[string]string{
			"MIRROR_SETUP": "false", "MIRROR_TITLE": "", "MIRROR_USERNAME": "", "MIRROR_CHANNEL": "",
		}); err != nil {
			log.Warn("cannot clear the mirror request", "err", err)
		}
		_ = os.Setenv("MIRROR_SETUP", "false")
		_ = os.Setenv("MIRROR_CHANNEL", "")
	}
	list, err := cat.Mirrors(ctx)
	if err != nil {
		log.Warn("public mirror was not created", "err", err)
		clear()
		return
	}
	if len(list) > 0 {
		clear()
		return
	}
	if users == nil || pool == nil || len(cfg.BotTokens) == 0 {
		log.Warn("public mirror was not created", "err", "no signed-in account or bot")
		clear()
		return
	}
	if raw := strings.TrimSpace(os.Getenv("MIRROR_CHANNEL")); raw != "" {
		wizard.SetStatus(setup.Status{Stage: "Adding the public mirror", Percent: 99})
		mctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		err := attachExistingMirror(mctx, cat, pool, users, cfg.ChannelID, raw)
		clear()
		if err != nil {
			log.Warn("public mirror was not added", "err", err)
		}
		return
	}
	title := strings.TrimSpace(os.Getenv("MIRROR_TITLE"))
	if title == "" {
		title = fallbackMirrorTitle()
	}
	username := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(os.Getenv("MIRROR_USERNAME"))), "@")
	if !tgsetup.ValidChannelUsername(username) {
		username = ""
	}
	wizard.SetStatus(setup.Status{Stage: "Creating a public mirror", Percent: 99})
	mctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, err = createMirror(mctx, cat, pool, users, envPath, cfg.ChannelID, title, true, username, []string{"movie", "series"}, false, func(text, state string) {
		log.Info("setup mirror", "step", text, "state", state)
		if state != "bad" {
			wizard.SetStatus(setup.Status{Stage: text, Percent: 99})
		}
	})
	clear()
	if err != nil {
		log.Warn("public mirror was not created", "err", err)
	}
}

func attachExistingMirror(ctx context.Context, cat *catalog.Catalog, pool *tgpool.Pool, users *tguser.Manager, main int64, raw string) error {
	ctx = tguser.Prioritise(ctx)
	channelID, username, err := mirror.ParseTarget(raw)
	if err != nil {
		return err
	}
	if channelID != 0 && channelID == main {
		return errors.New("that is the main channel")
	}
	val, err := users.Run(ctx, func(ctx context.Context, api *tg.Client) (any, error) {
		ch, err := tguser.ResolveChannelInput(ctx, api, raw)
		if err != nil {
			return nil, err
		}
		if ch.ID == main {
			return nil, errors.New("that is the main channel")
		}
		return tgsetup.Created{ID: ch.ID, AccessHash: ch.AccessHash, Title: ch.Title}, nil
	})
	if err != nil {
		return err
	}
	if err := promoteAllBots(ctx, users, pool, val.(tgsetup.Created), nil); err != nil {
		return err
	}
	acceptBot, err := pool.ReadyBot()
	if err != nil {
		return err
	}
	id, title, err := pool.AcceptMirror(ctx, acceptBot, channelID, username)
	if err != nil {
		return err
	}
	if id == main {
		return errors.New("that is the main channel")
	}
	row, err := cat.AddMirror(ctx, id, 0, title, []string{"movie", "series"})
	if err != nil {
		return err
	}
	if username != "" {
		if err := cat.SetMirrorUsername(ctx, row.ID, username); err != nil {
			return err
		}
	}
	_ = muteMirror(ctx, users, id)
	return nil
}

func fallbackMirrorTitle() string {
	return "Films " + randomSuffix(6)
}

func randomSuffix(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "library"
	}
	for i, b := range raw {
		raw[i] = letters[int(b)%len(letters)]
	}
	return string(raw)
}
