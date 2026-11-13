package seerr

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OpenTitle is one requested film or series that is not in the library yet.
type OpenTitle struct {
	TMDB   int
	Series bool
}

// OpenRequests lists Seerr requests that are still missing. A declined request is left out.
func OpenRequests(ctx context.Context, base, key string) ([]OpenTitle, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" || strings.TrimSpace(key) == "" {
		return nil, nil
	}
	c := &client{base: base, key: key, http: &http.Client{Timeout: 20 * time.Second}}
	var out []OpenTitle
	seen := map[string]bool{}
	for page := 0; page < 3; page++ {
		var body struct {
			PageInfo struct {
				Pages int `json:"pages"`
			} `json:"pageInfo"`
			Results []struct {
				Status int `json:"status"`
				Media  struct {
					TMDB   int    `json:"tmdbId"`
					Type   string `json:"mediaType"`
					Status int    `json:"status"`
				} `json:"media"`
			} `json:"results"`
		}
		path := "/api/v1/request?" + url.Values{
			"take": []string{"50"},
			"skip": []string{fmt.Sprint(page * 50)},
		}.Encode()
		if err := c.callOK(ctx, http.MethodGet, path, nil, &body); err != nil {
			return nil, err
		}
		for _, row := range body.Results {
			if row.Status == 3 || row.Media.TMDB <= 0 {
				continue
			}
			// AVAILABLE and BLOCKLISTED need no more copies. DELETED titles stay open.
			if row.Media.Status == 5 || row.Media.Status == 6 {
				continue
			}
			series := row.Media.Type == "tv"
			id := fmt.Sprintf("%t:%d", series, row.Media.TMDB)
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, OpenTitle{TMDB: row.Media.TMDB, Series: series})
		}
		if page+1 >= body.PageInfo.Pages || len(body.Results) == 0 {
			break
		}
	}
	return out, nil
}
