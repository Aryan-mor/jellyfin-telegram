package subtitles

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	subplusAPI  = "https://sub-plus.ir/api.php"
	maxAPIBytes = 1 << 20
	maxZipBytes = 32 << 20
)

// subplusLanguages maps language codes to the values sub-plus.ir documents. An unknown value is
// not an error upstream: it silently answers in Persian, so anything outside this list must never
// be sent.
var subplusLanguages = map[string]string{
	"en": "english", "fa": "farsi_persian", "ar": "arabic", "es": "spanish", "pt": "portuguese",
	"it": "italian", "nl": "dutch", "he": "hebrew", "id": "indonesian", "da": "danish",
	"no": "norwegian", "bn": "bengali", "bg": "bulgarian", "hr": "croatian", "sv": "swedish",
	"vi": "vietnamese", "cs": "czech", "fi": "finnish", "fr": "french", "de": "german",
	"el": "greek", "hu": "hungarian", "is": "icelandic", "ja": "japanese", "mk": "macedonian",
	"ms": "malay", "pl": "polish", "ro": "romanian", "ru": "russian", "sr": "serbian",
	"th": "thai", "tr": "turkish",
}

// SubPlus is a client for sub-plus.ir, a public keyless subtitle API.
type SubPlus struct {
	http     *http.Client
	api      string
	code     string
	language string
}

// NewSubPlus returns a client for a normalized language code, or false if the site does not
// offer that language.
func NewSubPlus(code string) (*SubPlus, bool) {
	value, ok := subplusLanguages[code]
	if !ok {
		return nil, false
	}
	return &SubPlus{http: &http.Client{Timeout: 30 * time.Second}, api: subplusAPI, code: code, language: value}, true
}

// Pack is one search hit: a bundle of subtitle files for a title.
type Pack struct {
	Tag      string
	Title    string
	IMDb     string
	Year     string
	Series   bool
	Releases []string
}

// Search takes free text, or an IMDb id as ttNNNNNNN for an exact match.
func (c *SubPlus) Search(ctx context.Context, query string) ([]Pack, error) {
	raw, err := c.post(ctx, url.Values{"q": {strings.TrimSpace(query)}, "l": {c.language}})
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var body struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      []struct {
			Title  string `json:"title"`
			Tag    string `json:"tag"`
			Series bool   `json:"series"`
			Info   string `json:"info"`
			IMDb   string `json:"imdb"`
			Year   string `json:"year"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("sub-plus search: bad response: %w", err)
	}
	if !body.OK {
		return nil, fmt.Errorf("sub-plus search: %s", strings.TrimSpace(body.Description))
	}
	packs := make([]Pack, 0, len(body.Result))
	for _, r := range body.Result {
		if strings.TrimSpace(r.Tag) == "" {
			continue
		}
		packs = append(packs, Pack{
			Tag:      r.Tag,
			Title:    strings.TrimSpace(r.Title),
			IMDb:     strings.TrimSpace(r.IMDb),
			Year:     strings.TrimSpace(r.Year),
			Series:   r.Series,
			Releases: splitLines(r.Info),
		})
	}
	return packs, nil
}

// Download returns the subtitle files of a pack.
func (c *SubPlus) Download(ctx context.Context, tag string) ([]SubFile, error) {
	raw, err := c.post(ctx, url.Values{"dl": {strings.TrimSpace(tag)}})
	if err != nil {
		return nil, err
	}
	var body struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Download    string `json:"download"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("sub-plus download: bad response: %w", err)
	}
	if !body.OK {
		return nil, fmt.Errorf("sub-plus download: %s", strings.TrimSpace(body.Description))
	}
	link := strings.TrimSpace(body.Download)
	if !validDownloadURL(link) {
		return nil, fmt.Errorf("sub-plus download: unusable link")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sub-plus zip: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sub-plus zip: HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxZipBytes))
	if err != nil {
		return nil, fmt.Errorf("sub-plus zip: %w", err)
	}
	return ExtractSubs(data, c.code)
}

func (c *SubPlus) post(ctx context.Context, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.api, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sub-plus: %w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxAPIBytes))
	if err != nil {
		return nil, fmt.Errorf("sub-plus: %w", err)
	}
	// Every POST outcome is a 200, so the JSON ok field is the real signal.
	if res.StatusCode >= http.StatusInternalServerError {
		return nil, fmt.Errorf("sub-plus: HTTP %d", res.StatusCode)
	}
	return data, nil
}

// validDownloadURL guards a known upstream bug: a bad tag returns ok with a link holding the
// server's filesystem path.
func validDownloadURL(link string) bool {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	if strings.Contains(u.Path, "//") || !strings.HasPrefix(u.Path, "/download/") {
		return false
	}
	return strings.HasSuffix(strings.ToLower(u.Path), ".zip")
}

func splitLines(s string) []string {
	var out []string
	for _, line := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }) {
		if l := strings.TrimSpace(line); l != "" {
			out = append(out, l)
		}
	}
	return out
}
