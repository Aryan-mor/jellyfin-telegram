package tgsearch

import (
	"context"
	"strings"

	"github.com/joho/godotenv"

	"github.com/Aryan-mor/jellyfin-telegram/internal/seerr"
)

// Seerr turns open Seerr requests into search queries.
type Seerr struct {
	Base string
	Root string
	key  string
}

// Queries lists films and series that Seerr still does not have.
func (s *Seerr) Queries(ctx context.Context) ([]Query, error) {
	if s == nil || strings.TrimSpace(s.Base) == "" {
		return nil, nil
	}
	if s.key == "" {
		key, err := seerr.EnsureAPIKey(s.Root)
		if err != nil {
			return nil, err
		}
		s.key = key
	}
	titles, err := seerr.OpenRequests(ctx, s.Base, s.key)
	if err != nil {
		return nil, err
	}
	var out []Query
	for _, t := range titles {
		out = append(out, Query{TMDB: t.TMDB, Movie: !t.Series})
	}
	return out, nil
}

// Switch reads TELEGRAM_SEARCH from the settings file. A missing line stays on.
func Switch(path string) func() bool {
	return func() bool {
		m, err := godotenv.Read(path)
		if err != nil {
			return true
		}
		v, ok := m["TELEGRAM_SEARCH"]
		v = strings.TrimSpace(v)
		if !ok || v == "" {
			return true
		}
		return strings.EqualFold(v, "true") || v == "1"
	}
}
