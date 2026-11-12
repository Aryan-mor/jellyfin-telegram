package tgsearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/autofilter"
)

// filterTMDB serves a trending list of Korean and American titles, some on Netflix. details counts
// the detail calls.
func filterTMDB(t *testing.T, details *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enc := json.NewEncoder(w)
		switch {
		case r.URL.Path == "/trending/all/day":
			_ = enc.Encode(map[string]any{"results": []map[string]any{
				{"id": 42, "media_type": "tv", "first_air_date": "2025-01-01", "genre_ids": []int{18}, "origin_country": []string{"KR"}, "original_language": "ko"},
				{"id": 43, "media_type": "tv", "first_air_date": "2025-01-01", "genre_ids": []int{18}, "origin_country": []string{"KR"}, "original_language": "ko"},
				{"id": 44, "media_type": "tv", "first_air_date": "2025-01-01", "genre_ids": []int{18}, "origin_country": []string{"US"}, "original_language": "en"},
				{"id": 45, "media_type": "movie", "release_date": "2025-01-01", "genre_ids": []int{18}, "original_language": "ko"},
			}})
		case strings.HasPrefix(r.URL.Path, "/genre/"):
			_ = enc.Encode(map[string]any{"genres": []map[string]any{{"id": 18, "name": "Drama"}}})
		case r.URL.Path == "/tv/42":
			*details++
			_ = enc.Encode(map[string]any{"origin_country": []string{"KR"}, "networks": []map[string]any{{"id": 1}}})
		case r.URL.Path == "/tv/43":
			*details++
			_ = enc.Encode(map[string]any{"origin_country": []string{"KR"}, "networks": []map[string]any{{"id": 213}}})
		case r.URL.Path == "/tv/44":
			*details++
			_ = enc.Encode(map[string]any{"origin_country": []string{"US"}, "networks": []map[string]any{{"id": 49}}})
		case r.URL.Path == "/movie/45":
			*details++
			if r.URL.Query().Get("append_to_response") != "watch/providers" {
				t.Errorf("film details without providers: %s", r.URL.RawQuery)
			}
			_ = enc.Encode(map[string]any{"runtime": 120, "origin_country": []string{"KR"},
				"watch/providers": map[string]any{"results": map[string]any{"DE": map[string]any{"flatrate": []map[string]any{{"provider_id": 8}}}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func trendingWith(srv *httptest.Server, f autofilter.Filter) *Trending {
	return &Trending{Key: "k", Base: srv.URL, Filter: f, MaxPerDay: 10,
		Now: func() time.Time { return time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC) }}
}

func ids(qs []Query) []int {
	var out []int
	for _, q := range qs {
		out = append(out, q.TMDB)
	}
	return out
}

func TestTrendingExcludedCountryUsesProductionWhenTheListOmitsIt(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enc := json.NewEncoder(w)
		switch {
		case r.URL.Path == "/trending/all/day":
			_ = enc.Encode(map[string]any{"results": []map[string]any{
				{"id": 7, "media_type": "movie", "release_date": "2025-01-01", "genre_ids": []int{18}, "origin_country": []string{}, "original_language": "hi"},
				{"id": 8, "media_type": "tv", "first_air_date": "2025-01-01", "genre_ids": []int{18}, "origin_country": []string{"IN"}, "original_language": "hi"},
				{"id": 9, "media_type": "movie", "release_date": "2025-01-01", "genre_ids": []int{18}, "origin_country": []string{}, "original_language": "en"},
			}})
		case strings.HasPrefix(r.URL.Path, "/genre/"):
			_ = enc.Encode(map[string]any{"genres": []map[string]any{{"id": 18, "name": "Drama"}}})
		case r.URL.Path == "/movie/7":
			calls++
			_ = enc.Encode(map[string]any{"runtime": 100, "production_countries": []map[string]any{{"iso_3166_1": "IN"}}})
		case r.URL.Path == "/movie/9":
			calls++
			_ = enc.Encode(map[string]any{"runtime": 100, "origin_country": []string{"US"}, "production_countries": []map[string]any{{"iso_3166_1": "US"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	f := autofilter.Filter{Countries: autofilter.Rule{Exclude: []string{"IN"}}}
	qs, err := trendingWith(srv, f).Queries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(qs); len(got) != 1 || got[0] != 9 {
		t.Fatalf("got %v", got)
	}
	if calls != 2 {
		t.Fatalf("detail calls %d", calls)
	}
}

func TestTrendingExcludedCountryButIncludedNetwork(t *testing.T) {
	var calls int
	srv := filterTMDB(t, &calls)
	f := autofilter.Filter{
		Countries: autofilter.Rule{Exclude: []string{"KR"}},
		Networks:  autofilter.Rule{Include: []string{"netflix"}},
	}
	qs, err := trendingWith(srv, f).Queries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 42 is Korean and not on Netflix, 44 is not on Netflix; 43 and the film 45 are on Netflix.
	if got := ids(qs); len(got) != 2 || got[0] != 43 || got[1] != 45 {
		t.Fatalf("got %v", got)
	}
	if qs[1].Runtime != 120 {
		t.Fatalf("runtime %d", qs[1].Runtime)
	}
	// The film's details are fetched once, for both the filter and the runtime.
	if calls != 4 {
		t.Fatalf("detail calls %d", calls)
	}
}

func TestTrendingSkipsDetailsWhenTheyCannotMatter(t *testing.T) {
	var calls int
	srv := filterTMDB(t, &calls)
	// A positive language decides before the networks are needed.
	f := autofilter.Filter{
		Languages: autofilter.Rule{Include: []string{"ko"}},
		Networks:  autofilter.Rule{Include: []string{"netflix"}},
	}
	qs, err := trendingWith(srv, f).Queries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 44 needs its networks and is rejected; the Korean film is fetched once for its runtime.
	if got := ids(qs); len(got) != 3 || got[0] != 42 || got[1] != 43 || got[2] != 45 {
		t.Fatalf("got %v", got)
	}
	if calls != 2 {
		t.Fatalf("detail calls %d", calls)
	}
}

func TestTrendingRefetchesWhenTheFilterChanges(t *testing.T) {
	var calls int
	srv := filterTMDB(t, &calls)
	f := autofilter.Filter{Countries: autofilter.Rule{Include: []string{"US"}}}
	tr := trendingWith(srv, f)
	tr.Settings = func() autofilter.Filter { return f }
	qs, _ := tr.Queries(context.Background())
	if got := ids(qs); len(got) != 1 || got[0] != 44 {
		t.Fatalf("got %v", got)
	}
	f = autofilter.Filter{Countries: autofilter.Rule{Include: []string{"KR"}}}
	qs, _ = tr.Queries(context.Background())
	if got := ids(qs); len(got) != 3 || got[0] != 42 {
		t.Fatalf("after change got %v", got)
	}
}

func TestSeerrRequestsBypassTheFilter(t *testing.T) {
	var calls int
	tmdb := filterTMDB(t, &calls)
	seerr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pageInfo": map[string]any{"pages": 1},
			"results": []map[string]any{
				{"status": 2, "media": map[string]any{"tmdbId": 42, "mediaType": "tv", "status": 3}},
				{"status": 2, "media": map[string]any{"tmdbId": 900, "mediaType": "movie", "status": 3}},
			},
		})
	}))
	defer seerr.Close()
	// This filter rejects every trending title.
	none := autofilter.Filter{Countries: autofilter.Rule{Include: []string{"FR"}}, Networks: autofilter.Rule{Exclude: []string{"netflix"}}}
	want := Multi{&Seerr{Base: seerr.URL, key: "x"}, trendingWith(tmdb, none)}
	qs, err := want.Queries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(qs); len(got) != 2 || got[0] != 42 || got[1] != 900 {
		t.Fatalf("got %v", got)
	}
}
