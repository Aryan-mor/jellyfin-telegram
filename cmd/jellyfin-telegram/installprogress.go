package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/managed"
	"github.com/Aryan-mor/jellyfin-telegram/internal/setup"
)

// installReporter turns the progress of a download and its unpacking into what the setup page
// shows: a bar that moves, the bytes and speed, and a line in the log at each quarter. Unpacking
// is reported on its own because it is the longest quiet wait on a slow disk.
type installReporter struct {
	wizard *setup.Wizard
	// The bar runs from downloadFrom to downloadTo while downloading, then on to unpackTo.
	downloadFrom, downloadTo, unpackTo int

	now func() time.Time

	mu      sync.Mutex
	started map[string]time.Time
	lastSet time.Time
	best    int
	quarter map[string]int
}

func newInstallReporter(w *setup.Wizard, downloadFrom, downloadTo, unpackTo int) *installReporter {
	return &installReporter{
		wizard: w, downloadFrom: downloadFrom, downloadTo: downloadTo, unpackTo: unpackTo,
		now: time.Now, started: map[string]time.Time{}, quarter: map[string]int{},
	}
}

// Progress is the callback to hand to managed.Install.
func (r *installReporter) Progress(name string, done, total int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	unpack := strings.HasPrefix(name, managed.UnpackPrefix)
	label := strings.TrimPrefix(name, managed.UnpackPrefix)
	verb, from, to := "Downloading", r.downloadFrom, r.downloadTo
	if unpack {
		verb, from, to = "Unpacking", r.downloadTo, r.unpackTo
	}
	now := r.now()
	if _, ok := r.started[name]; !ok {
		r.started[name] = now
		r.quarter[name] = -1
		if !unpack && total > 0 {
			r.wizard.Logf("%s %s (%s)", verb, label, humanBytes(total))
		} else {
			r.wizard.Logf("%s %s", verb, label)
		}
	}
	finished := total > 0 && done >= total
	known := total > 0
	fraction := 0.0
	if known {
		fraction = float64(done) / float64(total)
		if fraction > 1 {
			fraction = 1
		}
	}
	percent := from
	if known {
		percent = from + int(float64(to-from)*fraction)
	}
	if percent < r.best {
		percent = r.best
	}
	r.best = percent

	if q := int(fraction * 4); known && q > r.quarter[name] && !finished {
		r.quarter[name] = q
		if q > 0 {
			r.wizard.Logf("%s %s: %d%%", verb, label, q*25)
		}
	}
	if finished {
		r.wizard.Logf("%s %s: done", verb, label)
	}

	// A status for every chunk would flood the page. Twice a second keeps the bar smooth.
	if !finished && now.Sub(r.lastSet) < 200*time.Millisecond {
		return
	}
	r.lastSet = now
	r.wizard.SetStatus(setup.Status{
		Stage:   verb + " " + label,
		Percent: percent,
		Detail:  r.detail(unpack, done, total, now.Sub(r.started[name])),
	})
}

func (r *installReporter) detail(unpack bool, done, total int64, elapsed time.Duration) string {
	if unpack {
		if total <= 0 {
			return "Unpacking. Many small files make this slow on some disks."
		}
		return fmt.Sprintf("%d%%. Many small files make this slow on some disks.", 100*done/total)
	}
	text := humanBytes(done)
	if total > 0 {
		text += " of " + humanBytes(total)
	}
	if secs := elapsed.Seconds(); secs >= 1 && done > 0 {
		text += " · " + humanBytes(int64(float64(done)/secs)) + "/s"
	}
	return text
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
