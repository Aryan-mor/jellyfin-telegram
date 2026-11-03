// Command jellyfin-telegram lets Jellyfin play video stored in a private Telegram channel.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgpool"
)

const usage = `Usage: jellyfin-telegram [-env FILE] <command> [options]

Commands:
  serve   Index the channel, write the library and serve streams (default)
  check   Verify the configuration, the bots and the channel, then exit
  bench   Measure download speed from the channel, then exit

Run "jellyfin-telegram bench -h" for bench options.
`

func main() {
	env := flag.String("env", ".env", "path to the settings file")
	verbose := flag.Bool("v", false, "log debug messages")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd, args := "serve", []string(nil)
	if flag.NArg() > 0 {
		cmd, args = flag.Arg(0), flag.Args()[1:]
	}

	cfg, err := config.Load(*env)
	// A first run has no settings at all. The setup page asks for them.
	if err != nil && !(cmd == "serve" && cfg.NeedsSetup()) {
		fmt.Fprintf(os.Stderr, "Configuration problems:\n%v\n", err)
		os.Exit(2)
	}

	switch cmd {
	case "serve":
		err = serve(ctx, cfg, *env, log)
	case "check":
		err = check(ctx, cfg, log)
	case "bench":
		err = bench(ctx, cfg, log, args)
	default:
		flag.Usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newPool(cfg config.Config, tokens []string, log *slog.Logger) (*tgpool.Pool, error) {
	return tgpool.New(tgpool.Options{
		APIID:       cfg.APIID,
		APIHash:     cfg.APIHash,
		ChannelID:   cfg.ChannelID,
		Tokens:      tokens,
		Concurrency: cfg.BotConcurrency,
		SessionDir:  filepath.Join(cfg.DataDir, "sessions"),
		Log:         log,
	})
}
