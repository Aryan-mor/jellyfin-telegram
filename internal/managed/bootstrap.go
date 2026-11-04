package managed

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/lang"
)

const adminUser = "admin"

// Credentials is what setup creates. The file holding it is private to this user.
type Credentials struct {
	User     string `json:"user"`
	Password string `json:"password"`
	APIKey   string `json:"api_key"`
}

// Bootstrap is the setup of a brand-new server.
type Bootstrap struct {
	URL       string
	StateFile string
	// MoviesDir and ShowsDir are the folders the two libraries read.
	MoviesDir string
	ShowsDir  string
	// HomeDir is the personal Home Videos and Photos folder. Empty skips that library.
	HomeDir string
	// Language is the user's language, or empty.
	Language string
}

// Run finishes the setup wizard and returns the credentials. The password is saved before it is
// used, so an interrupted run can continue instead of locking itself out.
func (b *Bootstrap) Run(ctx context.Context) (Credentials, error) {
	creds, err := b.load()
	if err != nil {
		return Credentials{}, err
	}
	var public struct {
		Done bool `json:"StartupWizardCompleted"`
	}
	c := &apiClient{base: b.URL, header: `MediaBrowser Client="jellyfin-telegram", Device="service", DeviceId="jellyfin-telegram", Version="1"`}
	err = retry(ctx, func() error { return c.call(ctx, http.MethodGet, "/System/Info/Public", nil, &public) })
	if err != nil {
		return Credentials{}, err
	}
	if !public.Done {
		if err := b.wizard(ctx, c, creds); err != nil {
			return Credentials{}, fmt.Errorf("Jellyfin first-run setup: %w", err)
		}
	}
	admin, err := login(ctx, c, creds)
	if err != nil {
		return Credentials{}, err
	}
	if creds.APIKey == "" {
		if creds.APIKey, err = createAPIKey(ctx, admin); err != nil {
			return Credentials{}, fmt.Errorf("create API key: %w", err)
		}
		if err := b.save(creds); err != nil {
			return Credentials{}, err
		}
	}
	if err := b.libraries(ctx, admin); err != nil {
		return Credentials{}, fmt.Errorf("create libraries: %w", err)
	}
	if err := b.preferences(ctx, admin); err != nil {
		return Credentials{}, fmt.Errorf("set language: %w", err)
	}
	return creds, nil
}

func (b *Bootstrap) load() (Credentials, error) {
	raw, err := os.ReadFile(b.StateFile)
	if err == nil {
		var creds Credentials
		if err := json.Unmarshal(raw, &creds); err != nil {
			return Credentials{}, fmt.Errorf("read %s: %w", b.StateFile, err)
		}
		return creds, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Credentials{}, err
	}
	secret := make([]byte, 12)
	if _, err := rand.Read(secret); err != nil {
		return Credentials{}, err
	}
	creds := Credentials{User: adminUser, Password: hex.EncodeToString(secret)}
	return creds, b.save(creds)
}

func (b *Bootstrap) save(creds Credentials) error {
	raw, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(b.StateFile), 0o700); err != nil {
		return err
	}
	return os.WriteFile(b.StateFile, raw, 0o600)
}

func (b *Bootstrap) wizard(ctx context.Context, c *apiClient, creds Credentials) error {
	language := "en"
	if b.Language != "" {
		language = b.Language
	}
	// The endpoints answer 503 for a moment after the server first comes up.
	err := retry(ctx, func() error {
		return c.call(ctx, http.MethodPost, "/Startup/Configuration", map[string]any{
			"UICulture": "en-US", "MetadataCountryCode": "US", "PreferredMetadataLanguage": language,
		}, nil)
	})
	if err != nil {
		return err
	}
	if err := c.call(ctx, http.MethodGet, "/Startup/User", nil, nil); err != nil {
		return err
	}
	if err := c.call(ctx, http.MethodPost, "/Startup/User", map[string]any{"Name": creds.User, "Password": creds.Password}, nil); err != nil {
		return err
	}
	// Automatic port mapping stays off: opening a router to the internet is the user's decision.
	if err := c.call(ctx, http.MethodPost, "/Startup/RemoteAccess", map[string]any{
		"EnableRemoteAccess": true, "EnableAutomaticPortMapping": false,
	}, nil); err != nil {
		return err
	}
	return c.call(ctx, http.MethodPost, "/Startup/Complete", nil, nil)
}

