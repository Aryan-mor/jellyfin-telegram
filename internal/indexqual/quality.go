// Package indexqual is the picture sizes the library indexer keeps.
// An empty list means every size, which is the default.
package indexqual

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/joho/godotenv"
)

// Key is the settings file entry. A comma separated list of ids. Empty means all.
const Key = "INDEX_QUALITIES"

// order is the ids a person can pick, and the order they are stored in.
var order = []string{"480", "720", "1080", "2k", "4k"}

var known = map[string]bool{"480": true, "720": true, "1080": true, "2k": true, "4k": true}

// reSize finds a picture size in a file name or caption. 2K is 1440p, 4K is 2160p.
var reSize = regexp.MustCompile(`(?i)\b(2160p|1440p|1080p|720p|480p|4k|2k)\b`)

// Set is the sizes to index. Empty means all of them, including a file with no size in its name.
type Set struct {
	ids []string
}

// All reports that nothing is filtered.
func (s Set) All() bool { return len(s.ids) == 0 }

// IDs returns the picked sizes. Nil means all.
func (s Set) IDs() []string {
	if s.All() {
		return nil
	}
	return append([]string{}, s.ids...)
}

// Key is a stable name for this choice, so a change can be told from the last scan.
func (s Set) Key() string {
	if s.All() {
		return "all"
	}
	return strings.Join(s.ids, ",")
}

// Env is the settings file value. Empty means all.
func (s Set) Env() string {
	if s.All() {
		return ""
	}
	return strings.Join(s.ids, ",")
}

// Load reads the list from the settings file. A key in the file wins over the environment.
// A missing file or a bad value means all.
func Load(envPath string) Set {
	m, err := godotenv.Read(envPath)
	raw := ""
	if err == nil {
		if v, ok := m[Key]; ok {
			raw = v
		} else {
			raw = os.Getenv(Key)
		}
	} else {
		raw = os.Getenv(Key)
	}
	s, err := Parse(raw)
	if err != nil {
		return Set{}
	}
	return s
}

// Parse reads a settings value. "all" and an empty string keep every size.
func Parse(raw string) (Set, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "all") {
		return Set{}, nil
	}
	return Clean(strings.Split(raw, ","))
}

// Clean checks a list submitted by a person. All five, or none, means every size.
func Clean(in []string) (Set, error) {
	seen := map[string]bool{}
	for _, v := range in {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" || v == "all" {
			continue
		}
		if !known[v] {
			return Set{}, fmt.Errorf("unknown quality %q", v)
		}
		seen[v] = true
	}
	if len(seen) == 0 || len(seen) == len(order) {
		return Set{}, nil
	}
	ids := make([]string, 0, len(seen))
	for _, id := range order {
		if seen[id] {
			ids = append(ids, id)
		}
	}
	return Set{ids: ids}, nil
}

// Bucket is the size id of a file, or "" when the name and caption do not say.
func Bucket(fileName, caption string) string {
	switch strings.ToLower(reSize.FindString(fileName + "\n" + caption)) {
	case "480p":
		return "480"
	case "720p":
		return "720"
	case "1080p":
		return "1080"
	case "1440p", "2k":
		return "2k"
	case "2160p", "4k":
		return "4k"
	default:
		return ""
	}
}

// Keeps reports that subtitle work and mirror copies may use this file.
// Music is always kept, because it has no picture size. With no list, every file is kept.
func (s Set) Keeps(fileName, caption, mime string) bool {
	return s.KeepsSize(fileName, caption, mime, 0, 0)
}

// KeepsSize is Keeps, using the real picture size when the name does not say.
func (s Set) KeepsSize(fileName, caption, mime string, width, height int) bool {
	if s.All() || musicFile(mime, fileName) {
		return true
	}
	return s.AllowsSize(fileName, caption, width, height)
}

// LoadKeeps reads the settings file and applies Keeps. A missing path keeps every file.
func LoadKeeps(envPath, fileName, caption, mime string) bool {
	return LoadKeepsSize(envPath, fileName, caption, mime, 0, 0)
}

// LoadKeepsSize is LoadKeeps with a known picture size.
func LoadKeepsSize(envPath, fileName, caption, mime string, width, height int) bool {
	if envPath == "" {
		return true
	}
	return Load(envPath).KeepsSize(fileName, caption, mime, width, height)
}

func musicFile(mime, name string) bool {
	if strings.HasPrefix(strings.ToLower(mime), "audio/") {
		return true
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".mp3", ".flac", ".m4a", ".aac", ".ogg", ".opus", ".wav", ".wma":
		return true
	default:
		return false
	}
}

// Allows reports that this file may be indexed. With no list, every file may.
// A file with no size in its name and no known picture size is left out once a list is set.
func (s Set) Allows(fileName, caption string) bool {
	return s.AllowsSize(fileName, caption, 0, 0)
}

// AllowsSize is Allows, then the real picture size when the name and caption do not say.
func (s Set) AllowsSize(fileName, caption string, width, height int) bool {
	if s.All() {
		return true
	}
	b := Bucket(fileName, caption)
	if b == "" {
		b = BucketOf(width, height)
	}
	if b == "" {
		return false
	}
	for _, id := range s.ids {
		if id == b {
			return true
		}
	}
	return false
}

// Forwards reports that a source channel may copy this file into the library.
// The name wins. With no size in the name, the picture size is used.
// A file with neither is still copied, so the library can measure it.
func (s Set) Forwards(fileName, caption string, width, height int) bool {
	if s.All() {
		return true
	}
	b := Bucket(fileName, caption)
	if b == "" {
		b = BucketOf(width, height)
	}
	if b == "" {
		return true
	}
	for _, id := range s.ids {
		if id == b {
			return true
		}
	}
	return false
}

// BucketOf maps a real picture to a size id. The name still wins when it has one.
// A wide short frame, such as 1920x800, counts as 1080. Zero stays unknown.
func BucketOf(width, height int) string {
	if width <= 0 && height <= 0 {
		return ""
	}
	if width <= 0 {
		return bucketHeight(height)
	}
	if height <= 0 {
		return bucketWidth(width)
	}
	long, short := width, height
	if height > width {
		long, short = height, width
	}
	// 2:1 and wider is a scope frame: the long side is the width of the size.
	if long*10 >= short*20 {
		return bucketWidth(long)
	}
	return bucketHeight(short)
}

func bucketHeight(v int) string {
	switch {
	case v >= 1800:
		return "4k"
	case v >= 1300:
		return "2k"
	case v >= 900:
		return "1080"
	case v >= 600:
		return "720"
	case v >= 400:
		return "480"
	default:
		return ""
	}
}

func bucketWidth(v int) string {
	switch {
	case v >= 3000:
		return "4k"
	case v >= 2200:
		return "2k"
	case v >= 1600:
		return "1080"
	case v >= 1100:
		return "720"
	case v >= 640:
		return "480"
	default:
		return ""
	}
}
