// Package localmedia finds the videos already on this computer, sorts them into films and series,
// and lets a person move them into the channel from the panel. Nothing is uploaded by itself.
package localmedia

import (
	"context"
	"hash/fnv"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/naming"
)

// Kinds a file is sorted into.
const (
	KindMovie   = "movie"
	KindEpisode = "episode"
	KindOther   = "other"
)

var videoExt = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".webm": true,
	".ts": true, ".m2ts": true, ".wmv": true, ".flv": true, ".mpg": true, ".mpeg": true,
}

// minTransportStream keeps the short segments of a transcode out of the list. A real recording in
// the .ts or .m2ts container is far larger than one segment.
const minTransportStream = 50 << 20

// skipDirs are folders that hold no library video: Jellyfin's own work folders, and the extras it
// files beside a film, which are not worth a channel post of their own.
var skipDirs = map[string]bool{
	"cache": true, "transcodes": true, "metadata": true, "trickplay": true, "$recycle.bin": true,
	"system volume information": true, "extras": true, "extra": true, "trailers": true,
	"featurettes": true, "sample": true, "samples": true, "behind the scenes": true,
	"deleted scenes": true, "interviews": true, "scenes": true, "shorts": true,
}

// File is one video found on disk.
type File struct {
	ID      int64     `json:"id"`
	Path    string    `json:"-"`
	Root    string    `json:"-"`
	Rel     string    `json:"rel"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"-"`

	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Year   int    `json:"year,omitempty"`
	Season int    `json:"season,omitempty"`
	Number int    `json:"number,omitempty"`
	TMDBID int    `json:"-"`
	Label  string `json:"-"`

	// UploadName and Caption are what the channel post will carry, so the indexer files it correctly.
	UploadName string `json:"upload_name"`
	Caption    string `json:"-"`
}

// Folder is one configured media folder and what the last scan found in it.
type Folder struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Files  int    `json:"files"`
	Bytes  int64  `json:"bytes"`
	Error  string `json:"error,omitempty"`
}

// ID is a number made from a file's path. It is stable between runs and small enough for a
// browser to hold without rounding.
func ID(p string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(filepath.ToSlash(filepath.Clean(p))))
	id := int64(h.Sum64() & (1<<52 - 1))
	if id == 0 {
		id = 1
	}
	return id
}

// Scan walks every root, skipping the excluded folders, and returns the videos it finds.
// A root that cannot be read is reported in its Folder and does not stop the others.
func Scan(ctx context.Context, roots, exclude []string) ([]File, []Folder) {
	var files []File
	folders := make([]Folder, 0, len(roots))
	seen := map[int64]bool{}
	for _, root := range roots {
		folder := Folder{Path: root}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			folder.Error = "this folder does not exist"
			if err == nil {
				folder.Error = "this is not a folder"
			}
			folders = append(folders, folder)
			continue
		}
		folder.Exists = true
		err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				// One unreadable folder must not hide the rest.
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if p != root && (skipDir(d.Name()) || excluded(p, exclude)) {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || !videoExt[strings.ToLower(filepath.Ext(p))] || skipFile(d.Name()) {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(p))
			if (ext == ".ts" || ext == ".m2ts") && info.Size() < minTransportStream {
				return nil
			}
			id := ID(p)
			if seen[id] {
				return nil
			}
			seen[id] = true
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return nil
			}
			f := classify(root, p, filepath.ToSlash(rel))
			f.Size, f.ModTime = info.Size(), info.ModTime()
			files = append(files, f)
			folder.Files++
			folder.Bytes += f.Size
			return nil
		})
		if err != nil && ctx.Err() == nil {
			folder.Error = err.Error()
		}
		folders = append(folders, folder)
	}
	sort.SliceStable(files, func(i, j int) bool { return order(files[i], files[j]) })
	return files, folders
}

func classify(root, full, rel string) File {
	f := File{ID: ID(full), Path: full, Root: root, Rel: rel, Name: path.Base(rel)}
	p := naming.ParsePath(rel)
	f.Title, f.Year, f.TMDBID, f.Label = p.Title, p.Year, p.TMDBID, p.Label
	switch {
	case p.Title == "":
		f.Kind = KindOther
		f.Title = ""
		f.UploadName = f.Name
		return f
	case p.Episode:
		f.Kind, f.Season, f.Number = KindEpisode, p.Season, p.Number
	default:
		f.Kind = KindMovie
	}
	f.UploadName, f.Caption = naming.UploadName(p, filepath.Ext(full))
	return f
}

func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || skipDirs[strings.ToLower(name)]
}

// skipFile leaves out trailers and samples that sit among the films.
func skipFile(name string) bool {
	stem := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
	return stem == "sample" || strings.HasSuffix(stem, "-trailer") || strings.HasSuffix(stem, ".sample") || strings.HasSuffix(stem, "-sample")
}

// excluded reports whether p is one of the folders, or inside one.
func excluded(p string, folders []string) bool {
	for _, f := range folders {
		if f != "" && Within(f, p) {
			return true
		}
	}
	return false
}

// Within reports whether target is dir itself or lies below it.
func Within(dir, target string) bool {
	a, err1 := filepath.Abs(dir)
	b, err2 := filepath.Abs(target)
	if err1 != nil || err2 != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		a, b = strings.ToLower(a), strings.ToLower(b)
	}
	rel, err := filepath.Rel(a, b)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func order(a, b File) bool {
	if a.Kind != b.Kind {
		return rank(a.Kind) < rank(b.Kind)
	}
	if ta, tb := strings.ToLower(a.Title), strings.ToLower(b.Title); ta != tb {
		return ta < tb
	}
	if a.Year != b.Year {
		return a.Year < b.Year
	}
	if a.Season != b.Season {
		return a.Season < b.Season
	}
	if a.Number != b.Number {
		return a.Number < b.Number
	}
	return strings.ToLower(a.Rel) < strings.ToLower(b.Rel)
}

func rank(kind string) int {
	switch kind {
	case KindMovie:
		return 0
	case KindEpisode:
		return 1
	}
	return 2
}
