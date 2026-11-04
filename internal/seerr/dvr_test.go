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
			// The real server refuses an id in the body as read-only, so this one does too.
			var got map[string]any
			_ = json.Unmarshal(body, &got)
			if _, has := got["id"]; has {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"request/body/id is read-only"}`))
				return
			}
			got["id"] = 0
			radarr[0] = got
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
	// Moving Radarr to another address must change the saved server, not add a second one.
	one[0].Hostname = "jellyfin-telegram-radarr"
	one[0].Port = 7878
	if err := ApplyDVR(context.Background(), srv.URL, "key", one); err != nil {
		t.Fatal(err)
	}
	if len(radarr) != 1 || radarr[0]["hostname"] != "jellyfin-telegram-radarr" || radarr[0]["port"] != float64(7878) {
		t.Fatalf("the server was not moved: %v", radarr)
	}
	if err := RemoveDVR(context.Background(), srv.URL, "key"); err != nil {
		t.Fatal(err)
	}
}
