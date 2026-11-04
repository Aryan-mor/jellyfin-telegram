package stack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfigureIsIdempotent(t *testing.T) {
	var posts []string
	profiles := []map[string]any{{
		"id": 1, "name": "HD-1080p", "upgradeAllowed": true,
		"items": []any{
			map[string]any{"name": "Bluray-1080p", "allowed": true, "quality": map[string]any{"name": "Bluray-1080p"}},
			map[string]any{"name": "Remux-2160p", "allowed": true, "quality": map[string]any{"name": "Remux-2160p"}},
		},
	}}
	defs := []map[string]any{{"quality": map[string]any{"name": "Bluray-1080p"}, "maxSize": 0}}
	var indexers []map[string]any
	var clients []any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method == http.MethodPost {
			posts = append(posts, r.URL.Path+"?"+r.URL.RawQuery+" "+string(body))
		}
		switch {
		case r.URL.Path == "/api/v3/config/host":
			writeJSON(w, map[string]any{"urlBase": "", "username": ""})
		case r.URL.Path == "/api/v3/rootfolder" && r.Method == http.MethodGet:
			writeJSON(w, []any{})
		case r.URL.Path == "/api/v3/rootfolder":
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/api/v3/qualityprofile" && r.Method == http.MethodGet:
			writeJSON(w, profiles)
		case strings.HasPrefix(r.URL.Path, "/api/v3/qualityprofile/"):
			var got map[string]any
			_ = json.Unmarshal(body, &got)
			if got["upgradeAllowed"] != false {
				t.Errorf("upgrade stays on: %s", body)
			}
			if bytesContain(body, "Remux-2160p") && !bytesContain(body, `"allowed":false`) {
				t.Errorf("2160 remux still allowed: %s", body)
			}
			w.WriteHeader(http.StatusAccepted)
		case r.URL.Path == "/api/v3/qualitydefinition" && r.Method == http.MethodGet:
			writeJSON(w, defs)
		case r.URL.Path == "/api/v3/qualitydefinition/update":
			var got []map[string]any
			_ = json.Unmarshal(body, &got)
			if len(got) != 1 || got[0]["maxSize"] == float64(0) {
				t.Errorf("size not capped: %s", body)
			}
			w.WriteHeader(http.StatusAccepted)
		case r.URL.Path == "/api/v3/downloadclient" && r.Method == http.MethodGet:
			writeJSON(w, clients)
		case r.URL.Path == "/api/v3/downloadclient":
			if !bytesContain(body, `"removeCompletedDownloads":true`) {
				t.Errorf("completed downloads are not removed: %s", body)
			}
			clients = append(clients, map[string]any{"name": "qBittorrent"})
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/api/v3/indexer" && r.Method == http.MethodGet:
			writeJSON(w, indexers)
		case r.URL.Path == "/api/v3/indexer" && r.Method == http.MethodPost && r.URL.Query().Get("forceSave") == "":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("indexer test failed"))
		case strings.Contains(r.URL.Path, "/api/v3/indexer"):
			if r.Method == http.MethodPost {
				indexers = []map[string]any{{"id": 4, "name": "Bitmagnet", "enableAutomaticSearch": false}}
				writeJSON(w, indexers[0])
				return
			}
			if !strings.Contains(r.URL.RawQuery, "forceSave=true") {
				t.Error("enable without forceSave")
			}
			indexers[0]["enableAutomaticSearch"] = true
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	app := App{Kind: "radarr", Base: srv.URL, Key: "k"}
	setup := Setup{Root: "/data/media/movies", CapMB: 2000, DownloadHost: "qb", DownloadPort: 8080, Category: "radarr", IndexerURL: "http://bitmagnet:3333/torznab", URLBase: "/radarr", Username: "admin", Password: "pw"}
	if _, err := app.Configure(context.Background(), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Configure(context.Background(), setup); err != nil {
		t.Fatal(err)
	}
	creates := 0
	for _, p := range posts {
		if strings.Contains(p, "/api/v3/downloadclient") {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("download client created %d times", creates)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func bytesContain(b []byte, s string) bool { return strings.Contains(string(b), s) }
