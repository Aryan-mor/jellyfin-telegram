package tgpool

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/tgerr"

	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
)

type fakeWorker struct {
	id          int64
	notReady    bool
	onGet       func(offset int64, limit int) ([]byte, error)
	calls       atomic.Int64
	invalidated atomic.Int64
	running     atomic.Int64
	peak        atomic.Int64
	delay       time.Duration
}

func (f *fakeWorker) settled() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func (f *fakeWorker) status() BotStatus { return BotStatus{ID: f.id, Ready: !f.notReady} }

func (f *fakeWorker) getBlock(_ context.Context, _ int, _, offset int64, limit int) ([]byte, error) {
	f.calls.Add(1)
	n := f.running.Add(1)
	defer f.running.Add(-1)
	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			break
		}
	}
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	return f.onGet(offset, limit)
}

func (f *fakeWorker) messages(context.Context, []int) ([]media.Message, error) { return nil, nil }
func (f *fakeWorker) thumbnail(context.Context, int) ([]byte, error)           { return nil, nil }
func (f *fakeWorker) invalidate(int)                                           { f.invalidated.Add(1) }
func (f *fakeWorker) deleteMessages(context.Context, []int) error              { return nil }
func (f *fakeWorker) sendDocument(context.Context, string, string, []byte, int) (int, error) {
	return 77, nil
}

func full(_ int64, limit int) ([]byte, error) { return bytes.Repeat([]byte{'x'}, limit), nil }

