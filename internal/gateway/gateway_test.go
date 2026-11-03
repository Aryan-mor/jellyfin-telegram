package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/blockstore"
	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
)

const (
	testToken   = "secret-token"
	testChannel = int64(555)
	testMessage = 10
)

type fakeFetcher struct {
	data   []byte
	calls  atomic.Int64
	fail   atomic.Bool
	delay  time.Duration
	mu     sync.Mutex
	blocks map[int64]int
}

func (f *fakeFetcher) FetchBlock(_ context.Context, _ int, _, block, _ int64) ([]byte, error) {
	f.calls.Add(1)
	f.mu.Lock()
	if f.blocks == nil {
		f.blocks = map[int64]int{}
	}
	f.blocks[block]++
	f.mu.Unlock()
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.fail.Load() {
		return nil, errors.New("telegram is down")
	}
	start := block * media.BlockSize
	end := min(start+media.BlockSize, int64(len(f.data)))
	return f.data[start:end], nil
}

type fakeFiles map[int]media.File

func (m fakeFiles) Get(_ context.Context, id int) (media.File, error) {
	f, ok := m[id]
	if !ok {
		return media.File{}, catalog.ErrNotFound
	}
	return f, nil
}

type bench struct {
	srv     *httptest.Server
	engine  *Engine
	fetcher *fakeFetcher
	store   *blockstore.Store
	data    []byte
	url     string
}

func setup(t *testing.T, size int64, readahead int) *bench {
	t.Helper()
	data := make([]byte, size)
	rand.New(rand.NewSource(1)).Read(data)
	store, err := blockstore.Open(t.TempDir(), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fetcher := &fakeFetcher{data: data}
	engine := NewEngine(store, fetcher, log)
	files := fakeFiles{testMessage: {
		MessageID: testMessage, ChannelID: testChannel, DocID: 77, Size: size,
		MIME: "video/x-matroska", Date: time.Unix(1700000000, 0).UTC(),
	}}
	srv := httptest.NewServer(Handler(engine, files, Options{Token: testToken, ChannelID: testChannel, Readahead: readahead, BackgroundBytesPerSecond: 1 << 40}, log))
	t.Cleanup(srv.Close)
	return &bench{
		srv: srv, engine: engine, fetcher: fetcher, store: store, data: data,
		url: fmt.Sprintf("%s/s/%s/%d/%d", srv.URL, testToken, testChannel, testMessage),
	}
}

func (b *bench) get(t *testing.T, rangeHeader string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, b.url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, body
}

func TestWholeFile(t *testing.T) {
	b := setup(t, 3*media.BlockSize+1234, 0)
	res, body := b.get(t, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	if !bytes.Equal(body, b.data) {
		t.Fatal("body differs from the file")
	}
	if res.Header.Get("Accept-Ranges") != "bytes" {
		t.Error("ranges not advertised")
	}
	if res.Header.Get("Content-Type") != "video/x-matroska" {
		t.Errorf("content type %q", res.Header.Get("Content-Type"))
	}
}

func TestRanges(t *testing.T) {
	size := 3*media.BlockSize + 1234
	b := setup(t, size, 0)
	tests := []struct {
		name, header string
		from, to     int64
	}{
		{"head", "bytes=0-99", 0, 99},
		{"crosses a block", fmt.Sprintf("bytes=%d-%d", media.BlockSize-10, media.BlockSize+9), media.BlockSize - 10, media.BlockSize + 9},
		{"tail suffix", "bytes=-100", size - 100, size - 1},
		{"open end", fmt.Sprintf("bytes=%d-", size-5000), size - 5000, size - 1},
		{"last byte", fmt.Sprintf("bytes=%d-%d", size-1, size-1), size - 1, size - 1},
		{"end past file", fmt.Sprintf("bytes=%d-%d", size-10, size+100), size - 10, size - 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, body := b.get(t, tc.header)
			if res.StatusCode != http.StatusPartialContent {
				t.Fatalf("status %d", res.StatusCode)
			}
			if !bytes.Equal(body, b.data[tc.from:tc.to+1]) {
				t.Fatal("wrong bytes")
			}
			want := fmt.Sprintf("bytes %d-%d/%d", tc.from, tc.to, size)
			if got := res.Header.Get("Content-Range"); got != want {
				t.Errorf("Content-Range %q, want %q", got, want)
			}
		})
	}
	res, _ := b.get(t, fmt.Sprintf("bytes=%d-", size))
	if res.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("range past the end gave status %d", res.StatusCode)
	}
}

