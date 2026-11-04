// Package jellyfin talks to the Jellyfin HTTP API.
package jellyfin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/lang"
)

// Client is a minimal Jellyfin API client.
type Client struct {
	base string
	key  string
	http *http.Client
}

// New returns a Client.
func New(baseURL, apiKey string) *Client {
	return &Client{base: baseURL, key: apiKey, http: &http.Client{Timeout: 3 * time.Minute}}
}

// Source is one playable version of a library item.
type Source struct {
	ID   string `json:"Id"`
	Path string `json:"Path"`
}

// Item is a movie or an episode. Versions of one title share an item, and each version
// is listed in Sources.
type Item struct {
	ID          string            `json:"Id"`
	Path        string            `json:"Path"`
	Type        string            `json:"Type"`
	Name        string            `json:"Name"`
	Year        int               `json:"ProductionYear"`
	SeriesID    string            `json:"SeriesId"`
	SeriesName  string            `json:"SeriesName"`
	Season      int               `json:"ParentIndexNumber"`
	Episode     int               `json:"IndexNumber"`
	ProviderIDs map[string]string `json:"ProviderIds"`
	Sources     []Source          `json:"MediaSources"`
}

// IMDb returns the IMDb id of an item, which may be empty.
func (i Item) IMDb() string { return i.ProviderIDs["Imdb"] }

// ProbeResult summarises a PlaybackInfo answer for one item.
type ProbeResult struct {
	Streams      int
	RunTimeTicks int64
}

// Version returns the server version, which also proves the key works.
func (c *Client) Version(ctx context.Context) (string, error) {
	var out struct {
		Version string `json:"Version"`
	}
	err := c.do(ctx, http.MethodGet, "/System/Info", nil, &out)
	return out.Version, err
}

// AdminUserID returns an administrator, since item queries need a user.
func (c *Client) AdminUserID(ctx context.Context) (string, error) {
	var users []struct {
		ID     string `json:"Id"`
		Policy struct {
			IsAdministrator bool `json:"IsAdministrator"`
		} `json:"Policy"`
	}
	if err := c.do(ctx, http.MethodGet, "/Users", nil, &users); err != nil {
		return "", err
	}
	for _, u := range users {
		if u.Policy.IsAdministrator {
			return u.ID, nil
		}
	}
	return "", errors.New("no administrator found")
}

// EnsureAdmin makes a Jellyfin administrator that can sign in with password. Seerr's first-run
// setup accepts a user and a password, not an API key.
func (c *Client) EnsureAdmin(ctx context.Context, name, password string) error {
	var users []struct {
		ID     string          `json:"Id"`
		Name   string          `json:"Name"`
		Policy json.RawMessage `json:"Policy"`
	}
	if err := c.do(ctx, http.MethodGet, "/Users", nil, &users); err != nil {
		return err
	}
	var id string
	var policy json.RawMessage
	for _, u := range users {
		if strings.EqualFold(u.Name, name) {
			id, policy = u.ID, u.Policy
			break
		}
	}
	if id == "" {
		var created struct {
			ID     string          `json:"Id"`
			Policy json.RawMessage `json:"Policy"`
		}
		if err := c.post(ctx, "/Users/New", map[string]string{"Name": name, "Password": password}, &created); err != nil {
			return err
		}
		id, policy = created.ID, created.Policy
	} else if err := c.post(ctx, "/Users/"+url.PathEscape(id)+"/Password", map[string]any{
		"Id": id, "NewPw": password, "ResetPassword": false,
	}, nil); err != nil {
		return err
	}
	pol := map[string]any{}
	if len(policy) > 0 {
		if err := json.Unmarshal(policy, &pol); err != nil {
			return err
		}
	}
	pol["IsAdministrator"] = true
	pol["EnableAllFolders"] = true
	return c.post(ctx, "/Users/"+url.PathEscape(id)+"/Policy", pol, nil)
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return c.send(ctx, http.MethodPost, path, nil, raw, out)
}

