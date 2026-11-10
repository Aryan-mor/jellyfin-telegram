// Package proc starts the programs this one depends on, such as ffmpeg and ffprobe, without a window.
//
// After setup this program lets go of its console, so closing a terminal cannot stop the server. A
// console program started from a process with no console gets a new window of its own, and indexing
// a channel starts thousands of them, each one opening and closing in front of whatever the person is
// doing. Every program started here asks Windows not to make that window.
package proc

import (
	"context"
	"os/exec"
)

// Command is exec.CommandContext for a program that must not open a window.
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	Hide(cmd)
	return cmd
}
