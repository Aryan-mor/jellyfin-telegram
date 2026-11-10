// Package version knows which build is running and whether a newer release exists on GitHub.
package version

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net"
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
	mu     sync.Mutex
	cached Info
	// flight is set while a lookup is running. A second caller waits for that one
	// instead of starting another. The answer is not kept: the next check looks again.
	flight chan struct{}
	// Fetch loads the release this channel should follow. Tests replace it.
	Fetch = fetchLatest
)

// githubClient does not reuse a dead connection. One check every few minutes does not need a pool,
// and a stuck connection used to burn the whole timeout and get stored as "no update".
var githubClient = &http.Client{
	Timeout: 20 * time.Second,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 8 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		ResponseHeaderTimeout: 15 * time.Second,
		DisableKeepAlives:     true,
	},
}

// Check returns the running version and the newest release. Each call looks.
// Two calls at the same moment share one lookup. A failed lookup reports no update
// and does not stick: the next call looks again.
func Check(ctx context.Context) Info {
	info := Info{Current: Version, Channel: Channel}
	if Version == "dev" {
		return info
	}
	mu.Lock()
	if flight != nil {
		wait := flight
		mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
		}
		mu.Lock()
		out := cached
		mu.Unlock()
		out.Current = Version
		out.Channel = Channel
		return out
	}
	done := make(chan struct{})
	flight = done
	mu.Unlock()

	// A closed tab cancels the request. That must not be stored as "no update".
	lookup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	rel, err := Fetch(lookup)
	if err == nil {
		info.Latest = strings.TrimPrefix(rel.Tag, "v")
		info.URL = rel.URL
		info.Notes = rel.Notes
		info.Available = Newer(info.Latest, Version)
	}
	mu.Lock()
	cached = info
	flight = nil
	close(done)
	mu.Unlock()
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
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// The releases atom feed is not the REST API. That API allows 60 anonymous calls an hour
	// for the whole computer, and a 403 was stored as "nothing new", so the panel stayed quiet.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://github.com/"+Repo+"/releases.atom", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("User-Agent", "jellyfin-telegram")
	res, err := githubClient.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("github answered %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return Release{}, err
	}
	list, err := releasesFromFeed(body)
	if err != nil {
		return Release{}, err
	}
	chosen, ok := Choose(Channel, list)
	if !ok {
		return Release{}, fmt.Errorf("no %s release", Channel)
	}
	return chosen, nil
}

func releasesFromFeed(data []byte) ([]Release, error) {
	var feed struct {
		Entries []struct {
			Title   string `xml:"title"`
			Content string `xml:"content"`
			Links   []struct {
				Href string `xml:"href,attr"`
			} `xml:"link"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(data, &feed); err != nil {
		return nil, err
	}
	list := make([]Release, 0, len(feed.Entries))
	for _, entry := range feed.Entries {
		tag := strings.TrimSpace(entry.Title)
		if tag == "" {
			continue
		}
		page := ""
		for _, link := range entry.Links {
			if strings.Contains(link.Href, "/releases/tag/") {
				page = link.Href
				break
			}
		}
		if page == "" {
			page = "https://github.com/" + Repo + "/releases/tag/" + tag
		}
		notes := entry.Content
		if len(notes) > 1500 {
			notes = notes[:1500]
		}
		list = append(list, Release{
			Tag: tag, URL: page, Notes: notes,
			Pre: strings.Contains(tag, "-nightly."), Assets: releaseAssets(tag),
		})
	}
	return list, nil
}

func releaseAssets(tag string) []Asset {
	names := []string{
		"jellyfin-telegram-windows-amd64.zip",
		"jellyfin-telegram-linux-amd64.tar.gz",
		"jellyfin-telegram-linux-arm64.tar.gz",
	}
	out := make([]Asset, 0, len(names))
	for _, name := range names {
		out = append(out, Asset{
			Name: name,
			URL:  "https://github.com/" + Repo + "/releases/download/" + tag + "/" + name,
		})
	}
	return out
}
