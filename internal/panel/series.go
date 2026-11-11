package panel

import (
	"net/http"
	"sort"
	"sync"
	"time"
)

const (
	seriesStep   = 5 * time.Second
	seriesWindow = 10 * time.Minute
)

// Total is what one bot or account has done since the service started. The numbers only go up.
type Total struct {
	Work  int64 // blocks read, channel calls and refusals for a bot; logged events for an account
	Bytes int64 // bytes a bot read
}

// Point is the work done in one step ending at T.
type Point struct {
	T     int64 `json:"t"`
	Work  int64 `json:"work"`
	Bytes int64 `json:"bytes,omitempty"`
}

// SeriesView is one bot or account over the window.
type SeriesView struct {
	Key    string  `json:"key"` // "bot:<id>" or "account:<id>"
	Points []Point `json:"points"`
}

// sampler turns the running totals into one point per step. The server keeps them, so the chart
// has its history after a reload and does not depend on how often a page asks.
type sampler struct {
	totals func() map[string]Total

	mu     sync.Mutex
	last   map[string]Total
	series map[string][]Point
}

func newSampler(totals func() map[string]Total) *sampler {
	s := &sampler{totals: totals, last: map[string]Total{}, series: map[string][]Point{}}
	go func() {
		for range time.Tick(seriesStep) {
			s.sample(time.Now())
		}
	}()
	return s
}

func (s *sampler) sample(now time.Time) {
	cur := s.totals()
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := now.Add(-seriesWindow).Unix()
	for key, tot := range cur {
		prev, seen := s.last[key]
		p := Point{T: now.Unix()}
		if seen {
			// A restart resets the counters, so a drop is not negative work.
			p.Work = max(0, tot.Work-prev.Work)
			p.Bytes = max(0, tot.Bytes-prev.Bytes)
		}
		s.last[key] = tot
		list := s.series[key]
		i := 0
		for i < len(list) && list[i].T < cutoff {
			i++
		}
		s.series[key] = append(list[i:], p)
	}
}

func (s *sampler) view() []SeriesView {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SeriesView, 0, len(s.series))
	for key, pts := range s.series {
		out = append(out, SeriesView{Key: key, Points: append([]Point(nil), pts...)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func (p *panel) handleSeries(w http.ResponseWriter, _ *http.Request) {
	if p.series == nil {
		writeJSON(w, http.StatusOK, map[string]any{"step": int(seriesStep.Seconds()), "series": []SeriesView{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"step": int(seriesStep.Seconds()), "window": int(seriesWindow.Seconds()), "now": time.Now().Unix(), "series": p.series.view()})
}
