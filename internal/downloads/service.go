// Package downloads ties user sessions, the Docker stack and the uploader together.
// Turning it on must not be able to stop playback: Run logs errors and keeps going.
package downloads

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/autoupload"
	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/diskspace"
	"github.com/Aryan-mor/jellyfin-telegram/internal/setup"
	"github.com/Aryan-mor/jellyfin-telegram/internal/stack"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// Service is the panel's view of automatic downloads.
type Service struct {
	EnvPath  string
	Users    *tguser.Manager
	Runner   *stack.Runner
	Worker   *autoupload.Worker
	Cat      *catalog.Catalog
	Defaults tguser.Pace
	MinFree  int
	Log      *slog.Logger

	mu     sync.Mutex
	on     bool
	cancel context.CancelFunc
	wake   chan struct{}
	// run is held for a whole pass, so a switch-off cannot race a pass that recreates containers.
	run sync.Mutex
}

// New prepares the service. on is the value loaded from the settings file.
func New(on bool) *Service {
	return &Service{on: on, wake: make(chan struct{}, 1)}
}

// Run reconciles the stack and uploads until ctx ends.
func (s *Service) Run(ctx context.Context) {
	for {
		s.mu.Lock()
		on := s.on
		s.mu.Unlock()
		if on {
			s.tick(ctx)
		}
		select {
		case <-ctx.Done():
			s.shutdown()
			return
		case <-time.After(time.Minute):
		case <-s.wake:
		}
	}
}

// shutdown removes the containers when the program ends. Without it qBittorrent would keep
// downloading with nothing watching the free space.
func (s *Service) shutdown() {
	if s.Runner == nil || !s.Enabled() {
		return
	}
	s.run.Lock()
	defer s.run.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.Runner.Stop(ctx)
}

func (s *Service) tick(ctx context.Context) {
	if s.Runner == nil {
		return
	}
	// Turning the switch off waits for a pass that is still running, and cancels it.
	s.run.Lock()
	defer s.run.Unlock()
	tctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.mu.Lock()
	if !s.on {
		s.mu.Unlock()
		return
	}
	s.cancel = cancel
	s.mu.Unlock()
	ctx = tctx

	if s.Users != nil {
		s.Runner.SetCapMB(float64(s.Users.LargestCap()) / (1 << 20))
	}
	reconcileErr := s.Runner.Reconcile(ctx)
	if s.Worker == nil {
		return
	}
	snap := s.Runner.Snapshot()
	if snap.QbitPassword == "" {
		return
	}
	lib := s.library(snap)
	if q := lib.Qbit; q != nil {
		if err := q.Login(ctx); err != nil && s.Log != nil && ctx.Err() == nil {
			s.Log.Warn("qBittorrent", "err", err)
		}
	}
	s.Worker.Lib = lib
	if reconcileErr != nil {
		// The apps may be half set up, but qBittorrent keeps downloading, so the disk guard still runs.
		s.Worker.Guard(ctx)
		return
	}
	if err := s.Worker.RunOnce(ctx); err != nil && s.Log != nil && ctx.Err() == nil {
		s.Log.Warn("uploader", "err", err)
	}
}

func (s *Service) library(snap stack.Status) *autoupload.Source {
	return &autoupload.Source{
		Downloads:  s.Runner.Downloads,
		RadarrBase: "http://127.0.0.1:" + strconv.Itoa(stack.RadarrPort) + "/radarr",
		RadarrKey:  snap.RadarrAPIKey,
		SonarrBase: "http://127.0.0.1:" + strconv.Itoa(stack.SonarrPort) + "/sonarr",
		SonarrKey:  snap.SonarrAPIKey,
		SeerrBase:  s.Runner.SeerrURL,
		SeerrKey:   s.Runner.SeerrKey,
		Qbit: &stack.Qbit{
			Base: "http://127.0.0.1:" + strconv.Itoa(stack.QbitPort),
			User: "admin", Pass: snap.QbitPassword,
		},
	}
}

