package tgsearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/autofilter"
)

// Keys under which the trending source remembers its day.
const (
	trendingDayKey  = "tgsearch.trending.day"
	trendingListKey = "tgsearch.trending.list"
)

// Trending turns TMDB's daily trending list into search queries. It fetches once a day, keeps
// the day's list so the repeated passes of that day search the same titles, and caps how many
// titles it hands out. Titles that are not released yet are left out and looked at again on a
// later day, when the list is fetched anew.
type Trending struct {
	// Key is the TMDB API key (v3 key or v4 read token).
	Key string
	// Base is the TMDB API root. Empty uses the public one.
	Base string
	// Filter decides which titles are searched. Settings, when set, is read on every fetch instead,
	// so a saved change applies without a restart. A Seerr request never passes through either.
	Filter   autofilter.Filter
	Settings func() autofilter.Filter
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

func (t *Trending) filter() autofilter.Filter {
	if t.Settings != nil {
		return t.Settings()
	}
	return t.Filter
}

// Queries returns the day's titles, fetching the list on the first call of each day and again
// when the filter changes.
func (t *Trending) Queries(ctx context.Context) ([]Query, error) {
	if t == nil || strings.TrimSpace(t.Key) == "" {
		return nil, nil
	}
	f := t.filter()
	raw, _ := json.Marshal(f)
	sum := sha256.Sum256(raw)
	stamp := t.now().Format("2006-01-02") + " " + hex.EncodeToString(sum[:8])
	if last, list, ok := t.load(ctx); ok && last == stamp {
		return list, nil
	}
	list, err := t.fetch(ctx, f)
	if err != nil {
		// Not marked as run, so the next pass tries again.
		return nil, err
	}
	t.save(ctx, stamp, list)
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
	// Series carry their origin countries here; films do not, so details fill them in.
	OriginCountry    []string `json:"origin_country"`
	OriginalLanguage string   `json:"original_language"`
}

func (t *Trending) fetch(ctx context.Context, filter autofilter.Filter) ([]Query, error) {
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
		// An empty list is not "no country". Films on the trending list usually send one,
		// and the detail call is what names the production country.
		countries := it.OriginCountry
		if len(countries) == 0 {
			countries = nil
		}
		title := autofilter.Title{Adult: it.Adult, Genres: genres, Countries: countries}
		if it.OriginalLanguage != "" {
			title.Languages = []string{it.OriginalLanguage}
		}
		var d details
		fetched := false
		more := func() autofilter.Title {
			d, fetched = t.details(ctx, it.ID, movie), true
			return autofilter.Title{Countries: d.countries, Networks: d.networks}
		}
		if !filter.Allows(title, more) {
			continue
		}
		q := Query{TMDB: it.ID, Movie: movie}
		if movie {
			if !fetched {
				d = t.details(ctx, it.ID, true)
			}
			q.Runtime = d.runtime
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

// GenreNames lists TMDB's English genre names for films and series, sorted and without repeats.
func GenreNames(ctx context.Context, key string) ([]string, error) {
	ids, err := (&Trending{Key: key}).genreNames(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, n := range ids {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

// details is what one more TMDB call tells about a title. Nil lists mean unknown.
type details struct {
	runtime   int
	countries []string
	networks  []string
}

// details asks for a film's running time, origin countries and streaming providers in one call, or
// a series' networks and origin countries. A failure leaves everything unknown, which only turns
// the runtime check off and keeps those filter parts from being positive.
func (t *Trending) details(ctx context.Context, id int, movie bool) details {
	var res struct {
		Runtime             int      `json:"runtime"`
		OriginCountry       []string `json:"origin_country"`
		ProductionCountries []struct {
			Code string `json:"iso_3166_1"`
		} `json:"production_countries"`
		Networks []struct {
			ID int `json:"id"`
		} `json:"networks"`
		Providers struct {
			Results map[string]struct {
				Flatrate []struct {
					ID int `json:"provider_id"`
				} `json:"flatrate"`
			} `json:"results"`
		} `json:"watch/providers"`
	}
	path, q := fmt.Sprintf("/tv/%d", id), url.Values(nil)
	if movie {
		path, q = fmt.Sprintf("/movie/%d", id), url.Values{"append_to_response": {"watch/providers"}}
	}
	if err := t.get(ctx, path, q, &res); err != nil {
		return details{}
	}
	d := details{runtime: res.Runtime}
	seen := map[string]bool{}
	add := func(code string) {
		code = strings.ToUpper(strings.TrimSpace(code))
		if code == "" || seen[code] {
			return
		}
		seen[code] = true
		d.countries = append(d.countries, code)
	}
	for _, c := range res.OriginCountry {
		add(c)
	}
	for _, c := range res.ProductionCountries {
		add(c.Code)
	}
	var ids []int
	if movie {
		for _, region := range res.Providers.Results {
			for _, p := range region.Flatrate {
				ids = append(ids, p.ID)
			}
		}
	} else {
		for _, n := range res.Networks {
			ids = append(ids, n.ID)
		}
	}
	d.networks = autofilter.NetworksOf(ids, !movie)
	return d
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
