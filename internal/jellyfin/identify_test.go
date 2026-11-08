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
		{"turkish marks are the same name", []SearchResult{tm("Evlilik Güzeldir", "329662")}, 329662},
		{"a remake with the same title is ambiguous", []SearchResult{tm("Dune", "1"), tm("Dune", "2")}, 0},
		{"no results", nil, 0},
	}
	for _, c := range cases {
		query := "The Love Hypothesis"
		if c.name == "a remake with the same title is ambiguous" {
			query = "Dune"
		}
		if c.name == "turkish marks are the same name" {
			query = "Evlilik Guzeldir"
		}
		got, ok, err := pickTMDB(query, c.results)
		if err != nil || got != c.want || ok != (c.want > 0) {
			t.Errorf("%s: got %d %v %v", c.name, got, ok, err)
		}
	}
}

// What Jellyfin really answered for "The Hardacres": the TMDB entry has a Persian name, and the
// entry with the English name has only an IMDb id. They are one show from one year.
func TestPickMetaJoinsEntriesOfTheSameYear(t *testing.T) {
	results := []SearchResult{
		{Name: "\u062e\u0627\u0646\u0648\u0627\u062f\u0647 \u0647\u0627\u0631\u062f\u06cc\u06a9\u0631\u0632", Year: 2024, ProviderIDs: map[string]string{"Tmdb": "231751"}},
		{Name: "The Hardacres", Year: 2024, ProviderIDs: map[string]string{"Imdb": "tt28489780"}},
	}
	id, imdb, ok, err := pickMeta("The Hardacres", results)
	if err != nil || !ok || id != 231751 || imdb != "tt28489780" {
		t.Fatalf("got %d %q %v %v", id, imdb, ok, err)
	}
	// Two different shows in that year with TMDB ids are still ambiguous.
	results = append(results, SearchResult{Name: "Another Show", Year: 2024, ProviderIDs: map[string]string{"Tmdb": "42"}})
	if _, _, ok, _ := pickMeta("The Hardacres", results); ok {
		t.Fatal("a second show of the same year was ignored")
	}
	// A show of another year is not joined to the title.
	results = []SearchResult{
		{Name: "The Hardacres", Year: 2024, ProviderIDs: map[string]string{"Imdb": "tt1"}},
		{Name: "Something Else", Year: 1999, ProviderIDs: map[string]string{"Tmdb": "7"}},
	}
	if _, _, ok, _ := pickMeta("The Hardacres", results); ok {
		t.Fatal("an entry of another year was taken")
	}
	// Without the title anywhere, nothing is guessed.
	if _, _, ok, _ := pickMeta("The Hardacres", []SearchResult{{Name: "Other", Year: 2024, ProviderIDs: map[string]string{"Tmdb": "9"}}}); ok {
		t.Fatal("a different title was taken")
	}
}
