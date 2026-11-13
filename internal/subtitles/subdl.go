package subtitles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	subdlSearch = "https://api.subdl.com/api/v1/subtitles"
	subdlDL     = "https://dl.subdl.com"
)

// SubDL is a keyed client for subdl.com. Keys rotate so one limited key does not stall the rest.
type SubDL struct {
	http   *http.Client
	keys   []string
	cursor atomic.Uint64
	api    string
	dl     string
}

// NewSubDL returns a client. keys must not be empty.
func NewSubDL(keys []string) *SubDL {
	var clean []string
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k != "" {
			clean = append(clean, k)
		}
	}
	return &SubDL{
		http: &http.Client{Timeout: 30 * time.Second},
		keys: clean,
		api:  subdlSearch,
		dl:   subdlDL,
	}
}

func (c *SubDL) nextKey() string {
	if len(c.keys) == 0 {
		return ""
	}
	i := int(c.cursor.Add(1)-1) % len(c.keys)
	return c.keys[i]
}

// SearchTMDB lists packs for one title. lang is an ISO 639-1 code such as fa or en.
// season and episode are zero for a film.
func (c *SubDL) SearchTMDB(ctx context.Context, tmdb int, series bool, season, episode int, lang string) ([]Pack, error) {
	if c == nil || len(c.keys) == 0 || tmdb <= 0 {
		return nil, nil
	}
	q := url.Values{
		"api_key": {c.nextKey()},
		"tmdb_id": {strconv.Itoa(tmdb)},
	}
	if series {
		q.Set("type", "tv")
		if season > 0 {
			q.Set("season_number", strconv.Itoa(season))
		}
		if episode > 0 {
			q.Set("episode_number", strconv.Itoa(episode))
		}
	} else {
		q.Set("type", "movie")
	}
	code := strings.ToUpper(strings.TrimSpace(lang))
	if code == "" {
		code = "EN"
	}
	q.Set("languages", code)
	raw, err := c.get(ctx, c.api+"?"+q.Encode(), maxAPIBytes)
	if err != nil {
		return nil, err
	}
	var body struct {
		Status    bool   `json:"status"`
		Error     string `json:"error"`
		Subtitles []struct {
			ReleaseName string `json:"release_name"`
			URL         string `json:"url"`
		} `json:"subtitles"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("subdl search: bad response: %w", err)
	}
	if !body.Status {
		msg := strings.TrimSpace(body.Error)
		if msg == "" || strings.Contains(strings.ToLower(msg), "no subtitle") {
			return nil, nil
		}
		return nil, fmt.Errorf("subdl search: %s", msg)
	}
	var packs []Pack
	for _, s := range body.Subtitles {
		tag := subdlTag(s.URL)
		if tag == "" {
			continue
		}
		name := strings.TrimSpace(s.ReleaseName)
		packs = append(packs, Pack{
			Tag: tag, Title: name, Series: series, Releases: []string{name},
		})
	}
	return packs, nil
}

// Download fetches one pack and decodes it for code.
func (c *SubDL) Download(ctx context.Context, tag, code string) ([]SubFile, error) {
	t := strings.TrimSpace(tag)
	if c == nil || t == "" || strings.Contains(t, "?") {
		return nil, fmt.Errorf("subdl fetch: invalid tag")
	}
	q := url.Values{"api_key": {c.nextKey()}}
	raw, err := c.get(ctx, c.dl+t+"?"+q.Encode(), maxZipBytes)
	if err != nil {
		return nil, err
	}
	return ExtractSubs(raw, code)
}

var errSubdlLimited = errors.New("subdl: rate limited")

func (c *SubDL) get(ctx context.Context, target string, limit int64) ([]byte, error) {
	data, err := c.getOnce(ctx, target, limit)
	if err == nil || !errors.Is(err, errSubdlLimited) {
		return data, err
	}
	select {
	case <-time.After(1200 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return c.getOnce(ctx, target, limit)
}

func (c *SubDL) getOnce(ctx context.Context, target string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("subdl: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusTooManyRequests {
		return nil, errSubdlLimited
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("subdl: HTTP %d", res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, limit))
}

// subdlTag drops the API key a result embeds in its download URL.
func subdlTag(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Path == "" || u.Path == "/" {
		return ""
	}
	return u.Path
}
