package seerr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestDetailsReadsTheSeerrMovieShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "k" {
			http.Error(w, "no key", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/movie/969681":
			_, _ = w.Write([]byte(`{
				"title":"مرد عنکبوتی: روز کاملاً جدید",
				"originalTitle":"Spider-Man: Brand New Day",
				"releaseDate":"2026-07-29",
				"runtime":145,
				"imdbId":"tt22084616",
				"externalIds":{"imdbId":"tt22084616"},
				"releases":{"results":[
					{"iso_3166_1":"US","release_dates":[
						{"type":3,"release_date":"2026-07-29T00:00:00.000Z"},
						{"type":4,"release_date":"2099-01-01T00:00:00.000Z"}
					]},
					{"iso_3166_1":"DE","release_dates":[
						{"type":5,"release_date":"1999-11-25T00:00:00.000Z"}
					]}
				]}
			}`))
		case "/api/v1/tv/1396":
			_, _ = w.Write([]byte(`{
				"name":"بریکینگ بد",
				"originalName":"Breaking Bad",
				"firstAirDate":"2008-01-20",
				"episodeRunTime":[47],
				"externalIds":{"imdbId":"tt0903747"}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	d, err := Details(context.Background(), srv.URL, "k", 969681, false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Title == "" || d.OriginalTitle != "Spider-Man: Brand New Day" || d.Year != 2026 || d.Runtime != 145 || d.IMDb != "tt22084616" {
		t.Fatalf("%+v", d)
	}
	if !d.DigitalOut || d.Release.Year() != 1999 {
		t.Fatalf("digital %+v", d)
	}
	tv, err := Details(context.Background(), srv.URL, "k", 1396, true)
	if err != nil || tv.OriginalTitle != "Breaking Bad" || tv.Year != 2008 || tv.Runtime != 47 || tv.DigitalOut || tv.IMDb != "tt0903747" {
		t.Fatalf("%+v %v", tv, err)
	}
}

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
