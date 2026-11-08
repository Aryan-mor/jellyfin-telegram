package main

import (
	"context"
	"crypto/rand"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
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
			"MIRROR_SETUP": "false", "MIRROR_TITLE": "", "MIRROR_USERNAME": "",
		}); err != nil {
			log.Warn("cannot clear the mirror request", "err", err)
		}
		_ = os.Setenv("MIRROR_SETUP", "false")
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
	botID, err := config.BotID(cfg.BotTokens[0])
	if err != nil {
		log.Warn("public mirror was not created", "err", err)
		clear()
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
	_, err = createMirror(mctx, cat, pool, users, envPath, cfg.ChannelID, title, true, username, []string{"movie", "series"}, false, botID, func(text, state string) {
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
