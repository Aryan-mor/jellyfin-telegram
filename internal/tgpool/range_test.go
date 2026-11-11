package tgpool

import "testing"

func TestPreciseSpans(t *testing.T) {
	got := preciseSpans(100, 50, 10_000)
	if len(got) != 1 || got[0].Offset != 0 || got[0].Limit != 1024 {
		t.Fatalf("%+v", got)
	}
	got = preciseSpans(1_048_500, 200, 10<<20)
	if len(got) != 2 || got[0].Offset != 1_047_552 || got[0].Limit != 1024 || got[1].Offset != 1_048_576 || got[1].Limit != 1024 {
		t.Fatalf("%+v", got)
	}
	for _, sp := range got {
		if sp.Offset%1024 != 0 || sp.Limit%1024 != 0 {
			t.Fatalf("unaligned %+v", sp)
		}
		if sp.Offset/1_048_576 != (sp.Offset+int64(sp.Limit)-1)/1_048_576 {
			t.Fatalf("crosses a block %+v", sp)
		}
	}
}
