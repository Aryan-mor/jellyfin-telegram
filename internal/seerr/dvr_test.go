package seerr

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApplyDVROnce(t *testing.T) {
	var radarr []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(r.URL.Path, "sonarr") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
			return
		}
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(radarr)
		case http.MethodPost:
			var got map[string]any
			_ = json.Unmarshal(body, &got)
			if got["name"] != "jellyfin-telegram" || got["baseUrl"] != "/radarr" || got["minimumAvailability"] != "released" {
				t.Fatalf("%s", body)
			}
			got["id"] = 0
			radarr = append(radarr, got)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(got)
		case http.MethodPut:
			if !strings.HasSuffix(r.URL.Path, "/0") {
				t.Fatal(r.URL.Path)
			}
			w.WriteHeader(http.StatusOK)
		case http.MethodDelete:
			radarr = nil
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()
	one := []DVR{{Kind: "radarr", Hostname: "host.docker.internal", Port: 17878, APIKey: "k", BaseURL: "/radarr", ProfileID: 1, ProfileName: "HD-1080p", Directory: "/data/media/movies"}}
	if err := ApplyDVR(context.Background(), srv.URL, "key", one); err != nil {
		t.Fatal(err)
	}
	if err := ApplyDVR(context.Background(), srv.URL, "key", one); err != nil {
		t.Fatal(err)
	}
	if len(radarr) != 1 {
		t.Fatalf("rows %d", len(radarr))
	}
	if err := RemoveDVR(context.Background(), srv.URL, "key"); err != nil {
		t.Fatal(err)
	}
}
