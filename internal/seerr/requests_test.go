package seerr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenRequestsSkipsWhatIsDoneOrDeclined(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "k" {
			t.Errorf("key %q", r.Header.Get("X-API-Key"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"pageInfo":{"pages":1},"results":[
			{"status":2,"media":{"tmdbId":603,"mediaType":"movie","status":3}},
			{"status":2,"media":{"tmdbId":1396,"mediaType":"tv","status":4}},
			{"status":2,"media":{"tmdbId":279246,"mediaType":"tv","status":7}},
			{"status":2,"media":{"tmdbId":11,"mediaType":"movie","status":5}},
			{"status":2,"media":{"tmdbId":77,"mediaType":"movie","status":6}},
			{"status":3,"media":{"tmdbId":99,"mediaType":"movie","status":2}}
		]}`))
	}))
	defer srv.Close()
	got, err := OpenRequests(context.Background(), srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].TMDB != 603 || got[0].Series || got[1].TMDB != 1396 || !got[1].Series || got[2].TMDB != 279246 || !got[2].Series {
		t.Fatalf("%+v", got)
	}
}