func pool(concurrency int, w ...worker) *Pool {
	return newPool(w, concurrency, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestFetchBlockShortLastBlock(t *testing.T) {
	const size = media.BlockSize + 500
	w := &fakeWorker{id: 1, onGet: func(offset int64, limit int) ([]byte, error) {
		if offset != media.BlockSize {
			t.Errorf("offset = %d", offset)
		}
		if limit != int(media.BlockSize) {
			t.Errorf("limit = %d", limit)
		}
		return bytes.Repeat([]byte{'x'}, 500), nil
	}}
	got, err := pool(1, w).FetchBlock(testCtx(t), 1, 1, 1, size)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 500 {
		t.Fatalf("len = %d", len(got))
	}
}

func TestFetchBlockJoinsShortAnswers(t *testing.T) {
	var limits []int
	var mu sync.Mutex
	w := &fakeWorker{id: 1, onGet: func(offset int64, limit int) ([]byte, error) {
		mu.Lock()
		limits = append(limits, limit)
		mu.Unlock()
		if offset == 0 {
			return bytes.Repeat([]byte{'a'}, 4096), nil
		}
		if offset%media.BlockSize+int64(limit) > media.BlockSize {
			t.Errorf("read crosses a 1 MiB boundary: offset %d limit %d", offset, limit)
		}
		return bytes.Repeat([]byte{'b'}, limit), nil
	}}
	got, err := pool(1, w).FetchBlock(testCtx(t), 1, 1, 0, 5*media.BlockSize)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(got)) != media.BlockSize {
		t.Fatalf("len = %d", len(got))
	}
	if got[0] != 'a' || got[4096] != 'b' {
		t.Fatal("parts in the wrong place")
	}
	if len(limits) != 2 || limits[1] != int(media.BlockSize)-4096 {
		t.Fatalf("limits = %v", limits)
	}
}

func TestFetchBlockPastEnd(t *testing.T) {
	w := &fakeWorker{id: 1, onGet: full}
	if _, err := pool(1, w).FetchBlock(testCtx(t), 1, 1, 5, media.BlockSize); err == nil {
		t.Fatal("expected an error")
	}
	if w.calls.Load() != 0 {
		t.Fatal("telegram was called")
	}
}

func TestFloodWaitMovesToAnotherBot(t *testing.T) {
	bad := &fakeWorker{id: 1, onGet: func(int64, int) ([]byte, error) {
		return nil, tgerr.New(420, "FLOOD_WAIT_30")
	}}
	good := &fakeWorker{id: 2, onGet: full}
	p := pool(4, bad, good)
	ctx := testCtx(t)
	for b := range int64(5) {
		if _, err := p.FetchBlock(ctx, 1, 1, b, 10*media.BlockSize); err != nil {
			t.Fatal(err)
		}
	}
	if bad.calls.Load() != 1 {
		t.Errorf("flooded bot was used %d times, want 1", bad.calls.Load())
	}
	if good.calls.Load() != 5 {
		t.Errorf("healthy bot was used %d times, want 5", good.calls.Load())
	}
	stats := p.Stats()
	if stats[0].Floods != 1 || stats[1].Calls != 5 {
		t.Errorf("stats = %+v", stats)
	}
}

func TestPremiumFloodWaitIsAlsoBenched(t *testing.T) {
	bad := &fakeWorker{id: 1, onGet: func(int64, int) ([]byte, error) {
		return nil, tgerr.New(420, "FLOOD_PREMIUM_WAIT_30")
	}}
	good := &fakeWorker{id: 2, onGet: full}
	if _, err := pool(4, bad, good).FetchBlock(testCtx(t), 1, 1, 0, media.BlockSize); err != nil {
		t.Fatal(err)
	}
	if bad.calls.Load() != 1 {
		t.Errorf("flooded bot was used %d times", bad.calls.Load())
	}
}

func TestFileReferenceExpiredRefreshesThenRetries(t *testing.T) {
	var calls atomic.Int64
	w := &fakeWorker{id: 1}
	w.onGet = func(_ int64, limit int) ([]byte, error) {
		if calls.Add(1) == 1 {
			return nil, tgerr.New(400, "FILE_REFERENCE_EXPIRED")
		}
		return bytes.Repeat([]byte{'x'}, limit), nil
	}
	if _, err := pool(1, w).FetchBlock(testCtx(t), 9, 1, 0, media.BlockSize); err != nil {
		t.Fatal(err)
	}
	if w.invalidated.Load() != 1 {
		t.Errorf("invalidated %d times, want 1", w.invalidated.Load())
	}
}

func TestLostAccessBenchesBot(t *testing.T) {
	bad := &fakeWorker{id: 1, onGet: func(int64, int) ([]byte, error) {
		return nil, tgerr.New(400, "CHANNEL_PRIVATE")
	}}
	good := &fakeWorker{id: 2, onGet: full}
	p := pool(4, bad, good)
	ctx := testCtx(t)
	for b := range int64(3) {
		if _, err := p.FetchBlock(ctx, 1, 1, b, 5*media.BlockSize); err != nil {
			t.Fatal(err)
		}
	}
	if bad.calls.Load() != 1 {
		t.Errorf("bot without access was used %d times", bad.calls.Load())
	}
}

func TestMessageGoneDoesNotRetry(t *testing.T) {
	w := &fakeWorker{id: 1, onGet: func(int64, int) ([]byte, error) { return nil, ErrMessageGone }}
	_, err := pool(1, w).FetchBlock(testCtx(t), 1, 1, 0, media.BlockSize)
	if !errors.Is(err, ErrMessageGone) {
		t.Fatalf("err = %v", err)
	}
	if w.calls.Load() != 1 {
		t.Errorf("called %d times", w.calls.Load())
	}
}

func TestPersistentErrorGivesUp(t *testing.T) {
	boom := errors.New("boom")
	w := &fakeWorker{id: 1, onGet: func(int64, int) ([]byte, error) { return nil, boom }}
	_, err := pool(1, w).FetchBlock(testCtx(t), 1, 1, 0, media.BlockSize)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if w.calls.Load() != maxAttempts {
		t.Errorf("called %d times, want %d", w.calls.Load(), maxAttempts)
	}
}

func TestAllBotsFloodedStopsAtDeadline(t *testing.T) {
	w := &fakeWorker{id: 1, onGet: func(int64, int) ([]byte, error) {
		return nil, tgerr.New(420, "FLOOD_WAIT_600")
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := pool(1, w).FetchBlock(ctx, 1, 1, 0, media.BlockSize); err == nil {
		t.Fatal("expected an error")
	}
}

func TestDownloadJoinsBlocks(t *testing.T) {
	size := 2*media.BlockSize + 10
	w := &fakeWorker{id: 1, onGet: func(_ int64, limit int) ([]byte, error) { return bytes.Repeat([]byte{'x'}, limit), nil }}
	got, err := pool(1, w).Download(testCtx(t), 1, 1, size)
	if err != nil || int64(len(got)) != size {
		t.Fatalf("len %d, %v", len(got), err)
	}
}

func TestWarmReadsOnceWithEveryReadyBot(t *testing.T) {
	a := &fakeWorker{id: 1, onGet: full}
	b := &fakeWorker{id: 2, onGet: full}
	off := &fakeWorker{id: 3, onGet: full, notReady: true}
	pool(1, a, b, off).Warm(testCtx(t), 1, 1)
	if a.calls.Load() != 1 || b.calls.Load() != 1 || off.calls.Load() != 0 {
		t.Errorf("calls %d, %d, %d", a.calls.Load(), b.calls.Load(), off.calls.Load())
	}
}

func TestConcurrencyIsCappedPerBotAndSpread(t *testing.T) {
	a := &fakeWorker{id: 1, onGet: full, delay: 5 * time.Millisecond}
	b := &fakeWorker{id: 2, onGet: full, delay: 5 * time.Millisecond}
	skipped := &fakeWorker{id: 3, onGet: full, notReady: true}
	p := pool(2, a, b, skipped)
	ctx := testCtx(t)

	var wg sync.WaitGroup
	for i := range int64(40) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.FetchBlock(ctx, 1, 1, i, 50*media.BlockSize); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if a.peak.Load() > 2 || b.peak.Load() > 2 {
		t.Errorf("per-bot limit exceeded: %d and %d", a.peak.Load(), b.peak.Load())
	}
	if a.calls.Load() == 0 || b.calls.Load() == 0 {
		t.Errorf("load not spread: %d and %d", a.calls.Load(), b.calls.Load())
	}
	if skipped.calls.Load() != 0 {
		t.Error("a bot that is not ready was used")
	}
	if p.ReadyCount() != 2 {
		t.Errorf("ReadyCount = %d", p.ReadyCount())
	}
}

func TestGapHoldsTheSameBot(t *testing.T) {
	w := &fakeWorker{id: 1, onGet: full}
	p := pool(1, w)
	s := p.slots[0]
	if !p.tryTake(s, time.Now(), false) {
		t.Fatal("free bot was not taken")
	}
	p.release(s, true, time.Second)
	if p.tryTake(s, time.Now(), false) {
		t.Fatal("bot ran again during its rest")
	}
	if !p.tryTake(s, time.Now(), true) {
		t.Fatal("a job the person asked for waited out the voluntary rest")
	}
	p.release(s, true, 0)
	s.floodUntil = time.Now().Add(time.Minute)
	if p.tryTake(s, time.Now(), true) {
		t.Fatal("a flood wait let a user job through")
	}
}
