//go:build !windows

package proc

import "os/exec"

// Hide does nothing here: only Windows opens a window for a console program.
func Hide(cmd *exec.Cmd) {}