func login(ctx context.Context, c *apiClient, creds Credentials) (*apiClient, error) {
	var auth struct {
		AccessToken string `json:"AccessToken"`
	}
	err := c.call(ctx, http.MethodPost, "/Users/AuthenticateByName", map[string]any{"Username": creds.User, "Pw": creds.Password}, &auth)
	if err != nil {
		return nil, fmt.Errorf("log in to Jellyfin: %w", err)
	}
	return &apiClient{base: c.base, header: fmt.Sprintf(`%s, Token=%q`, c.header, auth.AccessToken)}, nil
}

func createAPIKey(ctx context.Context, admin *apiClient) (string, error) {
	const app = "jellyfin-telegram"
	if err := admin.call(ctx, http.MethodPost, "/Auth/Keys?app="+app, nil, nil); err != nil {
		return "", err
	}
	var keys struct {
		Items []struct {
			AccessToken string `json:"AccessToken"`
			AppName     string `json:"AppName"`
		} `json:"Items"`
	}
	if err := admin.call(ctx, http.MethodGet, "/Auth/Keys", nil, &keys); err != nil {
		return "", err
	}
	for _, k := range keys.Items {
		if k.AppName == app {
			return k.AccessToken, nil
		}
	}
	return "", errors.New("the new key is not listed")
}

func (b *Bootstrap) libraries(ctx context.Context, admin *apiClient) error {
	var existing []struct {
		Name string `json:"Name"`
	}
	if err := admin.call(ctx, http.MethodGet, "/Library/VirtualFolders", nil, &existing); err != nil {
		return err
	}
	have := map[string]bool{}
	for _, f := range existing {
		have[f.Name] = true
	}
	for _, lib := range []struct{ name, kind, dir string }{
		{"Movies", "movies", b.MoviesDir},
		{"Shows", "tvshows", b.ShowsDir},
		{"Home Videos and Photos", "homevideos", b.HomeDir},
	} {
		if lib.dir == "" {
			continue
		}
		if have[lib.name] {
			continue
		}
		q := url.Values{"name": {lib.name}, "collectionType": {lib.kind}, "refreshLibrary": {"false"}}
		// SaveSubtitlesWithMedia stays off so an uploaded subtitle goes to the item's own folder.
		// A file next to the .strm stays invisible once Jellyfin has listed that folder.
		err := admin.call(ctx, http.MethodPost, "/Library/VirtualFolders?"+q.Encode(), map[string]any{
			"LibraryOptions": map[string]any{
				"EnableRealtimeMonitor":  false,
				"SaveSubtitlesWithMedia": false,
				"PathInfos":              []map[string]string{{"Path": lib.dir}},
			},
		}, nil)
		if err != nil {
			return fmt.Errorf("%s: %w", lib.name, err)
		}
	}
	return nil
}

// preferences makes the user's language the default for subtitles and audio. The Smart mode shows
// subtitles only when the audio is in another language.
func (b *Bootstrap) preferences(ctx context.Context, admin *apiClient) error {
	if b.Language == "" {
		return nil
	}
	var me struct {
		ID            string         `json:"Id"`
		Configuration map[string]any `json:"Configuration"`
	}
	if err := admin.call(ctx, http.MethodGet, "/Users/Me", nil, &me); err != nil {
		return err
	}
	iso := lang.ISO3(b.Language)
	me.Configuration["SubtitleLanguagePreference"] = iso
	me.Configuration["AudioLanguagePreference"] = iso
	me.Configuration["SubtitleMode"] = "Smart"
	return admin.call(ctx, http.MethodPost, "/Users/"+url.PathEscape(me.ID)+"/Configuration", me.Configuration, nil)
}

func retry(ctx context.Context, fn func() error) error {
	deadline := time.Now().Add(time.Minute)
	for {
		err := fn()
		if err == nil || time.Now().After(deadline) {
			return err
		}
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type apiClient struct {
	base   string
	header string
}

func (c *apiClient) call(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.header)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, res.Body)
		// Query strings are dropped, because they can hold a name or a secret.
		return fmt.Errorf("%s %s: status %d", method, strings.SplitN(path, "?", 2)[0], res.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}
