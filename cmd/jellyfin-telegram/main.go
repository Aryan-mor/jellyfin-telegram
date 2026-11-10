// Command jellyfin-telegram lets Jellyfin play video stored in a private Telegram channel.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
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
	log := slog.New(slog.NewTextHandler(logOutput(), &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd, args := "serve", []string(nil)
	if flag.NArg() > 0 {
		cmd, args = flag.Arg(0), flag.Args()[1:]
	}

	// Read the restart flag before waitForPredecessor clears it. A panel restart
	// inherits the old process environment, including TELEGRAM_BOT_TOKENS from the
	// previous start. Reload lets the settings file win. A normal start still lets
	// a real environment win.
	load := settingsLoader()
	waitForPredecessor()

	cfg, err := load(*env)
	// A first run has no settings at all. The setup page asks for them.
	if err != nil && !(cmd == "serve" && cfg.NeedsSetup()) {
		fmt.Fprintf(os.Stderr, "Configuration problems:\n%v\n", err)
		os.Exit(2)
	}
	// First setup stays in this window so its log and progress stay visible. A server that is
	// already set up does not need the window.
	if cmd == "serve" && !cfg.NeedsSetup() {
		backgroundIfInteractive()
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

// settingsLoader must run before waitForPredecessor. That wait removes
// JFTG_RESTART_WAIT, and checking the variable afterwards always misses a panel restart.
func settingsLoader() func(string) (config.Config, error) {
	if os.Getenv(restartWaitEnv) != "" {
		return config.Reload
	}
	return config.Load
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

// logOutput writes the log to the terminal and to data/jellyfin-telegram.log, so a user who started
// the program from a shortcut or a service can still send the last lines. The file starts over once
// it passes 5 MB. If it cannot be opened, only the terminal is used.
func logOutput() io.Writer {
	dir := os.Getenv("DATA_DIR")
	if dir == "" {
		dir = "data"
	}
	path := filepath.Join(dir, "jellyfin-telegram.log")
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if st, err := os.Stat(path); err == nil && st.Size() > 5<<20 {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return os.Stderr
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return os.Stderr
	}
	// The terminal goes away once serve leaves the console. A failed stderr write must not drop
	// the line that was going to the file.
	return io.MultiWriter(stderrOrNothing{}, f)
}

// stderrOrNothing writes to the terminal and reports success even when there is no terminal.
type stderrOrNothing struct{}

func (stderrOrNothing) Write(p []byte) (int, error) {
	_, _ = os.Stderr.Write(p)
	return len(p), nil
}