func TestHeadReadsNothingFromTelegram(t *testing.T) {
	b := setup(t, 2*media.BlockSize, 0)
	res, err := http.Head(b.url)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || res.ContentLength != 2*media.BlockSize {
		t.Fatalf("status %d, length %d", res.StatusCode, res.ContentLength)
	}
	if b.fetcher.calls.Load() != 0 {
		t.Errorf("HEAD made %d Telegram calls", b.fetcher.calls.Load())
	}
}

func TestAccessControl(t *testing.T) {
	b := setup(t, media.BlockSize, 0)
	tests := []struct {
		path string
		want int
	}{
		{fmt.Sprintf("/s/wrong/%d/%d", testChannel, testMessage), http.StatusForbidden},
		{fmt.Sprintf("/s/%s/999/%d", testToken, testMessage), http.StatusNotFound},
		{fmt.Sprintf("/s/%s/%d/404", testToken, testChannel), http.StatusNotFound},
		{fmt.Sprintf("/s/%s/%d/abc", testToken, testChannel), http.StatusNotFound},
		{"/", http.StatusNotFound},
	}
	for _, tc := range tests {
		res, err := http.Get(b.srv.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != tc.want {
			t.Errorf("%s: status %d, want %d", tc.path, res.StatusCode, tc.want)
		}
	}
	if b.fetcher.calls.Load() != 0 {
		t.Error("a refused request reached Telegram")
	}
}

func TestCacheAvoidsSecondTelegramCall(t *testing.T) {
	b := setup(t, 2*media.BlockSize, 0)
	b.get(t, "")
	first := b.fetcher.calls.Load()
	if first != 2 {
		t.Fatalf("first read made %d calls, want 2", first)
	}
	_, body := b.get(t, "")
	if b.fetcher.calls.Load() != first {
		t.Errorf("second read made %d new calls", b.fetcher.calls.Load()-first)
	}
	if !bytes.Equal(body, b.data) {
		t.Error("cached body differs")
	}
}

func TestProbeWindowPinsOnlyWhatWasRead(t *testing.T) {
	b := setup(t, 5*media.BlockSize, 8)
	end := b.engine.BeginProbe(testMessage)
	b.get(t, fmt.Sprintf("bytes=0-%d", media.BlockSize-1))
	b.get(t, "bytes=-100")
	end()

	if b.fetcher.calls.Load() != 2 {
		t.Fatalf("probe made %d Telegram calls, want 2 and no read-ahead", b.fetcher.calls.Load())
	}
	if used := b.store.Used(); used != 0 {
		t.Errorf("probe blocks are evictable: %d bytes", used)
	}

	// A later read outside the window is not pinned.
	b.get(t, fmt.Sprintf("bytes=%d-%d", 2*media.BlockSize, 2*media.BlockSize))
	if b.store.Used() != media.BlockSize {
		t.Errorf("used = %d, want one evictable block", b.store.Used())
	}
}

func TestProbeWindowCountsBlocksRead(t *testing.T) {
	b := setup(t, 5*media.BlockSize, 0)
	end := b.engine.BeginProbe(testMessage)
	if got := end(); got != 0 {
		t.Errorf("empty window counted %d blocks", got)
	}
	end = b.engine.BeginProbe(testMessage)
	b.get(t, "bytes=0-10")
	if got := end(); got == 0 {
		t.Error("window counted no blocks after a read")
	}
	if b.engine.isProbing(testMessage) {
		t.Error("window stayed open")
	}
}

func TestCachedBlockIsPinnedByLaterProbe(t *testing.T) {
	b := setup(t, 2*media.BlockSize, 0)
	b.get(t, "bytes=0-10")
	if b.store.Used() != media.BlockSize {
		t.Fatal("block should start evictable")
	}
	end := b.engine.BeginProbe(testMessage)
	b.get(t, "bytes=0-10")
	end()
	if b.store.Used() != 0 {
		t.Error("cached block was not pinned by the probe")
	}
}

func TestReadAheadStartsOnlyWhenSequential(t *testing.T) {
	b := setup(t, 20*media.BlockSize, 4)

	// One block only: no read-ahead.
	b.get(t, fmt.Sprintf("bytes=0-%d", media.BlockSize-1))
	time.Sleep(50 * time.Millisecond)
	if b.fetcher.calls.Load() != 1 {
		t.Fatalf("single block read made %d calls", b.fetcher.calls.Load())
	}

	// Two blocks: read-ahead covers the next four.
	b.get(t, fmt.Sprintf("bytes=%d-%d", 5*media.BlockSize, 6*media.BlockSize+1))
	deadline := time.Now().Add(2 * time.Second)
	for b.fetcher.calls.Load() < 7 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := b.fetcher.calls.Load(); got != 7 {
		t.Fatalf("calls = %d, want 7", got)
	}
	for block := int64(7); block <= 10; block++ {
		if !waitHas(b.store, 77, block) {
			t.Errorf("block %d was not read ahead", block)
		}
	}
	if b.store.Has(77, 11) {
		t.Error("read-ahead went past its window")
	}
}

func TestPinnedBlocksDoNotTriggerReadAhead(t *testing.T) {
	b := setup(t, 20*media.BlockSize, 4)
	head := fmt.Sprintf("bytes=0-%d", 2*media.BlockSize-1)

	end := b.engine.BeginProbe(testMessage)
	b.get(t, head)
	end()
	if got := b.fetcher.calls.Load(); got != 2 {
		t.Fatalf("probe made %d calls, want 2", got)
	}

	// A repeat probe arrives as an ordinary request and must stay off Telegram.
	b.get(t, head)
	time.Sleep(100 * time.Millisecond)
	if got := b.fetcher.calls.Load(); got != 2 {
		t.Fatalf("repeat probe made %d new Telegram calls", got-2)
	}

	// Playback that runs past the pinned blocks does read ahead.
	b.get(t, fmt.Sprintf("bytes=0-%d", 3*media.BlockSize-1))
	for block := int64(2); block <= 6; block++ {
		if !waitHas(b.store, 77, block) {
			t.Errorf("block %d was not fetched once playback left the pinned area", block)
		}
	}
}

func TestPacerAllowsBurstThenHoldsTheRate(t *testing.T) {
	clock := time.Unix(1000, 0)
	var slept time.Duration
	p := &pacer{
		bytesPerSecond: 1000, burst: 5000,
		now:   func() time.Time { return clock },
		sleep: func(_ context.Context, d time.Duration) error { slept = d; return nil },
	}
	ctx := context.Background()
	if err := p.wait(ctx, 5000); err != nil || slept != 0 {
		t.Fatalf("burst was delayed by %v, %v", slept, err)
	}
	if err := p.wait(ctx, 2000); err != nil || slept != 2*time.Second {
		t.Fatalf("slept %v, %v, want 2s for 2000 bytes at 1000/s", slept, err)
	}
	slept = 0
	clock = clock.Add(10 * time.Second)
	if err := p.wait(ctx, 1000); err != nil || slept != 0 {
		t.Fatalf("a reader behind schedule was delayed by %v, %v", slept, err)
	}
}

func TestPacerAppliesOnlyToFFmpegWithAKnownDuration(t *testing.T) {
	req := func(ua string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("User-Agent", ua)
		return r
	}
	f := media.File{Size: 3600 << 20, DurationSeconds: 3600}
	p := newPacer(req("Lavf/62.3.100"), f)
	if p == nil || p.bytesPerSecond != 3<<20 {
		t.Fatalf("pacer = %+v, want 3 times 1 MiB/s", p)
	}
	if newPacer(req("Mozilla/5.0"), f) != nil {
		t.Error("a player was limited")
	}
	if newPacer(req("Lavf/62.3.100"), media.File{Size: 1 << 30}) != nil {
		t.Error("no duration should mean no limit")
	}
	low := newPacer(req("Lavf/62.3.100"), media.File{Size: 1 << 20, DurationSeconds: 3600})
	if low == nil || low.bytesPerSecond != paceFloor {
		t.Errorf("low bitrate rate = %+v", low)
	}
}

func TestBackgroundPacerPausesWhileBusyWithoutEarningCredit(t *testing.T) {
	clock := time.Unix(1000, 0)
	busyFor := 3
	var slept []time.Duration
	p := newBackgroundPacer(1000, func() bool {
		if busyFor > 0 {
			busyFor--
			return true
		}
		return false
	})
	p.now = func() time.Time { return clock }
	p.sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		clock = clock.Add(d)
		return nil
	}
	if err := p.wait(context.Background(), 1000); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 4 || slept[0] != holdPoll || slept[3] != time.Second {
		t.Errorf("slept %v, want three pauses and then one second for 1000 bytes", slept)
	}
}

