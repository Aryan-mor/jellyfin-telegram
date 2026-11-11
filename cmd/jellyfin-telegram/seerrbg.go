package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/front"
	"github.com/Aryan-mor/jellyfin-telegram/internal/jellyfin"
	"github.com/Aryan-mor/jellyfin-telegram/internal/managed"
	"github.com/Aryan-mor/jellyfin-telegram/internal/seerr"
	"github.com/Aryan-mor/jellyfin-telegram/internal/setup"
)

// seerrStartTimeout is how long Seerr gets to answer. Its first start reads tens of thousands of
// files that an antivirus scans one by one, which took three minutes on a normal Windows computer.
const seerrStartTimeout = 15 * time.Minute

// seerrProgress is what /seerr/ shows while Seerr is installing and starting.
type seerrProgress struct {
	mu    sync.Mutex
	state front.SeerrState
}

// State is the current progress.
func (p *seerrProgress) State() front.SeerrState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

func (p *seerrProgress) set(text string) {
	p.mu.Lock()
	p.state = front.SeerrState{Text: text}
	p.mu.Unlock()
}

func (p *seerrProgress) ready() {
	p.mu.Lock()
	p.state = front.SeerrState{Ready: true}
	p.mu.Unlock()
}

func (p *seerrProgress) fail(err error) {
	p.mu.Lock()
	p.state = front.SeerrState{Failed: true, Text: err.Error()}
	p.mu.Unlock()
}

// seerrSink sends the install progress of Seerr to its own page, not to the setup bar, which belongs
// to Jellyfin. Setup has usually finished by the time Seerr does.
type seerrSink struct {
	progress *seerrProgress
	wizard   *setup.Wizard
}

func (s seerrSink) SetStatus(st setup.Status) {
	text := st.Stage + "."
	if st.Detail != "" {
		text = st.Stage + ". " + st.Detail
	}
	s.progress.set(text)
}

func (s seerrSink) Logf(format string, args ...any) { s.wizard.Logf(format, args...) }

// jellyfinReady is what Seerr needs from Jellyfin once it is set up.
type jellyfinReady struct {
	cfg   config.Config
	creds managed.Credentials
}

// runSeerr brings Seerr up in the background. Its download, unpacking and first start do not need
// Jellyfin, so they run beside Jellyfin's, and only the last step, connecting the two, waits for it.
// A failure is shown on Seerr's own page and never stops the player.
func runSeerr(ctx context.Context, cfg config.Config, wizard *setup.Wizard, log *slog.Logger, progress *seerrProgress, child *sync.WaitGroup, jf <-chan jellyfinReady) {
	if err := bringUpSeerr(ctx, cfg, wizard, log, progress, child, jf); err != nil && ctx.Err() == nil {
		log.Warn("Seerr could not start", "err", err)
		wizard.Logf("Seerr could not start: %v", err)
		progress.fail(err)
	}
}

func bringUpSeerr(ctx context.Context, cfg config.Config, wizard *setup.Wizard, log *slog.Logger, progress *seerrProgress, child *sync.WaitGroup, jf <-chan jellyfinReady) error {
	root, err := filepath.Abs(filepath.Join(cfg.DataDir, "seerr"))
	if err != nil {
		return err
	}
	key, err := seerr.EnsureAPIKey(root)
	if err != nil {
		return err
	}
	progress.set("Preparing Seerr.")
	sink := seerrSink{progress: progress, wizard: wizard}
	// The bundle is a few hundred MB, so the download gets a client with a long timeout.
	bundle, err := seerr.Install(ctx, root, &http.Client{Timeout: 30 * time.Minute}, newInstallReporter(sink, 0, 50, 100).Progress, log)
	if err != nil {
		return err
	}
	srv := &seerr.Server{Root: root, APIKey: key, Bundle: bundle, Log: log}
	srv.Prepare()
	child.Add(1)
	go func() {
		defer child.Done()
		srv.Run(ctx)
	}()
	wizard.Logf("Starting Seerr")
	progress.set("Starting Seerr. The first start builds its database")
	if err := srv.WaitReady(ctx, seerrStartTimeout); err != nil {
		return err
	}
	wizard.Logf("Seerr is running")

	var j jellyfinReady
	select {
	case j = <-jf:
	case <-ctx.Done():
		return ctx.Err()
	}
	progress.set("Connecting Seerr to Jellyfin.")
	host, port, err := seerrJellyfin(j.cfg)
	if err != nil {
		return err
	}
	user, pass := j.creds.User, j.creds.Password
	if pass == "" {
		user, pass, err = seerr.LoginAccount(root)
		if err != nil {
			return err
		}
		if err := jellyfin.New(j.cfg.JellyfinURL, j.cfg.JellyfinAPIKey).EnsureAdmin(ctx, user, pass); err != nil {
			return fmt.Errorf("prepare the Jellyfin user for Seerr: %w", err)
		}
	}
	public := publicBase(j.cfg)
	setupSeerr := seerr.Setup{
		URL:               srv.URL(),
		APIKey:            key,
		JellyfinHost:      host,
		JellyfinPort:      port,
		JellyfinUser:      user,
		JellyfinPassword:  pass,
		JellyfinPublicURL: public,
		ApplicationURL:    strings.TrimRight(public, "/") + "/seerr",
		Language:          j.cfg.SubtitleLanguage,
	}
	// Seerr reaching Jellyfin can fail once at random right after it starts. That must not stop the player.
	if err := retry(ctx, 5, 5*time.Second, log, "Seerr setup", func() error { return setupSeerr.Run(ctx) }); err != nil {
		return err
	}
	wizard.Logf("Seerr is set up")
	progress.ready()
	return nil
}
