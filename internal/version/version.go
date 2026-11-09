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

// Channel is "stable" or "nightly", set at build time next to Version.
// A stable install only hears about stable releases. A nightly install only hears about nightlies.
var Channel = "stable"

// Repo is the GitHub repository whose releases are checked.
const Repo = "Aryan-mor/jellyfin-telegram"

const cacheFor = 15 * time.Minute

// Info is what the panel shows.
type Info struct {
	Current   string `json:"current"`
	Channel   string `json:"channel,omitempty"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	URL       string `json:"url,omitempty"`
	Notes     string `json:"notes,omitempty"`
}

// Asset is one file attached to a GitHub release.
type Asset struct {
	Name string
	URL  string
}

// Release is one GitHub release, newest-or-not. Choose picks the one this install should follow.
type Release struct {
	Tag    string
	URL    string
	Notes  string
	Pre    bool
	Assets []Asset
}

var (
	mu      sync.Mutex
	cached  Info
	fetched time.Time
	// Fetch loads the release this channel should follow. Tests replace it.
	Fetch = fetchLatest
)

// Check returns the running version and the newest release. The answer is cached for 15 minutes,
// and a failed lookup (offline, rate limited) simply reports no update.
func Check(ctx context.Context) Info {
	info := Info{Current: Version, Channel: Channel}
	if Version == "dev" {
		return info
	}
	mu.Lock()
	defer mu.Unlock()
	if time.Since(fetched) < cacheFor {
		cached.Current = Version
		cached.Channel = Channel
		return cached
	}
	rel, err := Fetch(ctx)
	fetched = time.Now()
	if err != nil {
		cached = info
		return cached
	}
	info.Latest = strings.TrimPrefix(rel.Tag, "v")
	info.URL = rel.URL
	info.Notes = rel.Notes
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
	nightly := 0
	hasNightly := false
	if base, n, ok := strings.Cut(v, "-nightly."); ok {
		num, err := strconv.Atoi(n)
		if err != nil || num < 1 || strings.Contains(base, "-") {
			return nil
		}
		v = base
		nightly = num
		hasNightly = true
	} else if strings.Contains(v, "-") {
		return nil
	}
	var out []int
	for _, s := range strings.Split(v, ".") {
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	if hasNightly {
		out = append(out, nightly)
	}
	return out
}

// Choose returns the newest release on this channel. Stable ignores prereleases.
// Nightly ignores everything that is not a nightly prerelease.
func Choose(channel string, list []Release) (Release, bool) {
	var best Release
	found := false
	for _, rel := range list {
		if channel == "nightly" {
			if !rel.Pre || parts(rel.Tag) == nil || !strings.Contains(rel.Tag, "-nightly.") {
				continue
			}
		} else if rel.Pre || strings.Contains(rel.Tag, "-nightly.") || parts(rel.Tag) == nil {
			continue
		}
		if !found || Newer(rel.Tag, best.Tag) {
			best = rel
			found = true
		}
	}
	return best, found
}

func fetchLatest(ctx context.Context) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+Repo+"/releases?per_page=30", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "jellyfin-telegram")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("github answered %d", res.StatusCode)
	}
	var body []struct {
		Tag    string `json:"tag_name"`
		URL    string `json:"html_url"`
		Body   string `json:"body"`
		Pre    bool   `json:"prerelease"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return Release{}, err
	}
	list := make([]Release, 0, len(body))
	for _, rel := range body {
		notes := rel.Body
		if len(notes) > 1500 {
			notes = notes[:1500]
		}
		assets := make([]Asset, 0, len(rel.Assets))
		for _, asset := range rel.Assets {
			assets = append(assets, Asset{Name: asset.Name, URL: asset.URL})
		}
		list = append(list, Release{Tag: rel.Tag, URL: rel.URL, Notes: notes, Pre: rel.Pre, Assets: assets})
	}
	chosen, ok := Choose(Channel, list)
	if !ok {
		return Release{}, fmt.Errorf("no %s release", Channel)
	}
	return chosen, nil
}
