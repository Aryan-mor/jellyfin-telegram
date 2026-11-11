//go:build live

package seerr

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestLiveBundleInstallsAndRuns downloads the published bundle for this system, checks it against
// the pinned hash, starts it as a child process and waits for it to answer. It needs the network,
// downloads a few hundred MB, and takes minutes:
//
//	go test -tags live -run TestLiveBundle -timeout 30m ./internal/seerr
func TestLiveBundleInstallsAndRuns(t *testing.T) {
	root := t.TempDir()
	if dir := os.Getenv("JFTG_LIVE_DIR"); dir != "" {
		root = dir
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	began := time.Now()
	bundle, err := Install(ctx, root, http.DefaultClient, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("download and unpack: %v", time.Since(began).Round(time.Second))
	srv := &Server{Root: root, APIKey: "live-test-key", Bundle: bundle}
	started := time.Now()
	srv.Prepare()
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Run(ctx)
	}()
	if err := srv.WaitReady(ctx, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	t.Logf("first start until it answers: %v", time.Since(started).Round(time.Second))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL()+"/api/v1/status", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status endpoint answered %d", res.StatusCode)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Error("Seerr did not stop when asked")
	}
}
