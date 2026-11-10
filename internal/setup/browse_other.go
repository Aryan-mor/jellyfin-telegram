//go:build !windows

package setup

import "os"

// browseTop starts in the home folder, or at the root when there is none.
func browseTop() (Listing, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return Browse("/")
	}
	return Browse(home)
}
