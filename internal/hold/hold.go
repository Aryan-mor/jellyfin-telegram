// Package hold is the switch that stops automatic Telegram work.
// Playback, Jellyfin, Seerr and a button a person presses keep working.
// A paused switch is remembered in the data folder, so a restart stays paused.
package hold

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const fileName = "automation.off"

type job struct {
	id     int
	cancel context.CancelFunc
}

// Gate is one process-wide pause switch.
type Gate struct {
	mu      sync.Mutex
	path    string
	paused  bool
	next    int
	changed chan struct{}
	jobs    []job
}

// Open reads the switch. A file named automation.off means it is paused.
func Open(dataDir string) (*Gate, error) {
	path := filepath.Join(dataDir, fileName)
	paused := false
	if _, err := os.Stat(path); err == nil {
		paused = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return &Gate{path: path, paused: paused, changed: make(chan struct{})}, nil
}

// Paused reports the switch.
func (g *Gate) Paused() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.paused
}

// Set pauses or resumes and wakes every waiter. Pausing cancels automatic work already running.
func (g *Gate) Set(paused bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.paused == paused {
		return nil
	}
	if paused {
		if err := os.WriteFile(g.path, []byte("1\n"), 0o600); err != nil {
			return err
		}
	} else if err := os.Remove(g.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	g.paused = paused
	if paused {
		for _, job := range g.jobs {
			job.cancel()
		}
		g.jobs = nil
	}
	close(g.changed)
	g.changed = make(chan struct{})
	slog.Info("automatic work", "paused", paused)
	return nil
}

// Wait blocks while the switch is paused.
func (g *Gate) Wait(ctx context.Context) error {
	for {
		g.mu.Lock()
		if !g.paused {
			g.mu.Unlock()
			return nil
		}
		ch := g.changed
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
		}
	}
}

// Begin starts one automatic pass. The context ends when the switch is paused or ctx ends.
func (g *Gate) Begin(ctx context.Context) (context.Context, func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	child, cancel := context.WithCancel(ctx)
	if g.paused {
		cancel()
		return child, func() {}
	}
	g.next++
	id := g.next
	g.jobs = append(g.jobs, job{id: id, cancel: cancel})
	return child, func() {
		cancel()
		g.mu.Lock()
		defer g.mu.Unlock()
		for i, job := range g.jobs {
			if job.id == id {
				g.jobs = append(g.jobs[:i], g.jobs[i+1:]...)
				return
			}
		}
	}
}

var active atomic.Pointer[Gate]

// Use installs the switch the running process checks. Nil clears it.
func Use(g *Gate) { active.Store(g) }

// Stopped reports whether automatic work must not start. No switch means it runs.
func Stopped() bool {
	g := active.Load()
	return g != nil && g.Paused()
}

// Set pauses or resumes the installed switch.
func Set(paused bool) error {
	g := active.Load()
	if g == nil {
		return errors.New("pause is not available")
	}
	return g.Set(paused)
}

// Wait blocks while the installed switch is paused.
func Wait(ctx context.Context) error {
	g := active.Load()
	if g == nil {
		return nil
	}
	return g.Wait(ctx)
}

// Begin starts one automatic pass on the installed switch.
// With no switch, ctx is returned unchanged.
func Begin(ctx context.Context) (context.Context, func()) {
	g := active.Load()
	if g == nil {
		return ctx, func() {}
	}
	return g.Begin(ctx)
}

// Changed is closed when the switch is paused or resumed. Nil when no switch is installed.
func Changed() <-chan struct{} {
	g := active.Load()
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.changed
}

// Sleep waits for d, for ctx to end, for ch, or for the switch to change.
// woken is true only when ch received. ok is false when ctx ended.
func Sleep(ctx context.Context, d time.Duration, ch <-chan struct{}) (ok, woken bool) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false, false
	case <-timer.C:
		return true, false
	case <-ch:
		return true, true
	case <-Changed():
		return true, false
	}
}
