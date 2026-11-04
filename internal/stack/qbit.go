package stack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// Qbit is the loopback WebUI.
type Qbit struct {
	Base string
	User string
	Pass string
	HTTP *http.Client
}

// Login stores the session cookie on the client.
func (q *Qbit) Login(ctx context.Context) error {
	if q.HTTP == nil {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return err
		}
		q.HTTP = &http.Client{Timeout: time.Minute, Jar: jar}
	}
	form := url.Values{"username": {q.User}, "password": {q.Pass}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.Base+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := q.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	if res.StatusCode >= 300 || !strings.Contains(string(body), "Ok") {
		return fmt.Errorf("qBittorrent login: %s", strings.TrimSpace(string(body)))
	}
	return nil
}

// ApplyLimits stops a torrent at ratio 0 instead of removing it, and skips executable names.
func (q *Qbit) ApplyLimits(ctx context.Context) error {
	prefs := map[string]any{
		"max_ratio_enabled":   true,
		"max_ratio":           0,
		"max_ratio_act":       0,
		"excluded_file_names": excludedFiles,
	}
	raw, _ := json.Marshal(prefs)
	form := url.Values{"json": {string(raw)}}
	return q.form(ctx, "/api/v2/app/setPreferences", form)
}

// EnsureCategory creates a save path under the shared volume so Radarr can hardlink.
func (q *Qbit) EnsureCategory(ctx context.Context, name, savePath string) error {
	form := url.Values{"category": {name}, "savePath": {savePath}}
	err := q.form(ctx, "/api/v2/torrents/createCategory", form)
	if err != nil && strings.Contains(err.Error(), "status 409") {
		// 409 also means "exists already", which is what a second start finds. Editing an unchanged
		// category answers 409 too, so what counts is whether the category is there with this path.
		_ = q.form(ctx, "/api/v2/torrents/editCategory", form)
		if q.hasCategory(ctx, name, savePath) {
			return nil
		}
	}
	return err
}

func (q *Qbit) hasCategory(ctx context.Context, name, savePath string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, q.Base+"/api/v2/torrents/categories", nil)
	if err != nil {
		return false
	}
	res, err := q.HTTP.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	var all map[string]struct {
		SavePath string `json:"savePath"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&all) != nil {
		return false
	}
	got, ok := all[name]
	return ok && got.SavePath == savePath
}

// PauseAll stops every torrent. qBittorrent 5 renamed pause to stop.
func (q *Qbit) PauseAll(ctx context.Context) error {
	err := q.form(ctx, "/api/v2/torrents/stop", url.Values{"hashes": {"all"}})
	if err != nil && strings.Contains(err.Error(), "404") {
		return q.form(ctx, "/api/v2/torrents/pause", url.Values{"hashes": {"all"}})
	}
	return err
}

// ResumeAll continues torrents after disk space is free again.
func (q *Qbit) ResumeAll(ctx context.Context) error {
	err := q.form(ctx, "/api/v2/torrents/start", url.Values{"hashes": {"all"}})
	if err != nil && strings.Contains(err.Error(), "404") {
		return q.form(ctx, "/api/v2/torrents/resume", url.Values{"hashes": {"all"}})
	}
	return err
}

// Delete removes one torrent. deleteFiles is set so a leftover copy does not fill the disk.
func (q *Qbit) Delete(ctx context.Context, hash string) error {
	if hash == "" {
		return nil
	}
	return q.form(ctx, "/api/v2/torrents/delete", url.Values{"hashes": {hash}, "deleteFiles": {"true"}})
}

func (q *Qbit) form(ctx context.Context, path string, form url.Values) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.Base+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", q.Base)
	res, err := q.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s: status %d (%s)", path, res.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
