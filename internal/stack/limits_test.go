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
