package seerr

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Languages asks Seerr, which keeps TMDB's data, what language a film or series was made in. The
// answer decides which audio track of a file is the original and which is a dub.
type Languages struct {
	// Base is the Seerr address and Key its API key.
	Base string
	Key  string

	mu    sync.Mutex
	cache map[string]string
}

// Original returns the ISO 639-1 original language of a film or series, such as "en" or "ja".
// Only an answer is remembered, so a Seerr that is still starting is asked again later.
func (l *Languages) Original(ctx context.Context, tmdb int, series bool) (string, error) {
	kind := "movie"
	if series {
		kind = "tv"
	}
	id := fmt.Sprintf("%s/%d", kind, tmdb)
	l.mu.Lock()
	if got, ok := l.cache[id]; ok {
		l.mu.Unlock()
		return got, nil
	}
	l.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c := &client{base: strings.TrimRight(l.Base, "/"), key: l.Key, http: &http.Client{}}
	var body struct {
		OriginalLanguage string `json:"originalLanguage"`
	}
	if err := c.callOK(ctx, http.MethodGet, "/api/v1/"+id, nil, &body); err != nil {
		return "", err
	}
	got := strings.ToLower(strings.TrimSpace(body.OriginalLanguage))
	if got == "" {
		return "", nil
	}
	l.mu.Lock()
	if l.cache == nil {
		l.cache = map[string]string{}
	}
	l.cache[id] = got
	l.mu.Unlock()
	return got, nil
}

// Info is what Seerr knows about one film or series. Seerr 3.5.1 returns camelCase titles
// and a TMDB release list whose dates stay snake_case.
type Info struct {
	Title         string
	OriginalTitle string
	Year          int
	IMDb          string
	Runtime       int
	// DigitalOut is true when a digital or physical release date is today or earlier.
	// Series never set it: they have no release-date list of that kind.
	DigitalOut bool
	// Release is the earliest digital or physical date when DigitalOut is set,
	// otherwise the premiere date.
	Release time.Time
}

// Details asks Seerr for one title. The id is the TMDB id. series chooses the tv route.
func Details(ctx context.Context, base, key string, tmdb int, series bool) (Info, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" || strings.TrimSpace(key) == "" || tmdb <= 0 {
		return Info{}, fmt.Errorf("seerr details need an address, a key and a tmdb id")
	}
	kind := "movie"
	if series {
		kind = "tv"
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c := &client{base: base, key: key, http: &http.Client{Timeout: 20 * time.Second}}
	var body struct {
		Title         string `json:"title"`
		OriginalTitle string `json:"originalTitle"`
		Name          string `json:"name"`
		OriginalName  string `json:"originalName"`
		ReleaseDate   string `json:"releaseDate"`
		FirstAirDate  string `json:"firstAirDate"`
		Runtime       int    `json:"runtime"`
		EpisodeRun    []int  `json:"episodeRunTime"`
		IMDbID        string `json:"imdbId"`
		ExternalIDs   struct {
			IMDbID string `json:"imdbId"`
		} `json:"externalIds"`
		Releases struct {
			Results []struct {
				Dates []struct {
					Type int    `json:"type"`
					When string `json:"release_date"`
				} `json:"release_dates"`
			} `json:"results"`
		} `json:"releases"`
	}
	if err := c.callOK(ctx, http.MethodGet, fmt.Sprintf("/api/v1/%s/%d", kind, tmdb), nil, &body); err != nil {
		return Info{}, err
	}
	d := Info{
		Title:         firstText(body.Title, body.Name),
		OriginalTitle: firstText(body.OriginalTitle, body.OriginalName),
		Runtime:       body.Runtime,
		IMDb:          strings.ToLower(firstText(body.ExternalIDs.IMDbID, body.IMDbID)),
	}
	if d.Runtime == 0 {
		for _, n := range body.EpisodeRun {
			if n > 0 {
				d.Runtime = n
				break
			}
		}
	}
	premiere := firstText(body.ReleaseDate, body.FirstAirDate)
	d.Year = yearOf(premiere)
	d.Release = dateOf(premiere)
	now := time.Now()
	var earliest time.Time
	for _, country := range body.Releases.Results {
		for _, rel := range country.Dates {
			if rel.Type != 4 && rel.Type != 5 {
				continue
			}
			when := dateOf(rel.When)
			if when.IsZero() || when.After(now) {
				continue
			}
			d.DigitalOut = true
			if earliest.IsZero() || when.Before(earliest) {
				earliest = when
			}
		}
	}
	if !earliest.IsZero() {
		d.Release = earliest
	}
	return d, nil
}

func firstText(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func yearOf(raw string) int {
	raw = strings.TrimSpace(raw)
	if len(raw) < 4 {
		return 0
	}
	y, err := strconv.Atoi(raw[:4])
	if err != nil || y < 1880 || y > 2200 {
		return 0
	}
	return y
}

func dateOf(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
