// Package gateway serves channel files over HTTP with byte ranges.
package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/Aryan-mor/jellyfin-telegram/internal/blockstore"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
)

const (
	fetchTimeout = 2 * time.Minute
	// foregroundHold is how long background work waits after the last foreground read.
	foregroundHold = 20 * time.Second
)

// BlockFetcher reads one block from Telegram.
type BlockFetcher interface {
	FetchBlock(ctx context.Context, messageID int, docID, block, size int64) ([]byte, error)
}

// Engine combines the disk cache with Telegram reads. Concurrent readers of one block
// share a single fetch.
type Engine struct {
	store   *blockstore.Store
	fetcher BlockFetcher
	flight  singleflight.Group
	log     *slog.Logger

	mu      sync.Mutex
	probing map[int]*probeWindow

	lastForeground atomic.Int64
	pacers         sharedPacers
}

type probeWindow struct {
	refs   int
	blocks int
}

// NewEngine returns an Engine.
func NewEngine(store *blockstore.Store, fetcher BlockFetcher, log *slog.Logger) *Engine {
	return &Engine{store: store, fetcher: fetcher, log: log, probing: map[int]*probeWindow{}}
}

// noteForeground records that something other than background work is reading.
func (e *Engine) noteForeground() { e.lastForeground.Store(time.Now().UnixNano()) }

// busy reports whether anything other than background work has read recently.
func (e *Engine) busy() bool {
	return time.Since(time.Unix(0, e.lastForeground.Load())) < foregroundHold
}

// BeginProbe marks a message as being probed until the returned function is called.
// Blocks read in that window are pinned, because Jellyfin repeats the same reads on
// every playback. The returned function reports how many blocks were read, which proves
// whether the probe really reached this file.
func (e *Engine) BeginProbe(messageID int) (end func() int) {
	e.mu.Lock()
	w := e.probing[messageID]
	if w == nil {
		w = &probeWindow{}
		e.probing[messageID] = w
	}
	w.refs++
	e.mu.Unlock()
	return func() int {
		e.mu.Lock()
		defer e.mu.Unlock()
		if w.refs--; w.refs <= 0 {
			delete(e.probing, messageID)
		}
		return w.blocks
	}
}

func (e *Engine) isProbing(messageID int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.probing[messageID] != nil
}

// noteProbeRead counts a block read and reports whether it belongs to a probe.
func (e *Engine) noteProbeRead(messageID int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	w := e.probing[messageID]
	if w == nil {
		return false
	}
	w.blocks++
	return true
}

// Block returns one block from the cache or from Telegram.
func (e *Engine) Block(ctx context.Context, f media.File, block int64) ([]byte, error) {
	pin := e.noteProbeRead(f.MessageID)
	data, ok := e.store.Get(f.DocID, block)
	if !ok {
		var err error
		data, err = e.fetch(ctx, f, block)
		if err != nil {
			return nil, err
		}
	}
	if pin {
		if err := e.store.Pin(f.DocID, block); err != nil {
			e.log.Warn("cannot pin block", "message", f.MessageID, "block", block, "err", err)
		}
	}
	return data, nil
}

// fetch detaches the Telegram call from ctx, so one reader leaving does not fail
// another reader waiting on the same block.
func (e *Engine) fetch(ctx context.Context, f media.File, block int64) ([]byte, error) {
	ch := e.flight.DoChan(fmt.Sprintf("%d/%d", f.DocID, block), func() (any, error) {
		fctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		data, err := e.fetcher.FetchBlock(fctx, f.MessageID, f.DocID, block, f.Size)
		if err != nil {
			return nil, err
		}
		if err := e.store.Put(f.DocID, block, data); err != nil {
			e.log.Warn("cannot cache block", "message", f.MessageID, "block", block, "err", err)
		}
		return data, nil
	})
	select {
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		return res.Val.([]byte), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// prefetch starts a background read of a block that is not cached yet.
func (e *Engine) prefetch(ctx context.Context, f media.File, block int64) {
	if ctx.Err() != nil || e.store.Has(f.DocID, block) {
		return
	}
	go func() {
		if _, err := e.fetch(ctx, f, block); err != nil && ctx.Err() == nil {
			e.log.Debug("prefetch failed", "message", f.MessageID, "block", block, "err", err)
		}
	}()
}