// RefreshLibrary starts a library scan and returns at once.
func (c *Client) RefreshLibrary(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/Library/Refresh", nil, nil)
}

// Items returns all movies and episodes with their versions.
func (c *Client) Items(ctx context.Context, userID string) ([]Item, error) {
	q := url.Values{
		"userId":           {userID},
		"recursive":        {"true"},
		"includeItemTypes": {"Movie,Episode"},
		"fields":           {"Path,MediaSources,ProviderIds"},
	}
	var out struct {
		Items []Item `json:"Items"`
	}
	err := c.do(ctx, http.MethodGet, "/Items", q, &out)
	return out.Items, err
}

// SubtitleStream is an embedded track Jellyfin would extract on request.
type SubtitleStream struct {
	Index    int // the number Jellyfin shows, which moves when external subtitles are added
	Position int // the place in the video file, which stays fixed
	Codec    string
	Language string
}

// Ext returns the file extension Jellyfin expects in its cache for this track.
func (s SubtitleStream) Ext() string {
	switch strings.ToLower(s.Codec) {
	case "ass", "ssa":
		return strings.ToLower(s.Codec)
	case "pgssub":
		return "sup"
	}
	return "srt"
}

// SubtitleInfo lists the tracks of one media source that extraction would cover.
type SubtitleInfo struct {
	Extractable []SubtitleStream
}

// SubtitleStreams reads the embedded tracks of a media source.
func (c *Client) SubtitleStreams(ctx context.Context, sourceID, userID string) (SubtitleInfo, error) {
	var out struct {
		MediaSources []struct {
			ID           string `json:"Id"`
			MediaStreams []struct {
				Type                   string `json:"Type"`
				Index                  int    `json:"Index"`
				Codec                  string `json:"Codec"`
				Language               string `json:"Language"`
				IsExternal             bool   `json:"IsExternal"`
				IsTextSubtitleStream   bool   `json:"IsTextSubtitleStream"`
				SupportsExternalStream bool   `json:"SupportsExternalStream"`
			} `json:"MediaStreams"`
		} `json:"MediaSources"`
	}
	q := url.Values{"userId": {userID}}
	if err := c.do(ctx, http.MethodGet, "/Items/"+url.PathEscape(sourceID)+"/PlaybackInfo", q, &out); err != nil {
		return SubtitleInfo{}, err
	}
	for _, src := range out.MediaSources {
		if src.ID != sourceID {
			continue
		}
		external := 0
		for _, m := range src.MediaStreams {
			if m.IsExternal {
				external++
			}
		}
		var info SubtitleInfo
		for _, m := range src.MediaStreams {
			pgs := strings.EqualFold(m.Codec, "pgssub")
			if m.Type != "Subtitle" || m.IsExternal || !m.SupportsExternalStream || (!m.IsTextSubtitleStream && !pgs) {
				continue
			}
			info.Extractable = append(info.Extractable, SubtitleStream{
				Index: m.Index, Position: m.Index - external, Codec: m.Codec, Language: lang.Normalize(m.Language),
			})
		}
		return info, nil
	}
	return SubtitleInfo{}, nil
}

// ProgramDataPath returns Jellyfin's data folder, as the server sees it.
func (c *Client) ProgramDataPath(ctx context.Context) (string, error) {
	var out struct {
		Path string `json:"ProgramDataPath"`
	}
	err := c.do(ctx, http.MethodGet, "/System/Info", nil, &out)
	return out.Path, err
}

// Item returns one item, such as the series of an episode.
func (c *Client) Item(ctx context.Context, itemID, userID string) (Item, error) {
	var out Item
	q := url.Values{"userId": {userID}}
	err := c.do(ctx, http.MethodGet, "/Items/"+url.PathEscape(itemID), q, &out)
	return out, err
}

