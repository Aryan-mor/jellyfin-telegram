package seerr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestOriginalLanguageIsAskedOnceAndSendsTheKey(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("X-API-Key") != "k" {
			http.Error(w, "no key", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/movie/27205":
			_, _ = w.Write([]byte(`{"originalLanguage":"EN"}`))
		case "/api/v1/tv/1396":
			_, _ = w.Write([]byte(`{"originalLanguage":"en"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	l := &Languages{Base: srv.URL + "/", Key: "k"}
	for i := 0; i < 3; i++ {
		got, err := l.Original(context.Background(), 27205, false)
		if err != nil || got != "en" {
			t.Fatalf("movie: %q %v", got, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("the answer was not remembered: %d calls", calls.Load())
	}
	if got, err := l.Original(context.Background(), 1396, true); err != nil || got != "en" {
		t.Fatalf("series: %q %v", got, err)
	}
	if _, err := l.Original(context.Background(), 5, false); err == nil {
		t.Fatal("a missing title was not an error")
	}
	// A failure is not remembered.
	if _, err := l.Original(context.Background(), 5, false); err == nil || calls.Load() != 4 {
		t.Fatalf("calls: %d", calls.Load())
	}
}
