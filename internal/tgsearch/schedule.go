package tgsearch

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// schedRec is one title's search history. Times are unix seconds.
type schedRec struct {
	First    int64 `json:"first"`
	Last     int64 `json:"last"`
	Tries    int   `json:"tries"`
	Digital  bool  `json:"digital"`
	Released int64 `json:"released,omitempty"`
}

func schedKey(q Query) string { return "tgsearch.sched." + q.Text() }

// nextTry is when a title that found nothing should be searched again.
// Day 0 is every 6 hours, days 1 to 4 once a day, days 4 to 12 every 2 days,
// and after that every 6 days. A digital release that is less than 3 days old
// stays on the 6 hour cadence. A record with no last try is due now.
func nextTry(rec schedRec, now time.Time, digitalOut bool) time.Time {
	now = now.UTC()
	if rec.Last == 0 {
		return now
	}
	last := time.Unix(rec.Last, 0).UTC()
	first := last
	if rec.First != 0 {
		first = time.Unix(rec.First, 0).UTC()
	}
	age := last.Sub(first)
	if age < 0 {
		age = 0
	}
	wait := cadence(age)
	if digitalOut && rec.Released > 0 {
		rel := time.Unix(rec.Released, 0).UTC()
		if !now.Before(rel) && now.Sub(rel) < 72*time.Hour {
			wait = 6 * time.Hour
		}
	}
	return last.Add(wait)
}

func cadence(age time.Duration) time.Duration {
	switch {
	case age < 24*time.Hour:
		return 6 * time.Hour
	case age < 4*24*time.Hour:
		return 24 * time.Hour
	case age < 12*24*time.Hour:
		return 48 * time.Hour
	default:
		return 6 * 24 * time.Hour
	}
}

func (s *Service) coolingDown(ctx context.Context, q Query) bool {
	if s.Limits.withDefaults().QueryCooldown < 0 || q.Text() == "" {
		return false
	}
	rec := s.loadSched(ctx, q)
	if rec.Last == 0 {
		return false
	}
	return s.now().UTC().Before(nextTry(rec, s.now(), q.DigitalOut))
}

func (s *Service) noteSearched(ctx context.Context, q Query) {
	if s.Limits.withDefaults().QueryCooldown < 0 || q.Text() == "" {
		return
	}
	rec := s.loadSched(ctx, q)
	now := s.now().UTC().Unix()
	if rec.First == 0 {
		rec.First = now
	}
	rec.Last = now
	rec.Tries++
	rec.Digital = q.DigitalOut
	if !q.Release.IsZero() {
		rec.Released = q.Release.UTC().Unix()
	}
	s.writeSched(ctx, q, rec)
}

func (s *Service) dueAt(ctx context.Context, q Query) time.Time {
	if s.Limits.withDefaults().QueryCooldown < 0 {
		return time.Time{}
	}
	rec := s.readSched(ctx, q)
	if rec.Last == 0 {
		return s.now().UTC()
	}
	return nextTry(rec, s.now(), q.DigitalOut)
}

func (s *Service) loadSched(ctx context.Context, q Query) schedRec {
	rec := s.readSched(ctx, q)
	changed := false
	if rec.First == 0 && rec.Last == 0 {
		if old := s.oldStamp(ctx, q); old > 0 {
			rec.First, rec.Last, rec.Tries = old, old, 1
			changed = true
		}
	}
	if !q.Release.IsZero() && rec.Released != q.Release.UTC().Unix() {
		rec.Released = q.Release.UTC().Unix()
		changed = true
	}
	// A new digital release starts the short cadence again.
	if q.DigitalOut && !rec.Digital {
		if rec.First != 0 {
			rec.First = s.now().UTC().Unix()
			rec.Last = 0
			rec.Tries = 0
		}
		rec.Digital = true
		changed = true
	}
	if changed {
		s.writeSched(ctx, q, rec)
	}
	return rec
}

func (s *Service) readSched(ctx context.Context, q Query) schedRec {
	key := schedKey(q)
	raw := ""
	if s.Store != nil {
		got, err := s.Store.StateValue(ctx, key)
		if err != nil {
			s.warn("read search schedule", err)
		} else {
			raw = got
		}
	} else if s.sched != nil {
		raw = s.sched[key]
	}
	var rec schedRec
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &rec)
	}
	return rec
}

func (s *Service) writeSched(ctx context.Context, q Query, rec schedRec) {
	raw, err := json.Marshal(rec)
	if err != nil {
		return
	}
	key := schedKey(q)
	if s.sched == nil {
		s.sched = map[string]string{}
	}
	s.sched[key] = string(raw)
	if s.Store != nil {
		s.warn("store search schedule", s.Store.SetStateValue(ctx, key, string(raw)))
	}
}

func (s *Service) oldStamp(ctx context.Context, q Query) int64 {
	key := "tgsearch.q." + q.Text()
	raw := ""
	if s.Store != nil {
		got, err := s.Store.StateValue(ctx, key)
		if err != nil {
			return 0
		}
		raw = got
	} else if s.sched != nil {
		raw = s.sched[key]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0
	}
	return n
}
