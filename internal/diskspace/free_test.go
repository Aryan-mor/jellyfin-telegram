package diskspace

import "testing"

func TestFree(t *testing.T) {
	n, err := Free(t.TempDir())
	if err != nil || n == 0 {
		t.Fatalf("free = %d, %v", n, err)
	}
}
