package tgsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/Aryan-mor/jellyfin-telegram/internal/seerr"
)

// Seerr turns open Seerr requests into search queries.
type Seerr struct {
	Base string
	Root string
	key  string
	// Store caches title details so a pass does not ask Seerr for every title every time.
	Store interface {
		StateValue(ctx context.Context, key string) (string, error)
		SetStateValue(ctx context.Context, key, value string) error
	}
}

const detailTTL = 12 * time.Hour

type cachedDetail struct {
	Title    string `json:"title"`
	Alt      string `json:"alt"`
	Year     int    `json:"year"`
	IMDb     string `json:"imdb"`
	Runtime  int    `json:"runtime"`
	Digital  bool   `json:"digital"`
	Release  int64  `json:"release"`
	StoredAt int64  `json:"at"`
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
		q := Query{TMDB: t.TMDB, Movie: !t.Series}
		if d, ok := s.detail(ctx, t.TMDB, t.Series); ok {
			q.Title = d.Title
			if !strings.EqualFold(d.OriginalTitle, d.Title) {
				q.AltTitle = d.OriginalTitle
			}
			q.Year = d.Year
			q.IMDb = d.IMDb
			q.Runtime = d.Runtime
			q.DigitalOut = d.DigitalOut
			q.Release = d.Release
		}
		out = append(out, q)
	}
	return out, nil
}

func (s *Seerr) detail(ctx context.Context, tmdb int, series bool) (seerr.Info, bool) {
	kind := "m"
	if series {
		kind = "tv"
	}
	key := fmt.Sprintf("tgsearch.meta.%s.%d", kind, tmdb)
	if s.Store != nil {
		if raw, err := s.Store.StateValue(ctx, key); err == nil && raw != "" {
			var cached cachedDetail
			if json.Unmarshal([]byte(raw), &cached) == nil && time.Since(time.Unix(cached.StoredAt, 0)) < detailTTL {
				return cached.details(), true
			}
		}
	}
	d, err := seerr.Details(ctx, s.Base, s.key, tmdb, series)
	if err != nil {
		return seerr.Info{}, false
	}
	if s.Store != nil {
		raw, merr := json.Marshal(cachedDetail{
			Title: d.Title, Alt: d.OriginalTitle, Year: d.Year, IMDb: d.IMDb,
			Runtime: d.Runtime, Digital: d.DigitalOut, Release: unixOf(d.Release), StoredAt: time.Now().Unix(),
		})
		if merr == nil {
			_ = s.Store.SetStateValue(ctx, key, string(raw))
		}
	}
	return d, true
}

func (c cachedDetail) details() seerr.Info {
	d := seerr.Info{
		Title: c.Title, OriginalTitle: c.Alt, Year: c.Year, IMDb: c.IMDb,
		Runtime: c.Runtime, DigitalOut: c.Digital,
	}
	if c.Release > 0 {
		d.Release = time.Unix(c.Release, 0).UTC()
	}
	return d
}

func unixOf(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().Unix()
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
