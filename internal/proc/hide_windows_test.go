//go:build windows

package proc

import (
	"context"
	"testing"
)

func TestNoWindowIsAskedFor(t *testing.T) {
	cmd := Command(context.Background(), "ffprobe")
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&createNoWindow == 0 || !cmd.SysProcAttr.HideWindow {
		t.Fatalf("CREATE_NO_WINDOW is not set: %+v", cmd.SysProcAttr)
	}
}
