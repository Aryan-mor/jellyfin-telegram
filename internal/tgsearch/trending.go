package tgsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Keys under which the trending source remembers its day.
const (
	trendingDayKey  = "tgsearch.trending.day"
	trendingListKey = "tgsearch.trending.list"
)

// DefaultGenres are the genres a trending title may have when no list is configured.
var DefaultGenres = []string{
	"Action", "Adventure", "Animation", "Comedy", "Crime", "Documentary", "Drama", "Family", "Fantasy",
	"History", "Horror", "Music", "Mystery", "Romance", "Science Fiction", "Thriller", "War", "Western",
	"Action & Adventure", "Sci-Fi & Fantasy", "War & Politics", "Kids",
}

// TrendingFilter decides which trending titles are searched for. It applies to trending only; a
// Seerr request is always searched.
type TrendingFilter struct {
	// AllowAdult lets titles that TMDB flags as adult, or that carry an erotic genre, through.
	AllowAdult bool
	// Include lists the genres a title must share at least one of. Empty means DefaultGenres.
	Include []string
	// Exclude lists genres that reject a title, even when it is also in Include.
	Exclude []string
}

// Allows reports whether a title with these properties passes. Genre names are compared without
// regard to case.
func (f TrendingFilter) Allows(adult bool, genres []string) bool {
	erotic := false
	for _, g := range genres {
		if strings.Contains(strings.ToLower(g), "erotic") {
			erotic = true
		}
	}
	if (adult || erotic) && !f.AllowAdult {
		return false
	}
	include := f.Include
	if len(include) == 0 {
		include = DefaultGenres
	}
	has := func(list []string, g string) bool {
		for _, x := range list {
			if strings.EqualFold(strings.TrimSpace(x), g) {
				return true
			}
		}
		return false
	}
	ok := false
	for _, g := range genres {
		if has(f.Exclude, g) {
			return false
		}
		if has(include, g) {
			ok = true
		}
	}
	return ok
}

// Trending turns TMDB's daily trending list into search queries. It fetches once a day, keeps
// the day's list so the repeated passes of that day search the same titles, and caps how many
// titles it hands out. Titles that are not released yet are left out and looked at again on a
// later day, when the list is fetched anew.
type Trending struct {
	// Key is the TMDB API key (v3 key or v4 read token).
	Key string
	// Base is the TMDB API root. Empty uses the public one.
	Base   string
	Filter TrendingFilter
	// Top is how many trending titles are looked at. Default 20.
	Top int
	// MaxPerDay caps the titles searched per day. Default 10.
	MaxPerDay int
	Store     interface {
		StateValue(ctx context.Context, key string) (string, error)
		SetStateValue(ctx context.Context, key, value string) error
	}
	HTTP *http.Client
	Now  func() time.Time

	// day and list back the state when Store is nil.
	day  string
	list []Query
}

func (t *Trending) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// Queries returns the day's titles, fetching the list on the first call of each day.
func (t *Trending) Queries(ctx context.Context) ([]Query, error) {
	if t == nil || strings.TrimSpace(t.Key) == "" {
		return nil, nil
	}
	today := t.now().Format("2006-01-02")
	if last, list, ok := t.load(ctx); ok && last == today {
		return list, nil
	}
	list, err := t.fetch(ctx)
	if err != nil {
		// Not marked as run, so the next pass tries again.
		return nil, err
	}
	t.save(ctx, today, list)
	return list, nil
}

func (t *Trending) load(ctx context.Context) (string, []Query, bool) {
	if t.Store == nil {
		return t.day, t.list, t.day != ""
	}
	day, err := t.Store.StateValue(ctx, trendingDayKey)
	if err != nil || day == "" {
		return "", nil, false
	}
	raw, err := t.Store.StateValue(ctx, trendingListKey)
	if err != nil {
		return "", nil, false
	}
	var list []Query
	_ = json.Unmarshal([]byte(raw), &list)
	return day, list, true
}

func (t *Trending) save(ctx context.Context, day string, list []Query) {
	t.day, t.list = day, list
	if t.Store == nil {
		return
	}
	raw, _ := json.Marshal(list)
	_ = t.Store.SetStateValue(ctx, trendingListKey, string(raw))
	_ = t.Store.SetStateValue(ctx, trendingDayKey, day)
}

