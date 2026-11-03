package seerr

import (
	"bytes"
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

// Setup is the first-run configuration. Jellyfin is the media server.
type Setup struct {
	URL               string
	APIKey            string
	JellyfinHost      string
	JellyfinPort      int
	JellyfinUser      string
	JellyfinPassword  string
	JellyfinPublicURL string
	ApplicationURL    string
	Language          string
}

type publicSettings struct {
	Initialized     bool `json:"initialized"`
	MediaServerType int  `json:"mediaServerType"`
}

type library struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// Run signs Seerr in to Jellyfin, marks setup finished, and turns on every library.
// A later start only refreshes the library list.
func (s Setup) Run(ctx context.Context) error {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	c := &client{base: strings.TrimRight(s.URL, "/"), key: s.APIKey, http: &http.Client{Timeout: 2 * time.Minute, Jar: jar}}

	pub, err := c.public(ctx)
	if err != nil {
		return err
	}
	if pub.MediaServerType != mediaServerJellyfin {
		if err := s.login(ctx, c); err != nil {
			return err
		}
		if pub, err = c.public(ctx); err != nil {
			return err
		}
		if pub.MediaServerType != mediaServerJellyfin {
			return fmt.Errorf("Seerr did not accept Jellyfin as its server")
		}
	}
	if !pub.Initialized {
		if err := s.finish(ctx, c); err != nil {
			return err
		}
	}
	return s.libraries(ctx, c)
}

func (s Setup) login(ctx context.Context, c *client) error {
	status, raw, err := c.call(ctx, http.MethodPost, "/api/v1/auth/jellyfin", map[string]any{
		"username":   s.JellyfinUser,
		"password":   s.JellyfinPassword,
		"hostname":   s.JellyfinHost,
		"port":       s.JellyfinPort,
		"urlBase":    "",
		"useSsl":     false,
		"email":      "admin@example.com",
		"serverType": mediaServerJellyfin,
	}, nil)
	if err != nil {
		return err
	}
	if status < 300 {
		return nil
	}
	// A previous attempt stored the server and then stopped before setup was marked done.
	if strings.Contains(strings.ToLower(raw), "already configured") {
		return nil
	}
	return fmt.Errorf("sign Seerr in to Jellyfin: %s", safe(raw, s.JellyfinPassword))
}

func (s Setup) finish(ctx context.Context, c *client) error {
	body := map[string]any{}
	if s.JellyfinPublicURL != "" {
		body["externalHostname"] = strings.TrimRight(s.JellyfinPublicURL, "/")
	}
	if len(body) > 0 {
		if err := c.callOK(ctx, http.MethodPost, "/api/v1/settings/jellyfin", body, nil); err != nil {
			return fmt.Errorf("set Jellyfin address in Seerr: %w", err)
		}
	}
	main := map[string]any{}
	if s.ApplicationURL != "" {
		main["applicationUrl"] = strings.TrimRight(s.ApplicationURL, "/")
	}
	if s.Language != "" {
		main["locale"] = s.Language
	}
	if len(main) > 0 {
		if err := c.callOK(ctx, http.MethodPost, "/api/v1/settings/main", main, nil); err != nil {
			return fmt.Errorf("set Seerr address: %w", err)
		}
	}
	return c.callOK(ctx, http.MethodPost, "/api/v1/settings/initialize", nil, nil)
}

func (s Setup) libraries(ctx context.Context, c *client) error {
	var libs []library
	if err := c.callOK(ctx, http.MethodPost, "/api/v1/settings/jellyfin/library/sync", nil, &libs); err != nil {
		return fmt.Errorf("read Jellyfin libraries: %w", err)
	}
	for _, lib := range libs {
		if lib.Enabled {
			continue
		}
		path := "/api/v1/settings/jellyfin/library/" + url.PathEscape(lib.ID)
		if err := c.callOK(ctx, http.MethodPut, path, map[string]any{"enabled": true}, nil); err != nil {
			return fmt.Errorf("enable library %s: %w", lib.Name, err)
		}
	}
	return nil
}

type client struct {
	base string
	key  string
	http *http.Client
}

func (c *client) public(ctx context.Context) (publicSettings, error) {
	var last error
	for i := 0; i < 10; i++ {
		var pub publicSettings
		if err := c.callOK(ctx, http.MethodGet, "/api/v1/settings/public", nil, &pub); err == nil {
			return pub, nil
		} else {
			last = err
		}
		select {
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			return publicSettings{}, ctx.Err()
		}
	}
	return publicSettings{}, fmt.Errorf("read Seerr: %w", last)
}

func (c *client) callOK(ctx context.Context, method, path string, body, out any) error {
	status, raw, err := c.call(ctx, method, path, body, out)
	if err != nil {
		return err
	}
	if status >= 300 {
		return fmt.Errorf("%s %s: status %d (%s)", method, path, status, raw)
	}
	return nil
}

func (c *client) call(ctx context.Context, method, path string, body, out any) (int, string, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, "", err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return 0, "", err
	}
	if c.key != "" {
		req.Header.Set("X-API-Key", c.key)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer res.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return res.StatusCode, "", err
	}
	if out != nil && res.StatusCode < 300 && len(payload) > 0 {
		if err := json.Unmarshal(payload, out); err != nil {
			return res.StatusCode, string(payload), err
		}
	}
	return res.StatusCode, strings.TrimSpace(string(payload)), nil
}

func safe(msg, secret string) string {
	if secret != "" {
		msg = strings.ReplaceAll(msg, secret, "…")
	}
	msg = strings.ReplaceAll(msg, "\n", " ")
	if len(msg) > 240 {
		msg = msg[:240]
	}
	return msg
}
