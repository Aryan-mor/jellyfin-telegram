// Package version knows which build is running and whether a newer release exists on GitHub.
package version

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Version is set at build time: -ldflags "-X .../internal/version.Version=1.0.42".
// A build from source has "dev" and never reports an update.
var Version = "dev"

// Repo is the GitHub repository whose releases are checked.
const Repo = "Aryan-mor/jellyfin-telegram"

const cacheFor = 6 * time.Hour

// Info is what the panel shows.
type Info struct {
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	URL       string `json:"url,omitempty"`
	Notes     string `json:"notes,omitempty"`
}

var (
	mu      sync.Mutex
	cached  Info
	fetched time.Time
	// Fetch can be replaced in tests.
	Fetch = fetchLatest
)

// Check returns the running version and the newest release. The answer is cached for six hours,
// and a failed lookup (offline, rate limited) simply reports no update.
func Check(ctx context.Context) Info {
	info := Info{Current: Version}
	if Version == "dev" {
		return info
	}
	mu.Lock()
	defer mu.Unlock()
	if time.Since(fetched) < cacheFor {
		cached.Current = Version
		return cached
	}
	tag, url, notes, err := Fetch(ctx)
	fetched = time.Now()
	if err != nil {
		cached = info
		return cached
	}
	info.Latest = strings.TrimPrefix(tag, "v")
	info.URL = url
	info.Notes = notes
	info.Available = Newer(info.Latest, Version)
	cached = info
	return info
}

// Newer reports whether a is a later dotted version than b. Anything unreadable is not newer.
func Newer(a, b string) bool {
	pa, pb := parts(a), parts(b)
	if pa == nil || pb == nil {
		return false
	}
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func parts(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	var out []int
	for _, s := range strings.Split(v, ".") {
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

func fetchLatest(ctx context.Context) (tag, url, notes string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+Repo+"/releases/latest", nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "jellyfin-telegram")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("github answered %d", res.StatusCode)
	}
	var body struct {
		Tag  string `json:"tag_name"`
		URL  string `json:"html_url"`
		Body string `json:"body"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return "", "", "", err
	}
	if len(body.Body) > 1500 {
		body.Body = body.Body[:1500]
	}
	return body.Tag, body.URL, body.Body, nil
}
