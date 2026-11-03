package autoupload

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestDownloadIDIgnoresOtherMovies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"records":[
			{"downloadId":"OTHER","movieId":99,"date":"2026-11-03T10:00:00Z"},
			{"downloadId":"OLD","movieId":5,"date":"2026-11-01T10:00:00Z"},
			{"downloadId":"NEW","movieId":5,"date":"2026-11-02T10:00:00Z"}]}`)
	}))
	defer srv.Close()
	s := &Source{RadarrBase: srv.URL}
	got, err := s.DownloadID(context.Background(), Item{Kind: "movie", OwnerID: 5})
	if err != nil || got != "NEW" {
		t.Fatalf("got %q %v", got, err)
	}
	none, err := s.DownloadID(context.Background(), Item{Kind: "movie", OwnerID: 6})
	if err != nil || none != "" {
		t.Fatalf("an unrelated record was used: %q %v", none, err)
	}
}

func TestUnmonitorCoversEveryEpisodeOfOneFile(t *testing.T) {
	var mu sync.Mutex
	var sent []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v3/episode"):
			_, _ = io.WriteString(w, `[{"id":10,"episodeFileId":7},{"id":11,"episodeFileId":7},{"id":12,"episodeFileId":8}]`)
		case r.Method == http.MethodPut && r.URL.Path == "/api/v3/episode/monitor":
			var body struct {
				EpisodeIDs []int `json:"episodeIds"`
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			mu.Lock()
			sent = body.EpisodeIDs
			mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
	}))
	defer srv.Close()
	s := &Source{SonarrBase: srv.URL}
	if err := s.Unmonitor(context.Background(), Item{Kind: "episode", RemoteID: 7, OwnerID: 10}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 2 || sent[0] != 10 || sent[1] != 11 {
		t.Fatalf("unmonitored %v, want episodes 10 and 11 only", sent)
	}
}

func TestDeleteFileAlreadyGoneIsDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	s := &Source{RadarrBase: srv.URL}
	if err := s.DeleteFile(context.Background(), Item{Kind: "movie", RemoteID: 3}); err != nil {
		t.Fatalf("a retry after a half-finished cleanup got stuck: %v", err)
	}
}

func TestAnEpisodeNoEpisodeListNamesIsSkipped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/series":
			_, _ = io.WriteString(w, `[{"id":1,"title":"Show","year":2020,"tmdbId":4,"statistics":{"episodeFileCount":1}},
				{"id":2,"title":"Empty","year":2021,"statistics":{"episodeFileCount":0}}]`)
		case r.URL.Path == "/api/v3/episodefile":
			_, _ = io.WriteString(w, `[{"id":5,"seasonNumber":1,"path":"/data/media/tv/a.mkv","size":10}]`)
		case r.URL.Path == "/api/v3/episode":
			_, _ = io.WriteString(w, `[]`)
		default:
			t.Errorf("a series with no files was read: %s", r.URL)
		}
	}))
	defer srv.Close()
	s := &Source{SonarrBase: srv.URL}
	items, err := s.episodes(context.Background())
	if err != nil || len(items) != 0 {
		t.Fatalf("items %v err %v", items, err)
	}
}
