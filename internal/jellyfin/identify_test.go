package jellyfin

import "testing"

func TestPickTMDB(t *testing.T) {
	tm := func(name, id string) SearchResult {
		return SearchResult{Name: name, ProviderIDs: map[string]string{"Tmdb": id}}
	}
	omdb := SearchResult{Name: "The Love Hypothesis", ProviderIDs: map[string]string{"Imdb": "tt1"}}
	cases := []struct {
		name    string
		results []SearchResult
		want    int
	}{
		{"one exact match among providers", []SearchResult{tm("The Love Hypothesis", "1032863"), omdb}, 1032863},
		{"punctuation and case are ignored", []SearchResult{tm("the love hypothesis!", "5")}, 5},
		{"a different title is not taken", []SearchResult{tm("Love Hypothesis 2", "9")}, 0},
		{"a remake with the same title is ambiguous", []SearchResult{tm("Dune", "1"), tm("Dune", "2")}, 0},
		{"no results", nil, 0},
	}
	for _, c := range cases {
		got, ok, err := pickTMDB("The Love Hypothesis", c.results)
		if c.name == "a remake with the same title is ambiguous" {
			got, ok, err = pickTMDB("Dune", c.results)
		}
		if err != nil || got != c.want || ok != (c.want > 0) {
			t.Errorf("%s: got %d %v %v", c.name, got, ok, err)
		}
	}
}