// UploadSubtitle stores a subtitle with the official API. Jellyfin keeps it in the metadata
// folder of that one item when the library has SaveSubtitlesWithMedia off.
func (c *Client) UploadSubtitle(ctx context.Context, itemID, language, format, text string) error {
	body, err := json.Marshal(map[string]any{
		"Language":          language,
		"Format":            format,
		"IsForced":          false,
		"IsHearingImpaired": false,
		"Data":              base64.StdEncoding.EncodeToString([]byte(text)),
	})
	if err != nil {
		return err
	}
	return c.send(ctx, http.MethodPost, "/Videos/"+url.PathEscape(itemID)+"/Subtitles", nil, body, nil)
}

// SearchResult is one online match for a title.
type SearchResult struct {
	Name        string            `json:"Name"`
	Year        int               `json:"ProductionYear"`
	ProviderIDs map[string]string `json:"ProviderIds"`
}

// SearchMovie asks Jellyfin's own online providers for a title. Jellyfin already holds the
// TMDB access, so no key is needed here.
func (c *Client) SearchMovie(ctx context.Context, name string) ([]SearchResult, error) {
	var out []SearchResult
	err := c.post(ctx, "/Items/RemoteSearch/Movie", map[string]any{
		"SearchInfo": map[string]any{"Name": name},
		"ItemId":     "00000000000000000000000000000000",
	}, &out)
	return out, err
}

// RefreshMetadata makes Jellyfin read an item again and replace what it holds. Jellyfin skips a
// locked item, and an earlier local .nfo set that lock, so the lock is lifted first.
func (c *Client) RefreshMetadata(ctx context.Context, itemID, userID string) error {
	var dto map[string]json.RawMessage
	q := url.Values{"userId": {userID}}
	if err := c.do(ctx, http.MethodGet, "/Items/"+url.PathEscape(itemID), q, &dto); err != nil {
		return err
	}
	if string(dto["LockData"]) == "true" {
		dto["LockData"] = json.RawMessage("false")
		raw, err := json.Marshal(dto)
		if err != nil {
			return err
		}
		if err := c.send(ctx, http.MethodPost, "/Items/"+url.PathEscape(itemID), nil, raw, nil); err != nil {
			return err
		}
	}
	return c.send(ctx, http.MethodPost, "/Items/"+url.PathEscape(itemID)+"/Refresh", url.Values{
		"metadataRefreshMode": {"FullRefresh"},
		"imageRefreshMode":    {"FullRefresh"},
		"replaceAllMetadata":  {"true"},
		"replaceAllImages":    {"true"},
	}, nil, nil)
}

// Probe asks for playback information. A .strm item is probed by this call, not by a
// library scan or a metadata refresh.
func (c *Client) Probe(ctx context.Context, itemID, userID string) (ProbeResult, error) {
	var out struct {
		MediaSources []struct {
			ID           string            `json:"Id"`
			RunTimeTicks int64             `json:"RunTimeTicks"`
			MediaStreams []json.RawMessage `json:"MediaStreams"`
		} `json:"MediaSources"`
	}
	q := url.Values{"userId": {userID}}
	if err := c.do(ctx, http.MethodGet, "/Items/"+url.PathEscape(itemID)+"/PlaybackInfo", q, &out); err != nil {
		return ProbeResult{}, err
	}
	for _, s := range out.MediaSources {
		if s.ID == itemID {
			return ProbeResult{Streams: len(s.MediaStreams), RunTimeTicks: s.RunTimeTicks}, nil
		}
	}
	return ProbeResult{}, nil
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, out any) error {
	return c.send(ctx, method, path, q, nil, out)
}

func (c *Client) send(ctx context.Context, method, path string, q url.Values, body []byte, out any) error {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", fmt.Sprintf(`MediaBrowser Client="jellyfin-telegram", Device="service", DeviceId="jellyfin-telegram", Version="1", Token=%q`, c.key))
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, res.Body)
		return fmt.Errorf("%s %s: status %d", method, path, res.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}
