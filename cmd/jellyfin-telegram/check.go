package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/jellyfin"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
	"github.com/Aryan-mor/jellyfin-telegram/internal/subtitles"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgpool"
)

const (
	scanBatch  = 100
	scanWindow = 2000
)

// check verifies the setup and changes nothing in the channel or the library.
func check(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	pool, err := newPool(cfg, cfg.BotTokens, log)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	pool.Start(cctx)
	pool.WaitSettled(cctx)

	problems := 0
	fmt.Printf("Bots (%d configured):\n", len(cfg.BotTokens))
	for _, st := range pool.Statuses() {
		switch {
		case !st.Ready:
			problems++
			fmt.Printf("  FAIL  %d @%s: %v\n", st.ID, st.Username, st.Err)
		case !st.IsAdmin:
			problems++
			fmt.Printf("  FAIL  %d @%s: sees %q but is not an admin\n", st.ID, st.Username, st.ChannelTitle)
		default:
			fmt.Printf("  OK    %d @%s: admin of %q\n", st.ID, st.Username, st.ChannelTitle)
		}
	}
	if pool.ReadyCount() == 0 {
		return errors.New("no bot can read the channel")
	}

	files, last, err := scanChannel(cctx, pool)
	if err != nil {
		return fmt.Errorf("read channel: %w", err)
	}
	var total int64
	for _, f := range files {
		total += f.Size
	}
	fmt.Printf("\nChannel: %d videos, %.1f GiB, last message id %d\n", len(files), float64(total)/(1<<30), last)
	if len(files) == 0 {
		problems++
		fmt.Println("  FAIL  no video found in the channel")
	}

	fmt.Println("\nGateway:")
	switch {
	case cfg.GatewayPublicURL == "":
		problems++
		fmt.Println("  FAIL  GATEWAY_PUBLIC_URL is empty")
	default:
		u, _ := url.Parse(cfg.GatewayPublicURL)
		fmt.Printf("  OK    public URL host is %s\n", u.Host)
		if h := u.Hostname(); h == "127.0.0.1" || h == "localhost" {
			fmt.Println("  WARN  only clients on this machine can reach a loopback URL")
		}
	}

	fmt.Println("\nJellyfin:")
	if !cfg.JellyfinEnabled() {
		fmt.Println("  SKIP  not configured, so new files are not probed automatically")
	} else {
		client := jellyfin.New(cfg.JellyfinURL, cfg.JellyfinAPIKey)
		version, err := client.Version(cctx)
		if err != nil {
			problems++
			fmt.Printf("  FAIL  %v\n", err)
		} else {
			fmt.Printf("  OK    server version %s\n", version)
		}
	}

	fmt.Println("\nSubtitles:")
	switch {
	case cfg.SubtitleLanguage == "":
		fmt.Println("  SKIP  SUBTITLE_LANGUAGE is not set")
	case !cfg.JellyfinEnabled():
		fmt.Println("  SKIP  needs the Jellyfin settings")
	default:
		fmt.Printf("  OK    language is %s\n", cfg.SubtitleLanguage)
		if _, err := subtitles.FindTools(cfg.FFprobePath); err != nil {
			problems++
			fmt.Printf("  FAIL  %v\n", err)
		}
		if cfg.SubtitlesOnline {
			if _, ok := subtitles.NewSubPlus(cfg.SubtitleLanguage); !ok {
				fmt.Println("  WARN  sub-plus.ir does not offer this language, so online subtitles are off")
			}
		}
	}

	if problems > 0 {
		return fmt.Errorf("%d problem(s) found", problems)
	}
	fmt.Println("\nAll checks passed.")
	return nil
}

// scanChannel lists every video by walking message ids from the start.
func scanChannel(ctx context.Context, pool *tgpool.Pool) ([]media.File, int, error) {
	var files []media.File
	last, empty := 0, 0
	for next := 1; empty < scanWindow; next += scanBatch {
		ids := make([]int, scanBatch)
		for i := range ids {
			ids[i] = next + i
		}
		msgs, err := pool.Messages(ctx, ids)
		if err != nil {
			return nil, 0, err
		}
		seen := false
		for _, m := range msgs {
			if !m.Exists {
				continue
			}
			seen = true
			last = max(last, m.ID)
			if m.File != nil {
				files = append(files, *m.File)
			}
		}
		if seen {
			empty = 0
		} else {
			empty += scanBatch
		}
	}
	return files, last, nil
}
