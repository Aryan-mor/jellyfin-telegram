package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
)

// A 100 Mbit/s remux needs 12.5 MB/s, which is the bar for smooth 4K playback.
const targetMBps = 12.5

// bench measures sustained download speed. It stores nothing and bypasses the cache.
func bench(ctx context.Context, cfg config.Config, log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	mib := fs.Int("mib", 128, "how many MiB to download")
	bots := fs.Int("bots", 0, "how many bots to use, 0 means all")
	message := fs.Int("message", 0, "message id to read, 0 means the largest video")
	if err := fs.Parse(args); err != nil {
		return err
	}

	tokens := cfg.BotTokens
	if *bots > 0 && *bots < len(tokens) {
		tokens = tokens[:*bots]
	}
	pool, err := newPool(cfg, tokens, log)
	if err != nil {
		return err
	}
	pool.Start(ctx)
	wctx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	pool.WaitSettled(wctx)
	if pool.ReadyCount() == 0 {
		return errors.New("no bot can read the channel, run the check command")
	}

	files, _, err := scanChannel(ctx, pool)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return errors.New("no video found in the channel")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Size > files[j].Size })
	file := files[0]
	if *message != 0 {
		found := false
		for _, f := range files {
			if f.MessageID == *message {
				file, found = f, true
			}
		}
		if !found {
			return fmt.Errorf("message %d is not a video in the channel", *message)
		}
	}

	// Start a quarter into the file, where real playback spends its time.
	lastBlock := (file.Size - 1) / media.BlockSize
	start := lastBlock / 4
	count := min(int64(*mib), lastBlock-start+1)
	workers := pool.ReadyCount() * cfg.BotConcurrency

	fmt.Printf("Reading %d MiB of message %d (%.1f GiB) with %d bots, %d parallel reads\n",
		count, file.MessageID, float64(file.Size)/(1<<30), pool.ReadyCount(), workers)

	var (
		next      atomic.Int64
		bytes     atomic.Int64
		firstOnce sync.Once
		first     time.Duration
		firstErr  error
		errOnce   sync.Once
		wg        sync.WaitGroup
	)
	next.Store(start)
	began := time.Now()
	bctx, stop := context.WithCancel(ctx)
	defer stop()
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for bctx.Err() == nil {
				b := next.Add(1) - 1
				if b >= start+count {
					return
				}
				rctx, done := context.WithTimeout(bctx, fetchDeadline)
				data, err := pool.FetchBlock(rctx, file.MessageID, file.DocID, b, file.Size)
				done()
				if err != nil {
					errOnce.Do(func() { firstErr = err; stop() })
					return
				}
				firstOnce.Do(func() { first = time.Since(began) })
				bytes.Add(int64(len(data)))
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	elapsed := time.Since(began)
	mbps := float64(bytes.Load()) / 1e6 / elapsed.Seconds()
	fmt.Printf("\nDownloaded:   %.1f MiB in %s\n", float64(bytes.Load())/(1<<20), elapsed.Round(time.Millisecond))
	fmt.Printf("First block:  %s\n", first.Round(time.Millisecond))
	fmt.Printf("Speed:        %.1f MB/s (%.0f Mbit/s)\n", mbps, mbps*8)
	for _, s := range pool.Stats() {
		fmt.Printf("  bot %d: %d calls, %d flood waits\n", s.ID, s.Calls, s.Floods)
	}
	verdict := "is below"
	if mbps >= targetMBps {
		verdict = "meets"
	}
	fmt.Printf("\nThis %s the %.1f MB/s needed for a 100 Mbit/s remux.\n", verdict, targetMBps)
	return nil
}

const fetchDeadline = 5 * time.Minute
