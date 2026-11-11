package hold

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPauseStopsWorkAndStaysPaused(t *testing.T) {
	dir := t.TempDir()
	g, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if g.Paused() {
		t.Fatal("new switch is paused")
	}
	ctx, stop := g.Begin(context.Background())
	if err := g.Set(true); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil {
		t.Fatal("running work was not cancelled")
	}
	stop()
	if _, err := os.Stat(filepath.Join(dir, fileName)); err != nil {
		t.Fatal(err)
	}
	again, err := Open(dir)
	if err != nil || !again.Paused() {
		t.Fatalf("reload: paused=%v err=%v", again != nil && again.Paused(), err)
	}
	done := make(chan struct{})
	go func() {
		if err := again.Wait(context.Background()); err != nil {
			t.Error(err)
		}
		close(done)
	}()
	if err := again.Set(false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("resume did not wake the waiter")
	}
	if _, err := os.Stat(filepath.Join(dir, fileName)); !os.IsNotExist(err) {
		t.Fatalf("file left behind: %v", err)
	}
}

func TestNoSwitchRuns(t *testing.T) {
	Use(nil)
	if Stopped() {
		t.Fatal("stopped")
	}
	ctx, stop := Begin(context.Background())
	stop()
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
	if err := Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}
