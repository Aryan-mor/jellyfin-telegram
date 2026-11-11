package managed

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
)

// The helpers below let another program this one runs as a child (Seerr) install and stop its
// files the same way Jellyfin does, instead of copying the code.

// Download fetches one archive, refuses it unless its hash is the pinned one, and unpacks it into
// the folder.
func Download(ctx context.Context, a Artifact, name, into string, client *http.Client, progress Progress) error {
	return fetch(ctx, a, name, into, client, progress)
}

// StopStale ends a copy of the program at exe that an earlier crash left running.
func StopStale(pidFile, exe string, log *slog.Logger) { stopStale(pidFile, exe, log) }

// StopStaleIn ends a leftover process whose executable sits inside dir.
func StopStaleIn(pidFile, dir string, log *slog.Logger) { stopStaleIn(pidFile, dir, log) }

// StopGently asks a child to end in the way its system allows.
func StopGently(p *os.Process) error { return stopGently(p) }

// HideWindow keeps a console program from opening a terminal window.
func HideWindow(cmd *exec.Cmd) { hideWindow(cmd) }
