package home

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/localmedia"
)

var keepExt = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".webm": true,
	".ts": true, ".m2ts": true, ".wmv": true, ".flv": true, ".mpg": true, ".mpeg": true,
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
	".heic": true, ".heif": true, ".tif": true, ".tiff": true, ".bmp": true,
}

func (s *Service) scan(ctx context.Context) {
	s.mu.Lock()
	if s.scanning {
		s.mu.Unlock()
		return
	}
	s.scanning = true
	dirs := append([]string(nil), s.dirs...)
	s.mu.Unlock()

	var files []File
	var folders []localmedia.Folder
	seen := map[int64]bool{}
	for _, root := range dirs {
		folder := localmedia.Folder{Path: root}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			folder.Error = "this folder does not exist"
			folders = append(folders, folder)
			continue
		}
		folder.Exists = true
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if p != root && (strings.HasPrefix(d.Name(), ".") || excluded(p, s.Exclude)) {
					return filepath.SkipDir
				}
				return nil
			}
			if !keepExt[strings.ToLower(filepath.Ext(p))] {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			id := localmedia.ID(p)
			if seen[id] {
				return nil
			}
			seen[id] = true
			kind := "video"
			if strings.HasPrefix(strings.ToLower(filepath.Ext(p)), ".jp") || imageExt(filepath.Ext(p)) {
				kind = "photo"
			}
			files = append(files, File{ID: id, Path: p, Name: d.Name(), Size: info.Size(), Mod: info.ModTime(), Kind: kind})
			folder.Files++
			folder.Bytes += info.Size()
			return nil
		})
		folders = append(folders, folder)
	}
	s.mu.Lock()
	s.scanning = false
	if ctx.Err() == nil {
		s.files, s.folders, s.scanned = files, folders, time.Now()
	}
	s.mu.Unlock()
}

func imageExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".png", ".gif", ".webp", ".heic", ".heif", ".tif", ".tiff", ".bmp":
		return true
	}
	return false
}

func excluded(p string, folders []string) bool {
	for _, f := range folders {
		if f != "" && localmedia.Within(f, p) {
			return true
		}
	}
	return false
}
