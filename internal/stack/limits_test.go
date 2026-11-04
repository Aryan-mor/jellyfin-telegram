package stack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSizeCapIsPerMinuteAndFollowsTheAccounts(t *testing.T) {
	var last []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, []map[string]any{
				{"quality": map[string]any{"name": "WEBDL-1080p"}, "minSize": 0, "preferredSize": 999, "maxSize": 0},
				{"quality": map[string]any{"name": "SDTV"}, "minSize": 0, "preferredSize": 5, "maxSize": 20},
			})
			return
		}
		raw, _ := io.ReadAll(r.Body)
		last = nil
		_ = json.Unmarshal(raw, &last)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	app := App{Kind: "radarr", Base: srv.URL, HTTP: &http.Client{}}

	if err := app.sizes(context.Background(), 2000, 150); err != nil {
		t.Fatal(err)
	}
	want := 2000.0 / 150
	for _, item := range last {
		if got := number(item["maxSize"]); got < want-0.001 || got > want+0.001 {
			t.Fatalf("maxSize %v, want %v megabytes per minute: a 150 minute film then stays at 2000 MB", got, want)
		}
		if number(item["preferredSize"]) > want+0.001 {
			t.Fatalf("preferred size %v is above the maximum", item["preferredSize"])
		}
	}
	// A Premium account raises the largest file, and the limit must follow it up.
	if err := app.sizes(context.Background(), 4000, 150); err != nil {
		t.Fatal(err)
	}
	if got := number(last[0]["maxSize"]); got < 4000.0/150-0.001 {
		t.Fatalf("the limit did not rise with the cap: %v", got)
	}
}

func TestBitmagnetAndPostgresArguments(t *testing.T) {
	bit := strings.Join(bitmagnetArgs("pw", true), " ")
	if strings.Contains(bit, "--keys=postgres") {
		t.Error("postgres is not a Bitmagnet worker key")
	}
	if strings.Contains(bit, "shm-size") {
		t.Error("shared memory belongs to the Postgres container")
	}
	for _, key := range []string{"--keys=http_server", "--keys=queue_server", "--keys=dht_crawler"} {
		if !strings.Contains(bit, key) {
			t.Errorf("missing %s", key)
		}
	}
	if strings.Contains(strings.Join(bitmagnetArgs("pw", false), " "), "dht_crawler") {
		t.Error("the crawler runs although the database is full")
	}
	if !strings.Contains(strings.Join(postgresArgs("pw"), " "), "--shm-size=1g") {
		t.Error("Postgres has too little shared memory")
	}
}

func TestEveryImageTagIsAReleaseNotAGuess(t *testing.T) {
	for _, image := range Images() {
		if strings.HasSuffix(image, ":latest") || !strings.Contains(image, ":") {
			t.Errorf("%s is not a pinned version", image)
		}
	}
	if !strings.HasSuffix(ImageBitmagnet, ":v0.10.1") {
		t.Errorf("Bitmagnet %s is not a tag that exists on ghcr.io", ImageBitmagnet)
	}
}

func TestContainerIDsAreNumbers(t *testing.T) {
	uid, gid := containerIDs()
	if uid == "" || gid == "" || uid == "0" || gid == "0" {
		t.Fatalf("uid %q gid %q", uid, gid)
	}
}

func TestCutoffMovesOffAQualityThatWasSwitchedOff(t *testing.T) {
	profile := map[string]any{
		"cutoff": float64(31),
		"items": []any{
			map[string]any{"allowed": true, "quality": map[string]any{"id": float64(7), "name": "Bluray-1080p"}},
			map[string]any{"allowed": true, "id": float64(1002), "name": "WEB 1080p", "items": []any{}},
			map[string]any{"allowed": true, "quality": map[string]any{"id": float64(31), "name": "Bluray-2160p"}},
			map[string]any{"allowed": true, "quality": map[string]any{"id": float64(30), "name": "Remux-2160p"}},
		},
	}
	disallowHeavy(profile["items"])
	fixCutoff(profile)
	if got := number(profile["cutoff"]); got != 1002 {
		t.Fatalf("cutoff %v, want the last allowed item 1002", got)
	}
	// A cutoff that is still allowed stays where it is.
	profile["cutoff"] = float64(7)
	fixCutoff(profile)
	if number(profile["cutoff"]) != 7 {
		t.Fatal("an allowed cutoff was moved")
	}
}

func TestOnlyTheProfileSeerrUsesIsChanged(t *testing.T) {
	var puts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, []map[string]any{
				{"id": 5, "name": "Ultra-HD", "cutoff": 31, "upgradeAllowed": true, "items": []any{
					map[string]any{"allowed": true, "quality": map[string]any{"id": 31, "name": "Remux-2160p"}},
				}},
				{"id": 6, "name": "HD-1080p", "cutoff": 7, "upgradeAllowed": true, "items": []any{
					map[string]any{"allowed": true, "quality": map[string]any{"id": 7, "name": "Bluray-1080p"}},
					map[string]any{"allowed": true, "quality": map[string]any{"id": 31, "name": "Remux-2160p"}},
				}},
			})
			return
		}
		puts = append(puts, r.URL.Path)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	app := App{Kind: "radarr", Base: srv.URL, HTTP: &http.Client{}}
	got, err := app.profiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 6 || len(puts) != 1 || !strings.HasSuffix(puts[0], "/6") {
		t.Fatalf("picked %+v, updated %v: the 2160p-only profile must be left alone", got, puts)
	}
}

func TestPrepareDirMakesTheFoldersTheAppsLookFor(t *testing.T) {
	dir := t.TempDir()
	if err := PrepareDir(dir); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"torrents/movies", "torrents/tv", "media/movies", "media/tv"} {
		if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(sub))); err != nil || !info.IsDir() {
			t.Errorf("%s was not created: %v", sub, err)
		}
	}
}

func TestSeerrReachesTheAppsByTheFastPath(t *testing.T) {
	host, port := seerrTarget("windows", "radarr")
	if host != RadarrName || port != RadarrInnerPort {
		t.Errorf("windows radarr: %s:%d, want the container name and its own port", host, port)
	}
	host, port = seerrTarget("darwin", "sonarr")
	if host != SonarrName || port != SonarrInnerPort {
		t.Errorf("macos sonarr: %s:%d", host, port)
	}
	host, port = seerrTarget("linux", "radarr")
	if host != "127.0.0.1" || port != RadarrPort {
		t.Errorf("linux radarr: %s:%d, want the published loopback port", host, port)
	}
}

func TestSearchMissingAsksEachAppForItsOwnCommand(t *testing.T) {
	for kind, want := range map[string]string{"radarr": "MissingMoviesSearch", "sonarr": "MissingEpisodeSearch"} {
		var got map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/api/v3/command" {
				t.Errorf("%s %s", r.Method, r.URL.Path)
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &got)
			w.WriteHeader(http.StatusCreated)
		}))
		err := searchMissing(context.Background(), App{Kind: kind, Base: srv.URL, HTTP: &http.Client{}})
		srv.Close()
		if err != nil || got["name"] != want || got["monitored"] != true {
			t.Errorf("%s: %v %v", kind, err, got)
		}
	}
	// An answer that is an error must surface, naming the app.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadRequest) }))
	defer bad.Close()
	if err := searchMissing(context.Background(), App{Kind: "radarr", Base: bad.URL, HTTP: &http.Client{}}); err == nil || !strings.Contains(err.Error(), "radarr") {
		t.Errorf("error %v", err)
	}
}
