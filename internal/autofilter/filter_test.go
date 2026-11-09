package autofilter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllows(t *testing.T) {
	korean := Title{Genres: []string{"Drama"}, Countries: []string{"KR"}, Languages: []string{"ko"}, Networks: []string{"netflix"}}
	koreanElsewhere := Title{Genres: []string{"Drama"}, Countries: []string{"KR"}, Languages: []string{"ko"}, Networks: []string{}}
	american := Title{Genres: []string{"Action"}, Countries: []string{"US"}, Languages: []string{"en"}, Networks: []string{"hulu"}}
	cases := []struct {
		name string
		f    Filter
		t    Title
		want bool
	}{
		// Nothing set: the old default genre list decides.
		{"empty keeps a default genre", Filter{}, american, true},
		{"empty drops a genre outside the defaults", Filter{}, Title{Genres: []string{"Reality"}}, false},
		{"empty drops a title without genres", Filter{}, Title{}, false},

		{"include only passes a match", Filter{Countries: Rule{Include: []string{"us"}}}, american, true},
		{"include only drops the rest", Filter{Countries: Rule{Include: []string{"US"}}}, korean, false},
		{"include only drops unknown values", Filter{Networks: Rule{Include: []string{"netflix"}}}, Title{Genres: []string{"Drama"}}, false},

		{"exclude only drops a match", Filter{Languages: Rule{Exclude: []string{"KO"}}}, korean, false},
		{"exclude only passes the rest", Filter{Languages: Rule{Exclude: []string{"ko"}}}, american, true},
		{"exclude only no longer needs a default genre", Filter{Genres: Rule{Exclude: []string{"Horror"}}}, Title{Genres: []string{"Reality"}}, true},

		{"both passes an include match", Filter{Genres: Rule{Include: []string{"Drama"}, Exclude: []string{"Horror"}}}, korean, true},
		{"both drops a title in include and exclude", Filter{Genres: Rule{Include: []string{"Drama"}, Exclude: []string{"Horror"}}},
			Title{Genres: []string{"Drama", "Horror"}}, false},
		{"both drops a title in neither", Filter{Genres: Rule{Include: []string{"Drama"}, Exclude: []string{"Horror"}}}, american, false},

		{"OR: excluded country, included network", Filter{Countries: Rule{Exclude: []string{"KR"}}, Networks: Rule{Include: []string{"netflix"}}}, korean, true},
		{"OR: excluded country, other network", Filter{Countries: Rule{Exclude: []string{"KR"}}, Networks: Rule{Include: []string{"netflix"}}}, koreanElsewhere, false},
		{"OR: neutral country, other network", Filter{Countries: Rule{Exclude: []string{"KR"}}, Networks: Rule{Include: []string{"netflix"}}}, american, false},
		{"OR: one positive beats two rejections", Filter{Genres: Rule{Exclude: []string{"Drama"}}, Languages: Rule{Exclude: []string{"ko"}}, Countries: Rule{Include: []string{"KR"}}}, korean, true},

		{"adult veto", Filter{}, Title{Adult: true, Genres: []string{"Drama"}}, false},
		{"erotic genre veto", Filter{}, Title{Genres: []string{"Drama", "Erotic"}}, false},
		{"adult veto beats an include", Filter{Networks: Rule{Include: []string{"netflix"}}}, Title{Adult: true, Networks: []string{"netflix"}}, false},
		{"allow adult", Filter{AllowAdult: true}, Title{Adult: true, Genres: []string{"Drama"}}, true},
	}
	for _, c := range cases {
		if got := c.f.Allows(c.t, nil); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestAllowsFetchesOnlyWhenItMatters(t *testing.T) {
	cases := []struct {
		name  string
		f     Filter
		t     Title
		fetch bool
		want  bool
	}{
		{"no network rule", Filter{Genres: Rule{Include: []string{"Drama"}}}, Title{Genres: []string{"Drama"}}, false, true},
		{"another part is positive", Filter{Languages: Rule{Include: []string{"ko"}}, Networks: Rule{Include: []string{"netflix"}}},
			Title{Languages: []string{"ko"}}, false, true},
		{"exclude-only after a rejection", Filter{Languages: Rule{Exclude: []string{"ko"}}, Networks: Rule{Exclude: []string{"netflix"}}},
			Title{Languages: []string{"ko"}}, false, false},
		{"an include can still pass", Filter{Countries: Rule{Exclude: []string{"KR"}}, Networks: Rule{Include: []string{"netflix"}}},
			Title{Countries: []string{"KR"}}, true, true},
		{"an exclude can still reject", Filter{Networks: Rule{Exclude: []string{"netflix"}}}, Title{}, true, false},
		{"unknown countries of a film", Filter{Countries: Rule{Include: []string{"KR"}}}, Title{}, true, true},
	}
	for _, c := range cases {
		fetched := false
		got := c.f.Allows(c.t, func() Title {
			fetched = true
			return Title{Countries: []string{"KR"}, Networks: []string{"netflix"}}
		})
		if fetched != c.fetch || got != c.want {
			t.Errorf("%s: fetched %v allowed %v", c.name, fetched, got)
		}
	}
}

func TestNetworksOf(t *testing.T) {
	if got := NetworksOf([]int{213, 999}, true); len(got) != 1 || got[0] != "netflix" {
		t.Fatal(got)
	}
	if got := NetworksOf([]int{8}, true); got == nil || len(got) != 0 {
		t.Fatal("a provider id matched a series network", got)
	}
	if got := NetworksOf([]int{337, 1899}, false); len(got) != 2 || got[0] != "disney_plus" || got[1] != "hbo_max" {
		t.Fatal(got)
	}
}

func TestOptions(t *testing.T) {
	cs, ls := Countries(), Languages()
	if len(cs) != 249 || len(ls) != 183 {
		t.Fatalf("countries %d languages %d", len(cs), len(ls))
	}
	for _, o := range append(cs, ls...) {
		if o.Name == "" || o.Name == o.ID {
			t.Errorf("no English name for %s", o.ID)
		}
	}
	names := map[string]string{}
	for _, o := range append(cs, ls...) {
		names[o.ID] = o.Name
	}
	if names["KR"] != "South Korea" || names["ko"] != "Korean" || names["fa"] != "Persian" {
		t.Fatal(names["KR"], names["ko"], names["fa"])
	}
}

func lookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestLegacyKeys(t *testing.T) {
	f := FromLookup(lookup(map[string]string{
		"TRENDING_GENRES": "Drama, Comedy", "TRENDING_EXCLUDE_GENRES": "Horror", "TRENDING_ALLOW_ADULT": "true",
	}))
	if len(f.Genres.Include) != 2 || f.Genres.Include[1] != "Comedy" || f.Genres.Exclude[0] != "Horror" || !f.AllowAdult {
		t.Fatalf("%+v", f)
	}
	// A saved new key wins, even when it is empty.
	f = FromLookup(lookup(map[string]string{"TRENDING_GENRES": "Drama", KeyGenres: "", KeyCountries: "KR,JP"}))
	if len(f.Genres.Include) != 0 || len(f.Countries.Include) != 2 {
		t.Fatalf("%+v", f)
	}
}

func TestSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("TRENDING_GENRES=Drama\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Server{EnvPath: path}
	mux := http.NewServeMux()
	s.Mount(mux, "/panel", func(h http.HandlerFunc) http.HandlerFunc { return h })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var view View
	getJSON(t, srv.URL+"/panel/auto-filter", &view)
	if !view.Configured || view.Filter.Genres.Include[0] != "Drama" || view.Options.GenresSource != "default" ||
		len(view.Options.Countries) != 249 || len(view.Options.Networks) != len(Networks) {
		t.Fatalf("%+v", view)
	}

	body := `{"allow_adult":false,"countries":{"include":[],"exclude":["kr"]},"networks":{"include":["netflix"],"exclude":[]}}`
	res, err := http.Post(srv.URL+"/panel/auto-filter", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatal(res.Status)
	}
	f := Load(path)
	if len(f.Genres.Include) != 0 || f.Countries.Exclude[0] != "KR" || f.Networks.Include[0] != "netflix" {
		t.Fatalf("%+v", f)
	}

	for _, bad := range []string{
		`{"countries":{"include":["XX"]}}`,
		`{"networks":{"include":["nope"]}}`,
		`{"languages":{"include":["ko"],"exclude":["KO"]}}`,
		`{"genres":{"include":["a,b"]}}`,
	} {
		res, err := http.Post(srv.URL+"/panel/auto-filter", "application/json", strings.NewReader(bad))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %s", bad, res.Status)
		}
	}
}

func getJSON(t *testing.T, url string, into any) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(into); err != nil {
		t.Fatal(err)
	}
}
