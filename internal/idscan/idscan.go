// Package idscan reads a channel by message id, newest first.
//
// Bots cannot read channel history, only ask for ids, so the channel is walked by id. The walk
// keeps two marks for each channel:
//
//	high  the newest id seen
//	low   every id from here up has been handled
//
// A channel met for the first time is probed to find its newest message, and that is where the walk
// starts. From there it goes down. New messages always come first: Up reads everything above high,
// and Down checks for new ones every few batches, so a fresh post never waits behind old history.
// When Down reaches id 1 the channel is covered and only Up has work left.
package idscan

import (
	"context"
	"strconv"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
)

// Store keeps the two marks.
type Store interface {
	StateValue(ctx context.Context, key string) (string, error)
	SetStateValue(ctx context.Context, key, value string) error
}

// Scan walks one channel.
type Scan struct {
	Lookup func(ctx context.Context, ids []int) ([]media.Message, error)
	// Handle is called for every message that exists. An error stops the walk, and the marks stay
	// where they were, so the same batch is read again later.
	Handle func(ctx context.Context, m media.Message) error
	Store  Store
	// Name starts the keys the marks are saved under.
	Name  string
	Batch int
	Pace  time.Duration
	// Legacy returns the newest id an older version saved, or zero. A channel that has one was
	// already read from the start, so its low mark is 1.
	Legacy func(ctx context.Context) (int, error)
	// Saved is called with the newest id each time it moves, so older code that reads it still works.
	Saved func(ctx context.Context, high int) error
}

func (s *Scan) batch() int {
	if s.Batch <= 0 {
		return 100
	}
	return s.Batch
}

type marks struct{ high, low int }

func (s *Scan) load(ctx context.Context) (marks, error) {
	var m marks
	for i, key := range []string{s.Name + ".high", s.Name + ".low"} {
		raw, err := s.Store.StateValue(ctx, key)
		if err != nil {
			return m, err
		}
		n, _ := strconv.Atoi(raw)
		if i == 0 {
			m.high = n
		} else {
			m.low = n
		}
	}
	if m.high == 0 && m.low == 0 && s.Legacy != nil {
		old, err := s.Legacy(ctx)
		if err != nil {
			return m, err
		}
		if old > 0 {
			m.high, m.low = old, 1
		}
	}
	return m, nil
}

func (s *Scan) save(ctx context.Context, m marks) error {
	if err := s.Store.SetStateValue(ctx, s.Name+".high", strconv.Itoa(m.high)); err != nil {
		return err
	}
	if err := s.Store.SetStateValue(ctx, s.Name+".low", strconv.Itoa(m.low)); err != nil {
		return err
	}
	if s.Saved != nil {
		return s.Saved(ctx, m.high)
	}
	return nil
}

func ids(start, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = start + i
	}
	return out
}

