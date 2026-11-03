package gateway

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
)

const (
	// paceFactor is how many times faster than playback ffmpeg may read. It leaves room to
	// transcode and for a player's buffer.
	paceFactor = 3
	// paceBurst is read at full speed first, because ffmpeg analyses the input before it
	// produces anything and a slow start would delay playback.
	paceBurst = 64 << 20
	// paceFloor keeps very low bitrates from being throttled to a crawl.
	paceFloor = 2 << 20
	// paceIdleReset is how long a file's budget lasts without any read.
	paceIdleReset = time.Minute
)

// pacer limits a reader to a steady rate after an initial burst.
type pacer struct {
	mu             sync.Mutex
	lastUsed       time.Time
	bytesPerSecond int64
	burst          int64
	started        time.Time
	read           int64
	now            func() time.Time
	sleep          func(context.Context, time.Duration) error
	// hold pauses the reader while it returns true. Only background work has one.
	hold func() bool
}

// holdPoll is how often a paused background reader looks again.
const holdPoll = 2 * time.Second

// sharedPacers gives all ffmpeg connections to one file a single budget. ffmpeg reconnects whenever
// it seeks, which an MP4 with its index at the end does several times at the start. A budget per
// connection would hand out a new burst each time: one such file read 207 MiB in 11 s.
type sharedPacers struct {
	mu    sync.Mutex
	byDoc map[int64]*pacer
}

// get returns the budget of a file. One left unused for a while starts over, so a later playback
// gets its burst back.
func (s *sharedPacers) get(doc int64, fresh func() *pacer) *pacer {
	if fresh == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byDoc == nil {
		s.byDoc = map[int64]*pacer{}
	}
	now := time.Now()
	for id, p := range s.byDoc {
		if now.Sub(p.lastTouched()) > paceIdleReset {
			delete(s.byDoc, id)
		}
	}
	p, ok := s.byDoc[doc]
	if !ok {
		p = fresh()
		p.touch(now)
		s.byDoc[doc] = p
	}
	return p
}

func (p *pacer) touch(t time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastUsed = t
}

func (p *pacer) lastTouched() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastUsed
}

// newBackgroundPacer limits work that nobody is waiting for: no burst, a fixed speed, and a full
// stop while anything is playing.
func newBackgroundPacer(bytesPerSecond int64, busy func() bool) *pacer {
	return &pacer{bytesPerSecond: bytesPerSecond, now: time.Now, sleep: sleepContext, hold: busy}
}

// newPacer returns a limit for ffmpeg reading one file, or nil for any other client. ffmpeg reads
// as fast as it can whether or not playback needs the data, and Jellyfin's own throttle does not
// apply to .strm input. Without a known duration there is no rate to derive, so no limit.
func newPacer(r *http.Request, f media.File) *pacer {
	if !strings.HasPrefix(r.UserAgent(), "Lavf") || f.DurationSeconds <= 0 {
		return nil
	}
	rate := max(paceFactor*f.Size/int64(f.DurationSeconds), paceFloor)
	return &pacer{bytesPerSecond: rate, burst: paceBurst, now: time.Now, sleep: sleepContext}
}

// limit applies the budget of the requested file to one reader.
func (e *Engine) limit(r *http.Request, f media.File) *pacer {
	first := newPacer(r, f)
	if first == nil {
		return nil
	}
	return e.pacers.get(f.DocID, func() *pacer { return first })
}

// wait blocks until n more bytes are within the limit.
func (p *pacer) wait(ctx context.Context, n int64) error {
	p.mu.Lock()
	if p.started.IsZero() {
		p.started = p.now()
	}
	p.mu.Unlock()
	for p.hold != nil && p.hold() {
		if err := p.sleep(ctx, holdPoll); err != nil {
			return err
		}
		// Time spent paused does not count as credit for reading faster afterwards.
		p.mu.Lock()
		p.started = p.started.Add(holdPoll)
		p.mu.Unlock()
	}
	p.mu.Lock()
	p.read += n
	p.lastUsed = p.now()
	over := p.read - p.burst
	delay := time.Duration(float64(over)/float64(p.bytesPerSecond)*float64(time.Second)) - p.now().Sub(p.started)
	p.mu.Unlock()
	if over <= 0 || delay <= 0 {
		return nil
	}
	return p.sleep(ctx, delay)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
