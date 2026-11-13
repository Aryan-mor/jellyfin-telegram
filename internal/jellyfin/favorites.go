package jellyfin

import (
	"context"
	"net/url"
	"strconv"
)

// SavedTitles is the set of films and series a Jellyfin user has marked as favorite.
// A favorite episode counts as its whole series.
type SavedTitles struct {
	Movies map[int]bool
	Series map[int]bool
}

// Favorites reads favorite films, series and episodes for every Jellyfin user.
func (c *Client) Favorites(ctx context.Context) (SavedTitles, error) {
	out := SavedTitles{Movies: map[int]bool{}, Series: map[int]bool{}}
	if c == nil {
		return out, nil
	}
	var users []struct {
		ID string `json:"Id"`
	}
	if err := c.do(ctx, "GET", "/Users", nil, &users); err != nil {
		return out, err
	}
	seenSeries := map[string]bool{}
	var seriesIDs []string
	var userID string
	for _, user := range users {
		if userID == "" {
			userID = user.ID
		}
		q := url.Values{
			"Recursive":        {"true"},
			"Filters":          {"IsFavorite"},
			"IncludeItemTypes": {"Movie,Series,Episode"},
			"Fields":           {"ProviderIds"},
			"Limit":            {"500"},
		}
		var page struct {
			Items []Item `json:"Items"`
		}
		if err := c.do(ctx, "GET", "/Users/"+user.ID+"/Items", q, &page); err != nil {
			return out, err
		}
		for _, it := range page.Items {
			switch it.Type {
			case "Movie":
				if id := tmdbOf(it); id > 0 {
					out.Movies[id] = true
				}
			case "Series":
				if id := tmdbOf(it); id > 0 {
					out.Series[id] = true
				}
			case "Episode":
				if it.SeriesID != "" && !seenSeries[it.SeriesID] {
					seenSeries[it.SeriesID] = true
					seriesIDs = append(seriesIDs, it.SeriesID)
				}
			}
		}
	}
	for _, id := range seriesIDs {
		series, err := c.Item(ctx, id, userID)
		if err != nil {
			continue
		}
		if n := tmdbOf(series); n > 0 {
			out.Series[n] = true
		}
	}
	return out, nil
}

func tmdbOf(it Item) int {
	n, _ := strconv.Atoi(it.ProviderIDs["Tmdb"])
	return n
}