// SetEnabled persists the switch. On requires a healthy admin session, Docker and a writable folder.
func (s *Service) SetEnabled(ctx context.Context, on bool) error {
	if on {
		if s.Users == nil || len(s.Users.Healthy()) == 0 {
			return fmt.Errorf("sign in at least one account that is an admin of the channel")
		}
		if err := stack.Available(); err != nil {
			return err
		}
		if s.Runner == nil {
			return fmt.Errorf("the download folder is not configured")
		}
		if err := stack.PrepareDir(s.Runner.Downloads); err != nil {
			return err
		}
		free, err := diskspace.Free(s.Runner.Downloads)
		if err != nil {
			return err
		}
		if s.MinFree > 0 && free < uint64(s.MinFree)<<30 {
			return fmt.Errorf("the download folder has %.1f GiB free, and %d GiB is the minimum", float64(free)/(1<<30), s.MinFree)
		}
	}
	if s.EnvPath != "" {
		if err := setup.UpdateEnv(s.EnvPath, map[string]string{"DOWNLOADS_ENABLED": strconv.FormatBool(on)}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.on = on
	cancel := s.cancel
	s.mu.Unlock()
	if !on {
		if cancel != nil {
			cancel()
		}
		s.run.Lock()
		if s.Runner != nil {
			_ = s.Runner.ForgetSeerr(ctx)
			s.Runner.Stop(ctx)
		}
		s.run.Unlock()
	}
	s.kick()
	return nil
}

// SetDefaults saves the pace used by sessions that do not override it.
func (s *Service) SetDefaults(p tguser.Pace) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if s.Users != nil {
		s.Users.SetDefaults(p)
	}
	s.mu.Lock()
	s.Defaults = p
	s.mu.Unlock()
	if s.EnvPath == "" {
		return nil
	}
	return setup.UpdateEnv(s.EnvPath, map[string]string{
		"UPLOAD_MB_PER_SEC":         strconv.Itoa(p.MBPerSec),
		"UPLOAD_REST_MINUTES":       strconv.Itoa(p.RestMinutes),
		"UPLOAD_LONG_REST_EVERY_GB": strconv.Itoa(p.LongEveryGB),
		"UPLOAD_LONG_REST_MINUTES":  strconv.Itoa(p.LongRestMinutes),
		"UPLOAD_DAILY_GB":           strconv.Itoa(p.DailyGB),
		"UPLOAD_HOURS":              p.Hours,
	})
}

func (s *Service) kick() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Enabled reports the switch.
func (s *Service) Enabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.on
}

// Status is the JSON the panel embeds.
func (s *Service) Status(ctx context.Context) (map[string]any, error) {
	var sessions []tguser.View
	if s.Users != nil {
		sessions = s.Users.Views()
	}
	var queue []map[string]any
	if s.Cat != nil {
		rows, err := s.Cat.ListUploads(ctx)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row.State == catalog.UploadCleaned {
				continue
			}
			queue = append(queue, map[string]any{
				"name": row.FileName, "state": row.State, "error": row.Error, "size": row.Size,
			})
		}
	}
	snap := stack.Status{}
	dir := ""
	if s.Runner != nil {
		snap = s.Runner.Snapshot()
		dir = s.Runner.Downloads
		if free, err := diskspace.Free(dir); err == nil {
			snap.FreeBytes = free
		}
	}
	s.mu.Lock()
	pace := s.Defaults
	on := s.on
	s.mu.Unlock()
	if sessions == nil {
		sessions = []tguser.View{}
	}
	if queue == nil {
		queue = []map[string]any{}
	}
	return map[string]any{
		"enabled": on, "error": snap.Error, "ready": snap.Ready, "dir": dir,
		"free_bytes": snap.FreeBytes, "min_free_gb": s.MinFree, "crawler": snap.Crawler,
		"radarr_user": snap.RadarrUser, "radarr_password": snap.RadarrPassword,
		"sonarr_user": snap.SonarrUser, "sonarr_password": snap.SonarrPassword,
		"containers": snap.Containers, "sessions": sessions, "queue": queue,
		"pace": pace,
	}, nil
}
