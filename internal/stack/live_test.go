//go:build live

package stack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Run with: JFTG_LIVE_DIR=D:\jftg-live go test -tags live -run TestLiveStack -timeout 40m ./internal/stack
// It starts the real containers on the fixed loopback ports, so stop the service's own stack first.
func TestLiveStack(t *testing.T) {
	root := os.Getenv("JFTG_LIVE_DIR")
	if root == "" {
		t.Skip("set JFTG_LIVE_DIR to a folder on a roomy disk")
	}
	dl := filepath.Join(root, "downloads")
	r := &Runner{Downloads: dl, ConfigDir: filepath.Join(root, "stack"), PublicBase: "http://localhost:8097", MaxDBGB: 40}
	r.SetCapMB(2000)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	defer cancel()
	t.Cleanup(func() {
		r.Stop(context.Background())
		for _, v := range []string{VolumeRadarr, VolumeSonarr, VolumePostgres} {
			_ = exec.Command("docker", "volume", "rm", "-f", v).Run()
		}
	})

	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	st := r.Snapshot()
	if !st.Ready || st.RadarrAPIKey == "" || st.SonarrAPIKey == "" {
		t.Fatalf("status %+v", st)
	}
	for _, c := range st.Containers {
		if !c.Running {
			t.Errorf("%s is not running", c.Name)
		}
	}

	for name, spec := range map[string]struct {
		port int
		base string
		key  string
		root string
	}{
		"radarr": {RadarrPort, "/radarr", st.RadarrAPIKey, "/data/media/movies"},
		"sonarr": {SonarrPort, "/sonarr", st.SonarrAPIKey, "/data/media/tv"},
	} {
		get := func(path string, out any) {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s%s", spec.port, spec.base, path), nil)
			req.Header.Set("X-Api-Key", spec.key)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", name, path, err)
			}
			defer res.Body.Close()
			raw, _ := io.ReadAll(res.Body)
			if res.StatusCode != 200 {
				t.Fatalf("%s %s: %d %s", name, path, res.StatusCode, raw)
			}
			if err := json.Unmarshal(raw, out); err != nil {
				t.Fatalf("%s %s: %v", name, path, err)
			}
		}
		var folders, clients, indexers, defs []map[string]any
		get("/api/v3/rootfolder", &folders)
		get("/api/v3/downloadclient", &clients)
		get("/api/v3/indexer", &indexers)
		// The size update answers 202 and is applied a moment later, so wait for it.
		for i := 0; i < 20; i++ {
			get("/api/v3/qualitydefinition", &defs)
			done := true
			for _, d := range defs {
				if number(d["maxSize"]) > 2000.0/60+0.01 || d["maxSize"] == nil {
					done = false
				}
			}
			if done {
				break
			}
			time.Sleep(time.Second)
		}
		if len(folders) != 1 || folders[0]["path"] != spec.root {
			t.Errorf("%s root folders %v", name, folders)
		}
		if len(clients) != 1 {
			t.Errorf("%s download clients %d", name, len(clients))
		}
		if len(indexers) != 1 || indexers[0]["enableAutomaticSearch"] != true || indexers[0]["enableRss"] != true || indexers[0]["enableInteractiveSearch"] != true {
			t.Errorf("%s indexers %v", name, indexers)
		}
		for _, d := range defs {
			if m := number(d["maxSize"]); m <= 0 || m > 2000.0/60+0.01 {
				t.Errorf("%s %v max size %v", name, d["title"], m)
			}
		}
		t.Logf("%s: root, 1 download client, Bitmagnet enabled, %d size limits", name, len(defs))
	}

	q := &Qbit{Base: fmt.Sprintf("http://127.0.0.1:%d", QbitPort), User: "admin", Pass: st.QbitPassword}
	if err := q.Login(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := q.HTTP.Get(q.Base + "/api/v2/app/preferences")
	if err != nil {
		t.Fatal(err)
	}
	var prefs map[string]any
	_ = json.NewDecoder(res.Body).Decode(&prefs)
	res.Body.Close()
	t.Logf("qBittorrent max_ratio_enabled=%v max_ratio=%v max_ratio_act=%v", prefs["max_ratio_enabled"], prefs["max_ratio"], prefs["max_ratio_act"])
	if prefs["max_ratio_enabled"] != true || number(prefs["max_ratio"]) != 0 {
		t.Errorf("ratio limit was not accepted")
	}

	caps, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/torznab/api?t=caps", BitmagnetPort))
	if err != nil || caps.StatusCode != 200 {
		t.Errorf("Bitmagnet torznab caps: %v %v", err, caps)
	} else {
		caps.Body.Close()
	}

	// A hardlink across the bind mount decides whether an import uses double space.
	out, err := exec.Command("docker", "exec", RadarrName, "sh", "-c",
		"echo a > /data/torrents/linktest && ln /data/torrents/linktest /data/media/movies/linktest && stat -c %h /data/media/movies/linktest; rm -f /data/torrents/linktest /data/media/movies/linktest").CombinedOutput()
	t.Logf("hardlink on the bind mount: %s (err %v)", strings.TrimSpace(string(out)), err)

	// The cleanup calls must be shaped the way the servers expect. Nothing exists yet, so the right
	// answer is "found nothing" or "not found", never a complaint about the request itself.
	probe := func(name, method string, port int, base, key, path, body string) {
		req, _ := http.NewRequestWithContext(ctx, method, fmt.Sprintf("http://127.0.0.1:%d%s%s", port, base, path), strings.NewReader(body))
		req.Header.Set("X-Api-Key", key)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			return
		}
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		res.Body.Close()
		t.Logf("shape %-34s -> %d %s", name, res.StatusCode, strings.TrimSpace(string(raw))[:min(60, len(strings.TrimSpace(string(raw))))])
		missing := res.StatusCode == 500 && strings.Contains(string(raw), "returned 0")
		if res.StatusCode == 400 || res.StatusCode == 405 || (res.StatusCode >= 500 && !missing) {
			t.Errorf("%s answered %d: %s", name, res.StatusCode, raw)
		}
	}
	probe("radarr history by movie", "GET", RadarrPort, "/radarr", st.RadarrAPIKey, "/api/v3/history?page=1&pageSize=50&sortKey=date&sortDirection=descending&eventType=1&movieIds=1", "")
	probe("sonarr history by episode", "GET", SonarrPort, "/sonarr", st.SonarrAPIKey, "/api/v3/history?page=1&pageSize=50&sortKey=date&sortDirection=descending&eventType=1&episodeId=1", "")
	probe("sonarr episodes of one file", "GET", SonarrPort, "/sonarr", st.SonarrAPIKey, "/api/v3/episode?episodeFileId=1", "")
	probe("sonarr unmonitor episodes", "PUT", SonarrPort, "/sonarr", st.SonarrAPIKey, "/api/v3/episode/monitor", `{"episodeIds":[1],"monitored":false}`)
	probe("radarr unmonitor movies", "PUT", RadarrPort, "/radarr", st.RadarrAPIKey, "/api/v3/movie/editor", `{"movieIds":[1],"monitored":false}`)
	probe("radarr delete movie file", "DELETE", RadarrPort, "/radarr", st.RadarrAPIKey, "/api/v3/moviefile/999", "")
	probe("sonarr delete episode file", "DELETE", SonarrPort, "/sonarr", st.SonarrAPIKey, "/api/v3/episodefile/999", "")
	probe("radarr movie list", "GET", RadarrPort, "/radarr", st.RadarrAPIKey, "/api/v3/movie", "")
	probe("sonarr series list", "GET", SonarrPort, "/sonarr", st.SonarrAPIKey, "/api/v3/series", "")

	// A second pass must change nothing, and a forced reconfigure must not duplicate anything.
	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	r.mu.Lock()
	r.configured = false
	r.mu.Unlock()
	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("third reconcile: %v", err)
	}
	var clients []map[string]any
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/radarr/api/v3/downloadclient", RadarrPort), nil)
	req.Header.Set("X-Api-Key", st.RadarrAPIKey)
	res2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.NewDecoder(res2.Body).Decode(&clients)
	res2.Body.Close()
	if len(clients) != 1 {
		t.Errorf("download clients after reconfigure: %d", len(clients))
	}
}