func TestBackgroundRequestsAreLimitedAndDoNotHoldEachOther(t *testing.T) {
	b := setup(t, 2*media.BlockSize, 0)
	req := func(background bool) {
		r, err := http.NewRequest(http.MethodGet, b.url, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Range", "bytes=0-100")
		if background {
			r.Header.Set(BackgroundHeader, "1")
		}
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}
	if b.engine.busy() {
		t.Fatal("busy before any read")
	}
	req(true)
	if b.engine.busy() {
		t.Error("background work marked the engine busy")
	}
	req(false)
	if !b.engine.busy() {
		t.Error("a player read did not mark the engine busy")
	}
}

func TestCachedBlocksAreNotPaced(t *testing.T) {
	b := setup(t, 3*media.BlockSize, 0)
	file := media.File{MessageID: testMessage, DocID: 77, Size: 3 * media.BlockSize}
	if _, err := b.engine.Block(context.Background(), file, 1); err != nil {
		t.Fatal(err)
	}
	waits := 0
	r := newReader(context.Background(), b.engine, file, 0)
	r.pacer = &pacer{
		bytesPerSecond: 1 << 30, now: time.Now,
		sleep: func(context.Context, time.Duration) error { return nil },
		hold:  func() bool { waits++; return false },
	}
	if _, err := io.ReadAll(r); err != nil {
		t.Fatal(err)
	}
	if waits != 2 {
		t.Errorf("paced %d blocks, want only the 2 uncached ones", waits)
	}
}

// Real case: an MP4 made ffmpeg reconnect while seeking, and each connection got a new burst.
func TestReconnectsShareOneBudget(t *testing.T) {
	var pool sharedPacers
	made := 0
	fresh := func() *pacer {
		made++
		return &pacer{bytesPerSecond: 1000, burst: 5000, now: time.Now, sleep: sleepContext}
	}
	first := pool.get(1, fresh)
	second := pool.get(1, fresh)
	other := pool.get(2, fresh)
	if first != second {
		t.Error("a reconnect got a new budget")
	}
	if first == other {
		t.Error("two files share a budget")
	}
	if made != 2 {
		t.Errorf("made %d budgets", made)
	}
}

func TestIdleBudgetStartsOver(t *testing.T) {
	var pool sharedPacers
	fresh := func() *pacer { return &pacer{bytesPerSecond: 1000, burst: 5000, now: time.Now, sleep: sleepContext} }
	first := pool.get(1, fresh)
	first.touch(time.Now().Add(-2 * paceIdleReset))
	if pool.get(1, fresh) == first {
		t.Error("a budget left idle was kept")
	}
}

func TestProbeIsNotPaced(t *testing.T) {
	b := setup(t, 2*media.BlockSize, 0)
	end := b.engine.BeginProbe(testMessage)
	defer end()
	r := newReader(context.Background(), b.engine, media.File{MessageID: testMessage, DocID: 77, Size: 2 * media.BlockSize}, 0)
	blocked := false
	r.pacer = &pacer{
		bytesPerSecond: 1, burst: 0, now: time.Now,
		sleep: func(context.Context, time.Duration) error { blocked = true; return nil },
	}
	if _, err := io.ReadAll(r); err != nil {
		t.Fatal(err)
	}
	if blocked {
		t.Error("a probe was delayed")
	}
}

func TestConcurrentReadersShareOneFetch(t *testing.T) {
	b := setup(t, media.BlockSize, 0)
	b.fetcher.delay = 100 * time.Millisecond
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := http.Get(b.url)
			if err != nil {
				t.Error(err)
				return
			}
			defer res.Body.Close()
			if _, err := io.Copy(io.Discard, res.Body); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := b.fetcher.calls.Load(); got != 1 {
		t.Errorf("Telegram called %d times, want 1", got)
	}
}

func TestTelegramFailureIsCleanBadGateway(t *testing.T) {
	b := setup(t, media.BlockSize, 0)
	b.fetcher.fail.Store(true)
	res, _ := b.get(t, "")
	if res.StatusCode != http.StatusBadGateway {
		t.Fatalf("status %d", res.StatusCode)
	}
	b.fetcher.fail.Store(false)
	res, body := b.get(t, "")
	if res.StatusCode != http.StatusOK || !bytes.Equal(body, b.data) {
		t.Fatalf("no recovery: status %d", res.StatusCode)
	}
}

func TestRangeStart(t *testing.T) {
	tests := []struct {
		header string
		want   int64
		ok     bool
	}{
		{"", 0, true},
		{"bytes=100-", 100, true},
		{"bytes=100-200", 100, true},
		{"bytes=-50", 0, false},
		{"bytes=0-1,5-6", 0, false},
		{"bytes=1000-", 0, false},
		{"items=1-2", 0, false},
	}
	for _, tc := range tests {
		got, ok := rangeStart(tc.header, 1000)
		if got != tc.want || ok != tc.ok {
			t.Errorf("rangeStart(%q) = %d, %v", tc.header, got, ok)
		}
	}
}

func waitHas(s *blockstore.Store, doc, block int64) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.Has(doc, block) {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}
