// Package library writes the .strm tree that Jellyfin scans.
package library

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Library writes files below a root directory.
type Library struct {
	root    string
	baseURL string
	token   string
	channel int64
}

// New returns a Library. baseURL and token go into every .strm file.
func New(root, baseURL, token string, channel int64) *Library {
	return &Library{root: root, baseURL: strings.TrimRight(baseURL, "/"), token: token, channel: channel}
}

// Prepare creates the top-level folders that Jellyfin libraries point at.
func (l *Library) Prepare() error {
	for _, d := range []string{"Movies", "Shows", "Home"} {
		if err := os.MkdirAll(filepath.Join(l.root, d), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// StreamURL returns the address stored in a .strm file.
func (l *Library) StreamURL(messageID int) string {
	return fmt.Sprintf("%s/s/%s/%d/%d", l.baseURL, l.token, l.channel, messageID)
}

// WriteStrm creates or updates a .strm file and reports whether it changed.
//
// An existing file keeps its modification time, so a new gateway address or token stays invisible
// to Jellyfin. When that time changes, Jellyfin deletes the extracted subtitles of the item. It then
// saves the old time from its folder cache, so it sees a change and deletes them again on every
// PlaybackInfo until it restarts.
func (l *Library) WriteStrm(rel string, messageID int) (bool, error) {
	full, err := l.resolve(rel)
	if err != nil {
		return false, err
	}
	old, statErr := os.Stat(full)
	changed, err := l.writeIfChanged(rel, []byte(l.StreamURL(messageID)+"\n"))
	if err != nil || !changed || statErr != nil {
		return changed, err
	}
	return true, os.Chtimes(full, old.ModTime(), old.ModTime())
}

// WriteUnmatchedSidecars stores local metadata for a movie that cannot be matched online.
// The poster is written only when thumb is non-empty.
func (l *Library) WriteUnmatchedSidecars(rel, title, plot string, thumb []byte) error {
	dir := path.Dir(rel)
	if _, err := l.writeIfChanged(path.Join(dir, "movie.nfo"), movieNFO(title, plot)); err != nil {
		return err
	}
	if len(thumb) == 0 {
		return nil
	}
	_, err := l.writeIfChanged(path.Join(dir, "poster.jpg"), thumb)
	return err
}

// HasTMDBSidecar reports whether the movie folder already names its TMDB id in movie.nfo.
func (l *Library) HasTMDBSidecar(rel string) bool {
	full, err := l.resolve(path.Join(path.Dir(rel), "movie.nfo"))
	if err != nil {
		return false
	}
	raw, err := os.ReadFile(full)
	return err == nil && bytes.Contains(raw, []byte("<tmdbid>"))
}

// WriteTMDBSidecar tells Jellyfin which TMDB entry a movie is, without a lock, a title or a plot, so
// the online metadata and poster are used. It replaces the local metadata and poster that
// WriteUnmatchedSidecars wrote, because those would win over the online ones.
func (l *Library) WriteTMDBSidecar(rel string, tmdbID int) error {
	nfo := fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<movie>\n  <uniqueid type=\"tmdb\" default=\"true\">%d</uniqueid>\n  <tmdbid>%d</tmdbid>\n</movie>\n", tmdbID, tmdbID)
	if _, err := l.writeIfChanged(path.Join(path.Dir(rel), "movie.nfo"), []byte(nfo)); err != nil {
		return err
	}
	full, err := l.resolve(path.Join(path.Dir(rel), "poster.jpg"))
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// RemoveUnmatchedSidecars deletes the local metadata that WriteUnmatchedSidecars wrote, so a title
// that became matchable gets its online metadata and poster. A missing file is not an error.
func (l *Library) RemoveUnmatchedSidecars(rel string) error {
	dir := path.Dir(rel)
	for _, name := range []string{"movie.nfo", "poster.jpg"} {
		full, err := l.resolve(path.Join(dir, name))
		if err != nil {
			return err
		}
		if err := os.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// WriteBytes stores a small file, such as a personal photo, under the library root.
func (l *Library) WriteBytes(rel string, data []byte) error {
	_, err := l.writeIfChanged(rel, data)
	return err
}

// Remove deletes a file, then every parent folder holding no other .strm file.
// The Movies, Shows and Home folders themselves are kept.
func (l *Library) Remove(rel string) error {
	full, err := l.resolve(rel)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for dir := path.Dir(rel); strings.Contains(dir, "/"); dir = path.Dir(dir) {
		fullDir, err := l.resolve(dir)
		if err != nil {
			return err
		}
		has, err := containsStrm(fullDir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if has {
			return nil
		}
		if err := os.RemoveAll(fullDir); err != nil {
			return err
		}
	}
	return nil
}

func (l *Library) resolve(rel string) (string, error) {
	local := filepath.FromSlash(rel)
	if !filepath.IsLocal(local) {
		return "", fmt.Errorf("path escapes the library: %q", rel)
	}
	return filepath.Join(l.root, local), nil
}

func (l *Library) writeIfChanged(rel string, data []byte) (bool, error) {
	full, err := l.resolve(rel)
	if err != nil {
		return false, err
	}
	if old, err := os.ReadFile(full); err == nil && bytes.Equal(old, data) {
		return false, nil
	}
	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return false, err
	}
	return true, os.Rename(tmp.Name(), full)
}

func containsStrm(dir string) (bool, error) {
	found := false
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".strm") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found, err
}

func movieNFO(title, plot string) []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString("<movie>\n  <title>")
	_ = xml.EscapeText(&b, []byte(title))
	b.WriteString("</title>\n  <plot>")
	_ = xml.EscapeText(&b, []byte(plot))
	b.WriteString("</plot>\n  <lockdata>true</lockdata>\n</movie>\n")
	return b.Bytes()
}
