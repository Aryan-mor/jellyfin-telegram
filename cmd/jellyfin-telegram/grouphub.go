package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/Aryan-mor/jellyfin-telegram/internal/indexer"
	"github.com/Aryan-mor/jellyfin-telegram/internal/intake"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
	"github.com/Aryan-mor/jellyfin-telegram/internal/sign"
)

// groupHub holds the shared-library service. It can start after the program is already serving,
// so a problem fixed in Telegram needs no restart: the panel's "Check again" calls Retry.
type groupHub struct {
	root context.Context
	open func(ctx context.Context) (*intake.Service, error)
	log  *slog.Logger
	// enabled is the SIGNED_LIBRARY setting the program started with.
	enabled bool

	retry sync.Mutex // one Retry at a time, so the service cannot start twice
	mu    sync.RWMutex
	svc   *intake.Service
	err   string
	idx   *indexer.Indexer
}

// newGroupHub tries once. A failure is kept for the panel and the library runs as a plain channel.
func newGroupHub(ctx context.Context, log *slog.Logger, enabled bool, open func(context.Context) (*intake.Service, error)) *groupHub {
	h := &groupHub{root: ctx, open: open, log: log, enabled: enabled}
	svc, err := open(ctx)
	if err != nil {
		log.Error("shared library is off", "err", err)
		h.err = err.Error()
		return h
	}
	h.svc = svc
	return h
}

// Get returns the running service, or nil in channel mode.
func (h *groupHub) Get() *intake.Service {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.svc
}

// Err is why the last attempt failed. It is empty after a success.
func (h *groupHub) Err() string {
	if h == nil {
		return ""
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.err
}

// SetIndexer hands over the indexer, which is built after the hub, and applies a service that is
// already running.
func (h *groupHub) SetIndexer(idx *indexer.Indexer) {
	h.mu.Lock()
	h.idx = idx
	svc := h.svc
	h.mu.Unlock()
	if svc != nil {
		idx.SetSigned(indexer.Signed{Allow: svc.Check.Allow})
	}
}

// Retry sets the group up again and starts it. It returns a line for the panel.
func (h *groupHub) Retry(ctx context.Context) (string, error) {
	h.retry.Lock()
	defer h.retry.Unlock()
	if svc := h.Get(); svc != nil {
		return "Running.", nil
	}
	svc, err := h.open(ctx)
	if err != nil {
		h.mu.Lock()
		h.err = err.Error()
		h.mu.Unlock()
		return "", err
	}
	if svc == nil {
		return "", errors.New("the shared library is switched off in the settings. Turn it on and restart once, then check again")
	}
	h.mu.Lock()
	h.svc, h.err = svc, ""
	idx := h.idx
	h.mu.Unlock()
	go svc.Run(h.root)
	if idx != nil {
		idx.SetSigned(indexer.Signed{Allow: svc.Check.Allow})
	}
	h.log.Info("shared library started from the panel")
	return "Started.", nil
}

// lazyForum lets the subtitle worker use the service once it exists. Until then a file is claimed
// by nobody and nothing is signed, which is what channel mode does.
type lazyForum struct{ hub *groupHub }

func (l lazyForum) Claim(ctx context.Context, f media.File) (bool, error) {
	if svc := l.hub.Get(); svc != nil {
		return svc.Claim(ctx, f)
	}
	return true, nil
}

func (l lazyForum) Resign(ctx context.Context, f media.File, archiveHash string) error {
	if svc := l.hub.Get(); svc != nil {
		return svc.Resign(ctx, f, archiveHash)
	}
	return nil
}

func (l lazyForum) ArchiveCaption(ctx context.Context, f media.File, archiveHash string, tracks []sign.Track) string {
	if svc := l.hub.Get(); svc != nil {
		return svc.ArchiveCaption(ctx, f, archiveHash, tracks)
	}
	return ""
}

func (l lazyForum) FillIMDb(ctx context.Context, f media.File, imdb string) error {
	if svc := l.hub.Get(); svc != nil {
		return svc.FillIMDb(ctx, f, imdb)
	}
	return nil
}
