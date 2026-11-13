package subtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Models a new AI Studio key can still call. 2.x is closed to new keys.
var geminiModels = []string{
	"gemini-3.1-flash-lite",
	"gemini-3.5-flash-lite",
	"gemini-flash-lite-latest",
	"gemini-3.5-flash",
}

// Gemini translates subtitle cues with the user's own key.
type Gemini struct {
	key  string
	http *http.Client
	// Base is the API origin plus version path, without a trailing slash.
	// Empty uses the public Generative Language API.
	Base string
}

// NewGemini returns a translator. An empty key returns nil.
func NewGemini(key string) *Gemini {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	return &Gemini{key: key, http: &http.Client{Timeout: 60 * time.Second}}
}

type srtCue struct {
	timing string
	text   string
}

// Translate turns an SRT into targetLang. Timestamps stay; only the dialogue is sent.
func (g *Gemini) Translate(ctx context.Context, srt, targetLang string) (string, error) {
	if g == nil || g.key == "" {
		return "", fmt.Errorf("gemini: no api key")
	}
	cues := parseSRT(srt)
	if len(cues) == 0 {
		return "", fmt.Errorf("gemini: no cues")
	}
	batches := batchCues(cues, 24000)
	var out []srtCue
	for _, batch := range batches {
		done, err := g.translateBatch(ctx, batch, targetLang, false)
		if err != nil {
			return "", err
		}
		out = append(out, done...)
	}
	return buildSRT(out), nil
}

func (g *Gemini) translateBatch(ctx context.Context, batch []srtCue, lang string, retried bool) ([]srtCue, error) {
	reply, err := g.generate(ctx, geminiPrompt(numberedCues(batch), lang))
	if err != nil {
		if !retried && strings.Contains(err.Error(), "HTTP 429") {
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return g.translateBatch(ctx, batch, lang, true)
		}
		return nil, err
	}
	translated := applyNumbered(batch, reply)
	if unchangedCues(batch, translated)*2 <= len(batch) {
		return translated, nil
	}
	if !retried {
		return g.translateBatch(ctx, batch, lang, true)
	}
	return nil, fmt.Errorf("gemini: incomplete translation")
}

func (g *Gemini) generate(ctx context.Context, prompt string) (string, error) {
	base := g.Base
	if base == "" {
		base = "https://generativelanguage.googleapis.com/v1beta"
	}
	var last error
	tried := map[string]bool{}
	for _, model := range geminiModels {
		if tried[model] {
			continue
		}
		tried[model] = true
		text, err := g.post(ctx, base, model, prompt)
		if err == nil {
			return text, nil
		}
		last = err
		if strings.Contains(err.Error(), "HTTP 401") || strings.Contains(err.Error(), "HTTP 403") {
			return "", err
		}
	}
	if last == nil {
		last = fmt.Errorf("gemini: no model")
	}
	return "", last
}

func (g *Gemini) post(ctx context.Context, base, model, prompt string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]string{{"text": prompt}}},
		},
		"generationConfig": map[string]any{
			"temperature": 0.2,
			"thinkingConfig": map[string]string{
				"thinkingLevel": "minimal",
			},
		},
	})
	if err != nil {
		return "", err
	}
	endpoint := base + "/models/" + model + ":generateContent?key=" + g.key
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := g.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("gemini: %w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("gemini: HTTP %d model=%s", res.StatusCode, model)
	}
	text := geminiText(raw)
	if text == "" {
		return "", fmt.Errorf("gemini: empty reply model=%s", model)
	}
	return text, nil
}

func geminiText(raw []byte) string {
	var body struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return ""
	}
	var b strings.Builder
	for _, c := range body.Candidates {
		for _, p := range c.Content.Parts {
			b.WriteString(p.Text)
		}
	}
	return strings.TrimSpace(b.String())
}

func parseSRT(raw string) []srtCue {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []srtCue
	for _, block := range regexp.MustCompile(`\n\s*\n`).Split(raw, -1) {
		lines := strings.Split(block, "\n")
		i := 0
		if i < len(lines) && regexp.MustCompile(`^\d+\s*$`).MatchString(strings.TrimSpace(lines[0])) {
			i = 1
		}
		if i >= len(lines) || !strings.Contains(lines[i], "-->") {
			continue
		}
		text := strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
		if text == "" {
			continue
		}
		out = append(out, srtCue{timing: strings.TrimSpace(lines[i]), text: text})
	}
	return out
}

func buildSRT(cues []srtCue) string {
	var b strings.Builder
	for i, c := range cues {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%d\n%s\n%s\n", i+1, c.timing, c.text)
	}
	return b.String()
}

func batchCues(cues []srtCue, maxChars int) [][]srtCue {
	var batches [][]srtCue
	var cur []srtCue
	size := 0
	for _, c := range cues {
		add := len(c.text) + 8
		if len(cur) > 0 && size+add > maxChars {
			batches = append(batches, cur)
			cur = nil
			size = 0
		}
		cur = append(cur, c)
		size += add
	}
	if len(cur) > 0 {
		batches = append(batches, cur)
	}
	return batches
}

func numberedCues(cues []srtCue) string {
	var b strings.Builder
	for i, c := range cues {
		fmt.Fprintf(&b, "%d. %s\n", i+1, strings.ReplaceAll(c.text, "\n", " | "))
	}
	return b.String()
}

var reNumbered = regexp.MustCompile(`^(\d+)\.\s*(.*)$`)

func applyNumbered(cues []srtCue, reply string) []srtCue {
	got := map[int]string{}
	for _, raw := range strings.Split(strings.ReplaceAll(reply, "\r\n", "\n"), "\n") {
		m := reNumbered.FindStringSubmatch(strings.TrimSpace(raw))
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || strings.TrimSpace(m[2]) == "" {
			continue
		}
		got[n] = strings.ReplaceAll(strings.TrimSpace(m[2]), " | ", "\n")
	}
	out := make([]srtCue, len(cues))
	for i, c := range cues {
		out[i] = c
		if t, ok := got[i+1]; ok {
			out[i].text = t
		}
	}
	return out
}

func unchangedCues(a, b []srtCue) int {
	n := 0
	for i := 0; i < len(a) && i < len(b); i++ {
		if strings.TrimSpace(a[i].text) == strings.TrimSpace(b[i].text) {
			n++
		}
	}
	return n
}
