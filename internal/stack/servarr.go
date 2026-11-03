package stack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// App is one Radarr or Sonarr reached over HTTP.
type App struct {
	// Kind is "radarr" or "sonarr".
	Kind string
	Base string
	Key  string
	HTTP *http.Client
}

// Setup is the desired configuration. Calling it again does not create a second copy.
type Setup struct {
	Root  string
	CapMB float64
	// RuntimeMin is the longest running time the cap must hold for. Quality limits are megabytes per
	// minute of runtime, in both Radarr and Sonarr.
	RuntimeMin   float64
	DownloadHost string
	DownloadPort int
	DownloadUser string
	DownloadPass string
	Category     string
	IndexerURL   string
	URLBase      string
	Username     string
	Password     string
}

// Profile is the quality profile Seerr should use.
type Profile struct {
	ID   int
	Name string
}

// Configure applies root folder, quality limits, the download client and Bitmagnet.
func (a App) Configure(ctx context.Context, s Setup) (Profile, error) {
	if a.HTTP == nil {
		a.HTTP = &http.Client{Timeout: 2 * time.Minute}
	}
	if err := a.host(ctx, s); err != nil {
		return Profile{}, err
	}
	if err := a.root(ctx, s.Root); err != nil {
		return Profile{}, err
	}
	profile, err := a.profiles(ctx)
	if err != nil {
		return Profile{}, err
	}
	if err := a.sizes(ctx, s.CapMB, s.RuntimeMin); err != nil {
		return Profile{}, err
	}
	if err := a.downloadClient(ctx, s); err != nil {
		return Profile{}, err
	}
	if err := a.indexer(ctx, s.IndexerURL); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func (a App) host(ctx context.Context, s Setup) error {
	var host map[string]any
	if err := a.call(ctx, http.MethodGet, "/api/v3/config/host", nil, &host); err != nil {
		return err
	}
	host["urlBase"] = s.URLBase
	host["authenticationMethod"] = "forms"
	host["authenticationRequired"] = "enabled"
	host["username"] = s.Username
	host["password"] = s.Password
	host["passwordConfirmation"] = s.Password
	return a.call(ctx, http.MethodPut, "/api/v3/config/host", host, nil)
}

func (a App) root(ctx context.Context, path string) error {
	var folders []map[string]any
	if err := a.call(ctx, http.MethodGet, "/api/v3/rootfolder", nil, &folders); err != nil {
		return err
	}
	for _, f := range folders {
		if f["path"] == path {
			return nil
		}
	}
	return a.call(ctx, http.MethodPost, "/api/v3/rootfolder", map[string]any{"path": path}, nil)
}

func (a App) profiles(ctx context.Context) (Profile, error) {
	var list []map[string]any
	if err := a.call(ctx, http.MethodGet, "/api/v3/qualityprofile", nil, &list); err != nil {
		return Profile{}, err
	}
	var picked Profile
	for _, profile := range list {
		profile["upgradeAllowed"] = false
		disallowHeavy(profile["items"])
		id := int(number(profile["id"]))
		if err := a.call(ctx, http.MethodPut, fmt.Sprintf("/api/v3/qualityprofile/%d", id), profile, nil); err != nil {
			return Profile{}, err
		}
		name, _ := profile["name"].(string)
		if picked.ID == 0 || preferProfile(name, picked.Name) {
			picked = Profile{ID: id, Name: name}
		}
	}
	if picked.ID == 0 {
		return Profile{}, fmt.Errorf("%s has no quality profile", a.Kind)
	}
	return picked, nil
}

func preferProfile(name, current string) bool {
	score := func(s string) int {
		n := strings.ToLower(s)
		switch {
		case strings.Contains(n, "1080"):
			return 3
		case n == "any" || n == "hd":
			return 2
		default:
			return 1
		}
	}
	return score(name) > score(current)
}

func (a App) sizes(ctx context.Context, capMB, runtimeMin float64) error {
	if capMB <= 0 {
		return nil
	}
	if runtimeMin <= 0 {
		runtimeMin = 60
	}
	var items []map[string]any
	if err := a.call(ctx, http.MethodGet, "/api/v3/qualitydefinition", nil, &items); err != nil {
		return err
	}
	// maxSize is megabytes per minute. The cap is always written, so a Premium sign-in raises it again.
	limit := capMB / runtimeMin
	for _, item := range items {
		item["maxSize"] = limit
		if preferred := number(item["preferredSize"]); preferred > limit {
			item["preferredSize"] = limit
		}
		if number(item["minSize"]) > limit {
			item["minSize"] = 0
		}
	}
	err := a.call(ctx, http.MethodPut, "/api/v3/qualitydefinition/update", items, nil)
	if err != nil && strings.Contains(err.Error(), "404") {
		return a.call(ctx, http.MethodPut, "/api/v3/qualitydefinition", items, nil)
	}
	return err
}

func (a App) downloadClient(ctx context.Context, s Setup) error {
	var list []map[string]any
	if err := a.call(ctx, http.MethodGet, "/api/v3/downloadclient", nil, &list); err != nil {
		return err
	}
	for _, item := range list {
		if item["name"] == "qBittorrent" {
			return nil
		}
	}
	fields := []map[string]any{
		{"name": "host", "value": s.DownloadHost},
		{"name": "port", "value": s.DownloadPort},
		{"name": "useSsl", "value": false},
		{"name": "username", "value": s.DownloadUser},
		{"name": "password", "value": s.DownloadPass},
		{"name": "initialState", "value": 0},
		{"name": "removeCompletedDownloads", "value": true},
		{"name": "removeFailedDownloads", "value": true},
	}
	if a.Kind == "sonarr" {
		fields = append(fields,
			map[string]any{"name": "tvCategory", "value": s.Category},
			map[string]any{"name": "recentTvPriority", "value": 0},
			map[string]any{"name": "olderTvPriority", "value": 0},
		)
	} else {
		fields = append(fields, map[string]any{"name": "movieCategory", "value": s.Category})
	}
	body := map[string]any{
		"enable": true, "protocol": "torrent", "priority": 1, "name": "qBittorrent",
		"implementation": "QBittorrent", "configContract": "QBittorrentSettings",
		"removeCompletedDownloads": true, "removeFailedDownloads": true,
		"fields": fields,
	}
	return a.call(ctx, http.MethodPost, "/api/v3/downloadclient", body, nil)
}

func (a App) indexer(ctx context.Context, rawURL string) error {
	var list []map[string]any
	if err := a.call(ctx, http.MethodGet, "/api/v3/indexer", nil, &list); err != nil {
		return err
	}
	var id int
	var enabled bool
	for _, item := range list {
		if item["name"] == "Bitmagnet" {
			id = int(number(item["id"]))
			enabled, _ = item["enable"].(bool)
		}
	}
	if id == 0 {
		body := indexerBody(a.Kind, rawURL, false)
		err := a.call(ctx, http.MethodPost, "/api/v3/indexer", body, &idWrap{into: &id})
		if err != nil {
			// A new Bitmagnet has an empty index, so the test fails. forceSave skips it.
			if err2 := a.call(ctx, http.MethodPost, "/api/v3/indexer?forceSave=true", body, &idWrap{into: &id}); err2 != nil {
				return err
			}
		}
	}
	if enabled {
		return nil
	}
	body := indexerBody(a.Kind, rawURL, true)
	body["id"] = id
	return a.call(ctx, http.MethodPut, fmt.Sprintf("/api/v3/indexer/%d?forceSave=true", id), body, nil)
}

type idWrap struct{ into *int }

func (w *idWrap) UnmarshalJSON(raw []byte) error {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return err
	}
	*w.into = int(number(obj["id"]))
	return nil
}

