package panel

import (
	"testing"
	"time"
)

func TestSamplerTurnsTotalsIntoSteps(t *testing.T) {
	cur := map[string]Total{"bot:1": {Work: 10, Bytes: 100}}
	s := &sampler{totals: func() map[string]Total { return cur }, last: map[string]Total{}, series: map[string][]Point{}}
	now := time.Unix(1000, 0)
	s.sample(now)
	cur["bot:1"] = Total{Work: 14, Bytes: 300}
	s.sample(now.Add(seriesStep))
	cur["bot:1"] = Total{Work: 2} // a restart resets the counters
	s.sample(now.Add(2 * seriesStep))

	got := s.view()[0].Points
	if len(got) != 3 || got[0].Work != 0 || got[1].Work != 4 || got[1].Bytes != 200 || got[2].Work != 0 {
		t.Fatalf("points = %+v", got)
	}
	s.sample(now.Add(seriesWindow + 2*seriesStep + time.Second))
	if n := len(s.view()[0].Points); n != 1 {
		t.Fatalf("old points kept: %d", n)
	}
}
