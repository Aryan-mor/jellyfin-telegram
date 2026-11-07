package seerr

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Languages asks Seerr, which keeps TMDB's data, what language a film or series was made in. The
// answer decides which audio track of a file is the original and which is a dub.
type Languages struct {
	// Base is the Seerr address and Key its API key.
	Base string
	Key  string

	mu    sync.Mutex
	cache map[string]string
}

// Original returns the ISO 639-1 original language of a film or series, such as "en" or "ja".
// Only an answer is remembered, so a Seerr that is still starting is asked again later.
func (l *Languages) Original(ctx context.Context, tmdb int, series bool) (string, error) {
	kind := "movie"
	if series {
		kind = "tv"
	}
	id := fmt.Sprintf("%s/%d", kind, tmdb)
	l.mu.Lock()
	if got, ok := l.cache[id]; ok {
		l.mu.Unlock()
		return got, nil
	}
	l.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c := &client{base: strings.TrimRight(l.Base, "/"), key: l.Key, http: &http.Client{}}
	var body struct {
		OriginalLanguage string `json:"originalLanguage"`
	}
	if err := c.callOK(ctx, http.MethodGet, "/api/v1/"+id, nil, &body); err != nil {
		return "", err
	}
	got := strings.ToLower(strings.TrimSpace(body.OriginalLanguage))
	if got == "" {
		return "", nil
	}
	l.mu.Lock()
	if l.cache == nil {
		l.cache = map[string]string{}
	}
	l.cache[id] = got
	l.mu.Unlock()
	return got, nil
}