type trendItem struct {
	ID           int    `json:"id"`
	MediaType    string `json:"media_type"`
	Adult        bool   `json:"adult"`
	GenreIDs     []int  `json:"genre_ids"`
	ReleaseDate  string `json:"release_date"`
	FirstAirDate string `json:"first_air_date"`
}

func (t *Trending) fetch(ctx context.Context) ([]Query, error) {
	var page struct {
		Results []trendItem `json:"results"`
	}
	if err := t.get(ctx, "/trending/all/day", nil, &page); err != nil {
		return nil, err
	}
	top := t.Top
	if top <= 0 {
		top = 20
	}
	if len(page.Results) > top {
		page.Results = page.Results[:top]
	}
	names, err := t.genreNames(ctx)
	if err != nil {
		return nil, err
	}
	limit := t.MaxPerDay
	if limit <= 0 {
		limit = 10
	}
	today := t.now().Format("2006-01-02")
	var out []Query
	for _, it := range page.Results {
		if len(out) >= limit {
			break
		}
		movie := it.MediaType == "movie"
		if !movie && it.MediaType != "tv" {
			continue
		}
		date := it.FirstAirDate
		if movie {
			date = it.ReleaseDate
		}
		if !Released(date, today) {
			continue
		}
		var genres []string
		for _, id := range it.GenreIDs {
			if n, ok := names[id]; ok {
				genres = append(genres, n)
			}
		}
		if !t.Filter.Allows(it.Adult, genres) {
			continue
		}
		q := Query{TMDB: it.ID, Movie: movie}
		if movie {
			q.Runtime = t.runtime(ctx, it.ID)
		}
		out = append(out, q)
	}
	return out, nil
}

// Released reports that date (YYYY-MM-DD) is today or earlier. A missing or unreadable date counts
// as not released.
func Released(date, today string) bool {
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return false
	}
	return date <= today
}

// genreNames maps TMDB genre ids to English names, for films and series.
func (t *Trending) genreNames(ctx context.Context) (map[int]string, error) {
	out := map[int]string{}
	for _, kind := range []string{"movie", "tv"} {
		var res struct {
			Genres []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"genres"`
		}
		if err := t.get(ctx, "/genre/"+kind+"/list", url.Values{"language": {"en-US"}}, &res); err != nil {
			return nil, err
		}
		for _, g := range res.Genres {
			out[g.ID] = g.Name
		}
	}
	return out, nil
}

// runtime asks for a film's running time in minutes. A failure leaves it unknown, which only turns
// the runtime check off.
func (t *Trending) runtime(ctx context.Context, id int) int {
	var res struct {
		Runtime int `json:"runtime"`
	}
	if err := t.get(ctx, fmt.Sprintf("/movie/%d", id), nil, &res); err != nil {
		return 0
	}
	return res.Runtime
}

func (t *Trending) get(ctx context.Context, path string, q url.Values, into any) error {
	base := strings.TrimRight(t.Base, "/")
	if base == "" {
		base = "https://api.themoviedb.org/3"
	}
	if q == nil {
		q = url.Values{}
	}
	key := strings.TrimSpace(t.Key)
	bearer := strings.Count(key, ".") == 2
	if !bearer {
		q.Set("api_key", key)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	if bearer {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	c := t.HTTP
	if c == nil {
		c = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return errors.New("TMDB could not be reached")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("TMDB answered %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

// Multi joins several sources of wanted titles. The first source's titles come first, and a title
// is listed once. A failing source does not hide the others.
type Multi []Wanted

// Queries gathers the titles of every source.
func (m Multi) Queries(ctx context.Context) ([]Query, error) {
	var out []Query
	seen := map[string]bool{}
	var firstErr error
	for _, w := range m {
		if w == nil {
			continue
		}
		qs, err := w.Queries(ctx)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		for _, q := range qs {
			if !seen[q.Text()] {
				seen[q.Text()] = true
				out = append(out, q)
			}
		}
	}
	if len(out) == 0 {
		return nil, firstErr
	}
	return out, nil
}

// ParseList splits a comma separated setting into trimmed, non-empty items.
func ParseList(csv string) []string {
	var out []string
	for _, p := range strings.Split(csv, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
