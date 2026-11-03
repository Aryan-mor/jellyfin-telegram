package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/blockstore"
	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/gateway"
	"github.com/Aryan-mor/jellyfin-telegram/internal/indexer"
	"github.com/Aryan-mor/jellyfin-telegram/internal/jellyfin"
	"github.com/Aryan-mor/jellyfin-telegram/internal/lang"
	"github.com/Aryan-mor/jellyfin-telegram/internal/library"
	"github.com/Aryan-mor/jellyfin-telegram/internal/subarchive"
	"github.com/Aryan-mor/jellyfin-telegram/internal/subtitles"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgpool"
)

const (
	reconcileEvery = time.Hour
	startupTimeout = 2 * time.Minute
	archiveIdle    = time.Minute
)

func serve(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	if err := cfg.ValidateServe(); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	token, err := cfg.EnsureGatewayToken()
	if err != nil {
		return fmt.Errorf("gateway token: %w", err)
	}

	cat, err := catalog.Open(filepath.Join(cfg.DataDir, "catalog.db"))
	if err != nil {
		return fmt.Errorf("open catalog: %w", err)
	}
	defer cat.Close()
	if err := checkChannelUnchanged(ctx, cat, cfg.ChannelID); err != nil {
		return err
	}

	store, err := blockstore.Open(filepath.Join(cfg.DataDir, "cache"), cfg.CacheBytes)
	if err != nil {
		return fmt.Errorf("open cache: %w", err)
	}

	lib := library.New(cfg.LibraryDir, cfg.GatewayPublicURL, token, cfg.ChannelID)
	if err := lib.Prepare(); err != nil {
		return fmt.Errorf("prepare library: %w", err)
	}

	pool, err := newPool(cfg, cfg.BotTokens, log)
	if err != nil {
		return err
	}
	pool.Start(ctx)
	if err := waitForBots(ctx, pool, log); err != nil {
		return err
	}

	engine := gateway.NewEngine(store, pool, log)
	srv := &http.Server{
		Addr: cfg.GatewayListen,
		Handler: gateway.Handler(engine, cat, gateway.Options{
			Token:     token,
			ChannelID: cfg.ChannelID,
			Readahead: cfg.ReadaheadBlocks,

			BackgroundBytesPerSecond: cfg.ExtractBytesPerSecond,
		}, log),
		ReadHeaderTimeout: 15 * time.Second,
	}
	srvErr := make(chan error, 1)
	go func() { srvErr <- srv.ListenAndServe() }()
	log.Info("gateway listening", "addr", cfg.GatewayListen, "public_url", cfg.GatewayPublicURL)

	var prober *jellyfin.Prober
	if cfg.JellyfinEnabled() {
		prober = jellyfin.NewProber(jellyfin.New(cfg.JellyfinURL, cfg.JellyfinAPIKey), cat, engine, log)
		prober.StreamURL = lib.StreamURL
		if err := setupSubtitles(ctx, cfg, cat, pool, prober, log); err != nil {
			return err
		}
	} else {
		log.Warn("JELLYFIN_URL is not set, so new files are not probed automatically")
	}

	idx := indexer.New(pool, cat, lib, store, log)
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		syncLoop(ctx, cfg, idx, prober, pool, cat, log)
	}()
	archiveDone := make(chan struct{})
	go func() {
		defer close(archiveDone)
		if prober != nil {
			archiveLoop(ctx, cfg, cat, prober, pool, lib, log)
		}
	}()

	select {
	case err := <-srvErr:
		return fmt.Errorf("gateway stopped: %w", err)
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	<-loopDone
	<-archiveDone
	return nil
}

func setupSubtitles(ctx context.Context, cfg config.Config, cat *catalog.Catalog, pool *tgpool.Pool, prober *jellyfin.Prober, log *slog.Logger) error {
	if cfg.SubtitleLanguage == "" {
		log.Info("SUBTITLE_LANGUAGE is not set, so subtitles are not handled")
		return nil
	}
	tools, err := subtitles.FindTools(cfg.FFprobePath)
	if err != nil {
		log.Warn("subtitle checks are off", "err", err)
		return nil
	}
	changed, err := cat.SwitchSubtitleLanguage(ctx, cfg.SubtitleLanguage)
	if err != nil {
		return fmt.Errorf("subtitle language: %w", err)
	}
	if changed {
		log.Info("subtitle language changed, checking every file again", "language", cfg.SubtitleLanguage)
	}
	resolver := &subtitles.Resolver{Tools: tools, Language: cfg.SubtitleLanguage, Local: &subarchive.ChannelSubs{
		Catalog: cat, Channel: pool, Language: cfg.SubtitleLanguage,
	}}
	if cfg.SubtitlesOnline {
		if provider, ok := subtitles.NewSubPlus(cfg.SubtitleLanguage); ok {
			resolver.Provider = provider
		} else {
			log.Warn("sub-plus.ir does not offer this language, so online subtitles are off", "language", cfg.SubtitleLanguage)
		}
	}
	prober.Subtitles = resolver
	prober.SubtitleLanguage = lang.ISO3(cfg.SubtitleLanguage)
	return nil
}

