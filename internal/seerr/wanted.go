package seerr

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TitleKey is how the indexer names a TMDB title in its country cache.
func TitleKey(tmdb int, series bool) string {
	k := "m"
	if series {
		k = "e"
	}
	return k + strconv.Itoa(tmdb)
}

// OpenTitles lists Seerr requests and watchlist entries as titles for search.
func OpenTitles(ctx context.Context, base, key, root string) ([]OpenTitle, error) {
	set, err := WantedTitles(ctx, base, key, root)
	if err != nil {
		return nil, err
	}
	var out []OpenTitle
	for k := range set {
		if len(k) < 2 {
			continue
		}
		series := k[0] == 'e'
		tmdb, err := strconv.Atoi(k[1:])
		if err != nil || tmdb <= 0 {
			continue
		}
		out = append(out, OpenTitle{TMDB: tmdb, Series: series})
	}
	return out, nil
}

// WantedTitlesFromAPI is OpenTitles under a clearer name for tgsearch.
func WantedTitlesFromAPI(ctx context.Context, base, key, root string) ([]OpenTitle, error) {
	return OpenTitles(ctx, base, key, root)
}

// WantedTitles merges open Seerr requests and every watchlist entry. These titles are not dropped
// by the auto country filter and are always searched on Telegram.
func WantedTitles(ctx context.Context, base, key, root string) (map[string]bool, error) {
	open, err := OpenRequests(ctx, base, key)
	if err != nil {
		return nil, err
	}
	watch, err := WatchlistTitles(root)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	add := func(list []OpenTitle) {
		for _, t := range list {
			if t.TMDB > 0 {
				set[TitleKey(t.TMDB, t.Series)] = true
			}
		}
	}
	add(open)
	add(watch)
	return set, nil
}

// Wanted caches WantedTitles for a short time so a catalog pass does not hammer Seerr.
type Wanted struct {
	Base string
	Key  string
	Root string
	TTL  time.Duration

	mu  sync.Mutex
	at  time.Time
	set map[string]bool
	err error
}

// Keep reports whether a TMDB title is protected by a Seerr request or watchlist entry.
func (w *Wanted) Keep(ctx context.Context, tmdb int, series bool) (bool, error) {
	if w == nil || tmdb <= 0 {
		return false, nil
	}
	if err := w.refresh(ctx); err != nil {
		return false, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return false, w.err
	}
	return w.set[TitleKey(tmdb, series)], nil
}

// Set returns the cached protected title keys.
func (w *Wanted) Set(ctx context.Context) (map[string]bool, error) {
	if w == nil {
		return nil, nil
	}
	if err := w.refresh(ctx); err != nil {
		return nil, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return nil, w.err
	}
	out := make(map[string]bool, len(w.set))
	for k, v := range w.set {
		out[k] = v
	}
	return out, nil
}

func (w *Wanted) refresh(ctx context.Context) error {
	if strings.TrimSpace(w.Base) == "" {
		return nil
	}
	ttl := w.TTL
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.set != nil && time.Since(w.at) < ttl {
		return nil
	}
	set, err := WantedTitles(ctx, w.Base, w.Key, w.Root)
	if err != nil {
		w.err = err
		return err
	}
	w.set, w.err, w.at = set, nil, time.Now()
	return nil
}
