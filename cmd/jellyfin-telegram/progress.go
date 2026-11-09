package main

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/Aryan-mor/jellyfin-telegram/internal/setup"
)

// watchSetup prints a progress bar for each change the setup page is already showing.
func watchSetup(w *setup.Wizard) {
	var mu sync.Mutex
	var prev setup.Status
	var have bool
	w.Notify(func(s setup.Status) {
		mu.Lock()
		defer mu.Unlock()
		if have && !showProgress(prev, s) {
			return
		}
		prev, have = s, true
		fmt.Fprintln(os.Stderr, formatSetupLine(s))
	})
}

// showProgress keeps a download from printing a line for every single percent.
func showProgress(prev, next setup.Status) bool {
	if next.Error != "" || next.Ready || next.Stage != prev.Stage {
		return true
	}
	return next.Percent/5 != prev.Percent/5
}

func formatSetupLine(s setup.Status) string {
	pct := s.Percent
	if s.Ready {
		pct = 100
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	const width = 28
	filled := width * pct / 100
	bar := strings.Repeat("#", filled) + strings.Repeat("-", width-filled)
	stage := s.Stage
	if s.Ready {
		stage = "Ready"
	}
	if s.Error != "" {
		return fmt.Sprintf("[%s] %3d%%  %s: %s", bar, pct, stage, s.Error)
	}
	return fmt.Sprintf("[%s] %3d%%  %s", bar, pct, stage)
}
