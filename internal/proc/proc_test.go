package proc

import (
	"context"
	"testing"
)

func TestCommandKeepsTheProgramAndItsArguments(t *testing.T) {
	cmd := Command(context.Background(), "ffprobe", "-v", "error")
	if cmd.Args[0] != "ffprobe" || len(cmd.Args) != 3 {
		t.Fatalf("args = %v", cmd.Args)
	}
}