func indexerBody(kind, rawURL string, enable bool) map[string]any {
	categories := []int{2000, 2010, 2020, 2030, 2040, 2045, 2050, 2060}
	if kind == "sonarr" {
		categories = []int{5000, 5010, 5020, 5030, 5040, 5045, 5050, 5060}
	}
	return map[string]any{
		"enable": enable, "protocol": "torrent", "priority": 25, "name": "Bitmagnet",
		"implementation": "Torznab", "implementationName": "Torznab", "configContract": "TorznabSettings",
		"fields": []map[string]any{
			{"name": "baseUrl", "value": rawURL},
			{"name": "apiPath", "value": "/api"},
			{"name": "apiKey", "value": "bitmagnet"},
			{"name": "categories", "value": categories},
			{"name": "minimumSeeders", "value": 1},
		},
	}
}

func disallowHeavy(v any) {
	switch n := v.(type) {
	case map[string]any:
		if heavy(fmt.Sprint(n["name"])) {
			n["allowed"] = false
		}
		if q, ok := n["quality"].(map[string]any); ok && heavy(fmt.Sprint(q["name"])) {
			n["allowed"] = false
		}
		for _, child := range n {
			disallowHeavy(child)
		}
	case []any:
		for _, child := range n {
			disallowHeavy(child)
		}
	}
}

func heavy(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "2160") || strings.Contains(n, "remux") || n == "4k"
}

func number(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	default:
		return 0
	}
}

func (a App) call(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.Base, "/")+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", a.Key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := a.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		text := strings.TrimSpace(string(payload))
		if len(text) > 240 {
			text = text[:240]
		}
		return fmt.Errorf("%s %s: status %d (%s)", method, path, res.StatusCode, text)
	}
	if out != nil && len(payload) > 0 {
		if u, ok := out.(*idWrap); ok {
			return u.UnmarshalJSON(payload)
		}
		if err := json.Unmarshal(payload, out); err != nil {
			return err
		}
	}
	return nil
}

// ProbeBase returns the first base URL that answers /ping.
func ProbeBase(ctx context.Context, port int, urlBase string) (string, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	var last error
	for _, base := range []string{
		fmt.Sprintf("http://127.0.0.1:%d%s", port, urlBase),
		fmt.Sprintf("http://127.0.0.1:%d", port),
	} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/ping", nil)
		if err != nil {
			return "", err
		}
		res, err := client.Do(req)
		if err != nil {
			last = err
			continue
		}
		res.Body.Close()
		if res.StatusCode < 500 {
			return base, nil
		}
		last = fmt.Errorf("ping %s: %s", base, res.Status)
	}
	if last == nil {
		last = fmt.Errorf("nothing answered on port %d", port)
	}
	return "", last
}
