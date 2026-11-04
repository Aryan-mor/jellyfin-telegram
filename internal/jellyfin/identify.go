package jellyfin

import (
	"context"
	"strings"
	"unicode"
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
	return pickTMDB(title, results)
}

func pickTMDB(title string, results []SearchResult) (int, bool, error) {
	want := fold(title)
	found := map[string]bool{}
	for _, r := range results {
		if id := r.ProviderIDs["Tmdb"]; id != "" && fold(r.Name) == want {
			found[id] = true
		}
	}
	if len(found) != 1 {
		return 0, false, nil
	}
	for id := range found {
		n := 0
		for _, r := range id {
			if r < '0' || r > '9' {
				return 0, false, nil
			}
			n = n*10 + int(r-'0')
		}
		return n, n > 0, nil
	}
	return 0, false, nil
}

func fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