// archiveLoop keeps the embedded subtitles of every video in the channel, one video at a time.
func archiveLoop(ctx context.Context, cfg config.Config, cat *catalog.Catalog, prober *jellyfin.Prober, pool *tgpool.Pool, lib *library.Library, log *slog.Logger) {
	if cfg.ExtractBytesPerSecond == 0 {
		log.Info("subtitle extraction is off")
		return
	}
	tools, err := subtitles.FindTools(cfg.FFprobePath)
	if err != nil {
		log.Warn("subtitle extraction is off", "err", err)
		return
	}
	client := jellyfin.New(cfg.JellyfinURL, cfg.JellyfinAPIKey)
	dataDir := cfg.JellyfinDataDir
	if dataDir == "" {
		if dataDir, err = client.ProgramDataPath(ctx); err != nil {
			log.Warn("subtitle extraction is off, cannot ask Jellyfin for its data folder", "err", err)
			return
		}
	}
	root := filepath.Join(dataDir, "data", "subtitles")
	if _, err := os.Stat(filepath.Dir(root)); err != nil {
		log.Warn("subtitle extraction is off, set JELLYFIN_DATA_DIR to Jellyfin's data folder", "path", dataDir)
		return
	}
	w := &subarchive.Worker{
		Catalog: cat, Client: client, Prober: prober, Channel: pool, Extractor: tools,
		Resolver: prober.Replacements(), SubtitlesRoot: root, StreamURL: lib.StreamURL, Log: log,
	}
	if err := cat.ResetExtractTries(ctx); err != nil {
		log.Warn("cannot reset subtitle extraction tries", "err", err)
	}
	verify := time.NewTicker(reconcileEvery)
	defer verify.Stop()
	idle := time.NewTicker(archiveIdle)
	defer idle.Stop()
	runVerify := func() {
		if err := w.Verify(ctx); err != nil && ctx.Err() == nil {
			log.Warn("subtitle cache check failed", "err", err)
		}
	}
	runVerify()
	for {
		for {
			did, err := w.RunOnce(ctx)
			if err != nil && ctx.Err() == nil {
				log.Warn("subtitle archive step failed", "err", err)
			}
			if !did || err != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-verify.C:
			runVerify()
		case <-idle.C:
		}
	}
}

func syncLoop(ctx context.Context, cfg config.Config, idx *indexer.Indexer, prober *jellyfin.Prober, pool *tgpool.Pool, cat *catalog.Catalog, log *slog.Logger) {
	probe := func(refresh bool) {
		if prober == nil {
			return
		}
		if err := prober.Run(ctx, refresh); err != nil && ctx.Err() == nil {
			log.Warn("probing stopped early", "err", err)
		}
	}
	reconcile := func() {
		removed, err := idx.Reconcile(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Warn("reconcile failed", "err", err)
			}
			return
		}
		if removed > 0 {
			log.Info("reconcile finished", "removed", removed)
		}
	}

	if err := idx.Rewrite(ctx); err != nil {
		log.Error("cannot refresh .strm files", "err", err)
	}
	added, err := idx.Sync(ctx, indexer.FullWindow)
	if err != nil && ctx.Err() == nil {
		log.Warn("initial sync failed", "err", err)
	}
	log.Info("initial sync finished", "added", added)
	reconcile()
	warmUp(ctx, cat, pool, log)
	probe(true)

	syncTick := time.NewTicker(cfg.SyncInterval)
	defer syncTick.Stop()
	reconcileTick := time.NewTicker(reconcileEvery)
	defer reconcileTick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-syncTick.C:
			added, err := idx.Sync(ctx, indexer.IncrementalWindow)
			if err != nil && ctx.Err() == nil {
				log.Warn("sync failed", "err", err)
			}
			if added > 0 {
				probe(true)
			}
		case <-reconcileTick.C:
			if err := idx.Rewrite(ctx); err != nil {
				log.Error("cannot refresh .strm files", "err", err)
			}
			reconcile()
			probe(true)
		}
	}
}

// warmUp opens every bot's connection to the data center of the first file.
func warmUp(ctx context.Context, cat *catalog.Catalog, pool *tgpool.Pool, log *slog.Logger) {
	all, err := cat.All(ctx)
	if err != nil || len(all) == 0 {
		return
	}
	began := time.Now()
	wctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	pool.Warm(wctx, all[0].MessageID, all[0].DocID)
	log.Info("bots warmed up", "took", time.Since(began).Round(time.Millisecond))
}

func waitForBots(ctx context.Context, pool *tgpool.Pool, log *slog.Logger) error {
	wctx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	pool.WaitSettled(wctx)
	var failures []error
	for _, st := range pool.Statuses() {
		if !st.Ready {
			failures = append(failures, fmt.Errorf("bot %d: %v", st.ID, st.Err))
		}
	}
	if pool.ReadyCount() == 0 {
		return fmt.Errorf("no bot could connect:\n%w", errors.Join(failures...))
	}
	for _, f := range failures {
		log.Warn("bot is not ready", "err", f)
	}
	log.Info("bots ready", "ready", pool.ReadyCount(), "failed", len(failures))
	return nil
}

// checkChannelUnchanged stops a changed channel id from serving another channel's
// message ids through existing .strm files.
func checkChannelUnchanged(ctx context.Context, cat *catalog.Catalog, channel int64) error {
	all, err := cat.All(ctx)
	if err != nil {
		return err
	}
	if len(all) > 0 && all[0].ChannelID != channel {
		return fmt.Errorf("the catalog belongs to channel %d, not %d: use a new DATA_DIR and LIBRARY_DIR for a different channel",
			all[0].ChannelID, channel)
	}
	return nil
}
