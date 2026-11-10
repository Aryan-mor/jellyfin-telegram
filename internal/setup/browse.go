package setup

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// maxBrowse is how many folders one listing returns. A folder with more is cut, since the person can
// still type the path.
const maxBrowse = 500

// Listing is the folders inside one folder, for the page's folder picker.
type Listing struct {
	// Path is the folder listed. Empty means the top: the drives on Windows, the home folder elsewhere.
	Path string `json:"path"`
	// Parent is the folder above, empty when this is the top.
	Parent string `json:"parent"`
	// Top is true for the list of drives.
	Top bool `json:"top,omitempty"`
	// Dirs holds full paths, sorted by name.
	Dirs []string `json:"dirs"`
	// Cut is true when more folders exist than were returned.
	Cut bool `json:"cut,omitempty"`
}

// Browse lists the folders inside raw. Files are never listed, and neither are hidden folders or the
// ones a system keeps for itself.
func Browse(raw string) (Listing, error) {
	if strings.TrimSpace(raw) == "" {
		return browseTop()
	}
	path, err := absFolder(raw)
	if err != nil {
		return Listing{}, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return Listing{}, err
	}
	out := Listing{Path: path, Parent: parentOf(path), Dirs: []string{}}
	var names []string
	for _, e := range entries {
		if !e.IsDir() || hiddenFolder(e.Name()) {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	if len(names) > maxBrowse {
		names, out.Cut = names[:maxBrowse], true
	}
	for _, n := range names {
		out.Dirs = append(out.Dirs, filepath.Join(path, n))
	}
	return out, nil
}

// parentOf is the folder above path, or "" at the top of a drive or of the tree.
func parentOf(path string) string {
	up := filepath.Dir(path)
	if up == path {
		return ""
	}
	return up
}

func hiddenFolder(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "$") || strings.EqualFold(name, "System Volume Information")
}

// handleBrowse lists folders for the picker. Only this machine, or a Tailscale address, may look
// at its disk.
func (w *Wizard) handleBrowse(rw http.ResponseWriter, r *http.Request) {
	if !TrustedClient(r) {
		writeError(rw, http.StatusForbidden, "folders can only be browsed from this computer. Type the path instead")
		return
	}
	list, err := Browse(r.URL.Query().Get("path"))
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			writeError(rw, http.StatusBadRequest, "no permission to open that folder")
			return
		}
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(rw, http.StatusOK, list)
}