func (s *Scan) pause(ctx context.Context) error {
	if s.Pace <= 0 {
		return ctx.Err()
	}
	select {
	case <-time.After(s.Pace):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// read handles one batch and reports whether any message in it exists.
func (s *Scan) read(ctx context.Context, start int) (seen bool, top int, err error) {
	msgs, err := s.Lookup(ctx, ids(start, s.batch()))
	if err != nil {
		return false, 0, err
	}
	for _, m := range msgs {
		if !m.Exists {
			continue
		}
		seen = true
		top = max(top, m.ID)
		if err := s.Handle(ctx, m); err != nil {
			return seen, top, err
		}
	}
	return seen, top, nil
}

// probe looks at one batch without handling it.
func (s *Scan) probe(ctx context.Context, start int) (bool, error) {
	msgs, err := s.Lookup(ctx, ids(start, s.batch()))
	if err != nil {
		return false, err
	}
	for _, m := range msgs {
		if m.Exists {
			return true, nil
		}
	}
	return false, nil
}

// beyond reports that nothing exists from start on, as far as window ids tell. Deleted messages
// leave holes, so a single empty batch is not proof.
func (s *Scan) beyond(ctx context.Context, start, window int) (bool, error) {
	for off := 0; off < max(window, s.batch()); off += s.batch() {
		hit, err := s.probe(ctx, start+off)
		if err != nil || hit {
			return false, err
		}
		if err := s.pause(ctx); err != nil {
			return false, err
		}
	}
	return true, nil
}

// find locates the newest part of a channel that has no marks yet. It doubles the id until it is
// past the end, then halves back. Afterwards low is the start of a batch that holds a message, and
// Up reads from there to the end.
func (s *Scan) find(ctx context.Context, window int) (marks, error) {
	batch := s.batch()
	hit, miss := 0, 0
	for pos, step := 1, batch; ; pos, step = pos+step, step*2 {
		past, err := s.beyond(ctx, pos, window)
		if err != nil {
			return marks{}, err
		}
		if past {
			miss = pos
			break
		}
		hit = pos
		if pos > 1<<30 {
			return marks{}, nil
		}
		if err := s.pause(ctx); err != nil {
			return marks{}, err
		}
	}
	if hit == 0 {
		// Nothing in the first ids. The walk still looks up to the window from id 1.
		return marks{high: 0, low: 1}, nil
	}
	for miss-hit > batch {
		mid := hit + (miss-hit)/2/batch*batch
		if mid <= hit {
			break
		}
		past, err := s.beyond(ctx, mid, window)
		if err != nil {
			return marks{}, err
		}
		if past {
			miss = mid
		} else {
			hit = mid
		}
		if err := s.pause(ctx); err != nil {
			return marks{}, err
		}
	}
	return marks{high: hit - 1, low: hit}, nil
}

// Up reads the ids above the newest one seen, and stops after window ids in a row hold nothing.
// A channel met for the first time is probed for its newest part first.
func (s *Scan) Up(ctx context.Context, window int) error {
	m, err := s.load(ctx)
	if err != nil {
		return err
	}
	if m.high == 0 && m.low == 0 {
		if m, err = s.find(ctx, window); err != nil {
			return err
		}
		if err := s.save(ctx, m); err != nil {
			return err
		}
	}
	return s.up(ctx, m, window)
}

func (s *Scan) up(ctx context.Context, m marks, window int) error {
	empty := 0
	for next := m.high + 1; empty < window; next += s.batch() {
		seen, top, err := s.read(ctx, next)
		if err != nil {
			return err
		}
		if seen {
			empty = 0
			m.high = max(m.high, top)
			if err := s.save(ctx, m); err != nil {
				return err
			}
		} else {
			empty += s.batch()
		}
		if err := s.pause(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Down reads older ids, from the low mark towards id 1, at most maxBatches batches (negative means
// all of them). Every upEvery batches it first looks for new messages, which always go first. It
// reports whether the channel is covered down to id 1.
func (s *Scan) Down(ctx context.Context, maxBatches, upEvery int) (bool, error) {
	m, err := s.load(ctx)
	if err != nil {
		return false, err
	}
	if m.high == 0 && m.low == 0 {
		return false, nil // Up has not met this channel yet
	}
	if upEvery <= 0 {
		upEvery = 3
	}
	for done := 0; m.low > 1; done++ {
		if maxBatches >= 0 && done >= maxBatches {
			return false, nil
		}
		if done > 0 && done%upEvery == 0 {
			if err := s.up(ctx, m, s.batch()); err != nil {
				return false, err
			}
			if m, err = s.load(ctx); err != nil {
				return false, err
			}
		}
		start := max(1, m.low-s.batch())
		n := m.low - start
		msgs, err := s.Lookup(ctx, ids(start, n))
		if err != nil {
			return false, err
		}
		for _, msg := range msgs {
			if !msg.Exists {
				continue
			}
			if err := s.Handle(ctx, msg); err != nil {
				return false, err
			}
		}
		m.low = start
		if err := s.save(ctx, m); err != nil {
			return false, err
		}
		if err := s.pause(ctx); err != nil {
			return false, err
		}
	}
	return true, nil
}
