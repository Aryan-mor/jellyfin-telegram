package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/jellyfin"
	"github.com/Aryan-mor/jellyfin-telegram/internal/seerr"
	"github.com/Aryan-mor/jellyfin-telegram/internal/subtitles"
)

// savedTitles is the Jellyfin favorites and the Seerr watchlist, cached for a short time.
// A saved series covers every episode of that series.
type savedTitles struct {
	jf   *jellyfin.Client
	root string
	log  *slog.Logger

	mu     sync.Mutex
	at     time.Time
	movies map[int]bool
	series map[int]bool
}

func (s *savedTitles) allows(ctx context.Context, m subtitles.Meta) bool {
	if s == nil || m.TMDB <= 0 {
		return false
	}
	if err := s.refresh(ctx); err != nil {
		if s.log != nil {
			s.log.Warn("saved titles", "err", err)
		}
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.Season > 0 || m.Episode > 0 {
		return s.series[m.TMDB]
	}
	return s.movies[m.TMDB]
}

func (s *savedTitles) refresh(ctx context.Context) error {
	s.mu.Lock()
	if s.movies != nil && time.Since(s.at) < 2*time.Minute {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	movies := map[int]bool{}
	series := map[int]bool{}
	var jfErr, watchErr error
	if s.jf != nil {
		saved, err := s.jf.Favorites(ctx)
		if err != nil {
			jfErr = err
		} else {
			movies, series = saved.Movies, saved.Series
		}
	}
	watch, err := seerr.WatchlistTitles(s.root)
	watchErr = err
	if err == nil {
		for _, t := range watch {
			if t.TMDB <= 0 {
				continue
			}
			if t.Series {
				series[t.TMDB] = true
			} else {
				movies[t.TMDB] = true
			}
		}
	}
	if jfErr != nil && watchErr != nil {
		return jfErr
	}
	s.mu.Lock()
	s.movies, s.series, s.at = movies, series, time.Now()
	s.mu.Unlock()
	return nil
}

// root is accepted so a test can point at a temp Seerr directory.
func savedRoot(dataDir string) string { return filepath.Join(dataDir, "seerr") }
