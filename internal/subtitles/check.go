package subtitles

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CheckSubDL asks SubDL whether each comma-separated key is accepted.
// The account call does not spend a subtitle search.
func CheckSubDL(ctx context.Context, raw string) error {
	return checkSubDL(ctx, raw, "https://api.subdl.com/api/v1/me", &http.Client{Timeout: 15 * time.Second})
}

func checkSubDL(ctx context.Context, raw, me string, client *http.Client) error {
	keys := splitKeys(raw)
	if len(keys) == 0 {
		return fmt.Errorf("subdl: empty key")
	}
	for i, key := range keys {
		q := url.Values{"api_key": {key}}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, me+"?"+q.Encode(), nil)
		if err != nil {
			return err
		}
		res, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("subdl: could not reach the site")
		}
		var body struct {
			Status bool   `json:"status"`
			Error  string `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&body)
		res.Body.Close()
		if res.StatusCode == http.StatusOK && body.Error == "" && !rejectedKey(body.Error) {
			continue
		}
		if len(keys) == 1 {
			return fmt.Errorf("subdl: the key was rejected")
		}
		return fmt.Errorf("subdl: key %d was rejected", i+1)
	}
	return nil
}

// CheckGemini sends one short prompt. A rejected key stops before a subtitle is translated.
func CheckGemini(ctx context.Context, key string) error {
	return checkGemini(ctx, key, "")
}

func checkGemini(ctx context.Context, key, base string) error {
	g := NewGemini(key)
	if g == nil {
		return fmt.Errorf("gemini: empty key")
	}
	g.Base = base
	g.http.Timeout = 20 * time.Second
	_, err := g.generate(ctx, "Reply with the single word OK.")
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "api key rejected") || strings.Contains(err.Error(), "HTTP 401") || strings.Contains(err.Error(), "HTTP 403") {
		return fmt.Errorf("gemini: the key was rejected")
	}
	return fmt.Errorf("gemini: %s", err.Error())
}

func splitKeys(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func rejectedKey(msg string) bool {
	msg = strings.ToLower(msg)
	return strings.Contains(msg, "invalid") || strings.Contains(msg, "api key") || strings.Contains(msg, "unauthor")
}
