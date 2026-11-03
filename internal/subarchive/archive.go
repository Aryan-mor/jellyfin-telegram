// Package subarchive keeps the embedded subtitles of every video in the channel, so each file is
// read in full at most once.
//
// When a client asks for an embedded subtitle as a separate document, Jellyfin reads the whole
// video from Telegram to extract it. This package does that work once in the background, stores
// the result next to the video as a channel message, and places it in Jellyfin's subtitle cache.
// Jellyfin then finds nothing left to extract. If the cache or this service's data is ever lost,
// the subtitles come back from the channel without touching the video.
package subarchive

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

const (
	namePrefix   = "jftg-subs-"
	manifestName = "manifest.json"
	// maxEntryBytes limits one file read back from an archive. Image subtitles can be large.
	maxEntryBytes = 512 << 20
)

var reArchiveName = regexp.MustCompile(`^` + namePrefix + `(\d+)-(-?\d+)\.zip$`)

// Name returns the file name of the archive for one version of a video.
func Name(messageID int, docID int64) string {
	return fmt.Sprintf("%s%d-%d.zip", namePrefix, messageID, docID)
}

// ParseName reads a file name made by Name.
func ParseName(name string) (messageID int, docID int64, ok bool) {
	m := reArchiveName.FindStringSubmatch(name)
	if m == nil {
		return 0, 0, false
	}
	messageID, err1 := strconv.Atoi(m[1])
	docID, err2 := strconv.ParseInt(m[2], 10, 64)
	return messageID, docID, err1 == nil && err2 == nil
}

// IsArchiveName reports whether a channel file is one of this service's archives.
func IsArchiveName(name string) bool { return strings.HasPrefix(name, namePrefix) }

// Entry is one extracted track. Position is its place in the video file, which stays fixed. The
// index Jellyfin shows for the same track moves up when external subtitles are added.
type Entry struct {
	Position int    `json:"position"`
	Ext      string `json:"ext"`
	Language string `json:"language,omitempty"`
	Data     []byte `json:"-"`
}

func (e Entry) fileName() string { return strconv.Itoa(e.Position) + "." + e.Ext }

// Build makes the archive for a set of tracks.
func Build(entries []Entry) ([]byte, error) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	manifest, err := json.Marshal(entries)
	if err != nil {
		return nil, err
	}
	if err := writeEntry(w, manifestName, manifest); err != nil {
		return nil, err
	}
	for _, e := range entries {
		if err := writeEntry(w, e.fileName(), e.Data); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeEntry(w *zip.Writer, name string, data []byte) error {
	f, err := w.Create(name)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	return err
}

// Read reverses Build. It fails if any listed track is missing, because a partial set would make
// Jellyfin read the whole video for the rest.
func Read(data []byte) ([]Entry, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("subtitle archive: %w", err)
	}
	files := make(map[string][]byte, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("subtitle archive: %w", err)
		}
		content, err := io.ReadAll(io.LimitReader(rc, maxEntryBytes))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("subtitle archive: %w", err)
		}
		files[f.Name] = content
	}
	raw, ok := files[manifestName]
	if !ok {
		return nil, errors.New("subtitle archive: no manifest")
	}
	var entries []Entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("subtitle archive: %w", err)
	}
	for i := range entries {
		content, ok := files[entries[i].fileName()]
		if !ok {
			return nil, fmt.Errorf("subtitle archive: missing track %d", entries[i].Position)
		}
		entries[i].Data = content
	}
	return entries, nil
}
