package jellyfin

import (
	"context"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Identifier finds the TMDB id of a movie from its title alone, so a file posted without a year
// still gets its online metadata.
type Identifier struct{ Client *Client }

// Movie returns the TMDB id when exactly one TMDB entry carries the title. Two entries with the
// same title, such as a remake, are too ambiguous to pick between.
func (i Identifier) Movie(ctx context.Context, title string) (int, bool, error) {
	results, err := i.Client.SearchMovie(ctx, title)
	if err != nil {
		return 0, false, err
	}
	id, _, ok, err := pickMeta(title, results)
	return id, ok, err
}

// MovieMeta returns the TMDB id and the IMDb id of that same result.
func (i Identifier) MovieMeta(ctx context.Context, title string) (tmdb int, imdb string, ok bool, err error) {
	results, err := i.Client.SearchMovie(ctx, title)
	if err != nil {
		return 0, "", false, err
	}
	return pickMeta(title, results)
}

// Series returns the TMDB id when exactly one series carries the title.
func (i Identifier) Series(ctx context.Context, title string) (int, bool, error) {
	id, _, ok, err := i.SeriesMeta(ctx, title)
	return id, ok, err
}

// SeriesMeta returns the TMDB id and the IMDb id of that same series.
func (i Identifier) SeriesMeta(ctx context.Context, title string) (tmdb int, imdb string, ok bool, err error) {
	results, err := i.Client.SearchSeries(ctx, title)
	if err != nil {
		return 0, "", false, err
	}
	return pickMeta(title, results)
}

func pickTMDB(title string, results []SearchResult) (int, bool, error) {
	id, _, ok, err := pickMeta(title, results)
	return id, ok, err
}

// pickMeta chooses the one TMDB entry that the title names.
//
// Jellyfin asks several providers, and they do not agree on a name: the TMDB entry may carry the
// title in the server's metadata language while another provider has the original title and only
// an IMDb id. Both are the same show when they come from the same year, so an entry counts when it
// has the title itself, or when it has the same year as an entry that does.
func pickMeta(title string, results []SearchResult) (int, string, bool, error) {
	want := fold(title)
	years := map[int]bool{}
	for _, r := range results {
		if fold(r.Name) == want {
			years[r.Year] = true
		}
	}
	if len(years) == 0 {
		return 0, "", false, nil
	}
	found := map[string]bool{}
	imdb := ""
	for _, r := range results {
		sameShow := fold(r.Name) == want || (r.Year != 0 && years[r.Year])
		if !sameShow {
			continue
		}
		if id := r.ProviderIDs["Tmdb"]; id != "" {
			found[id] = true
		}
		if imdb == "" {
			imdb = r.ProviderIDs["Imdb"]
		}
	}
	if len(found) != 1 {
		return 0, "", false, nil
	}
	for id := range found {
		n, err := strconv.Atoi(id)
		if err != nil || n <= 0 {
			return 0, "", false, nil
		}
		return n, imdb, true, nil
	}
	return 0, "", false, nil
}

// fold compares titles so "Evlilik Guzeldir" and "Evlilik Güzeldir" are the same name.
// Marks such as ü and ş are dropped. Letters that are different without a mark still differ.
func fold(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
