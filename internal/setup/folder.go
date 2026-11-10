package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Aryan-mor/jellyfin-telegram/internal/diskspace"
)

// LowSpace is the free space below which the page warns. Jellyfin, Seerr and the stream cache
// together take several GB before any video is played.
const LowSpace = 10 << 30

// Folder is what a typed folder means.
type Folder struct {
	// Path is the absolute, cleaned folder.
	Path string `json:"path"`
	// Free is the space left on the drive that holds it, in bytes.
	Free uint64 `json:"free"`
	// Low is true when Free is under LowSpace.
	Low bool `json:"low"`
	// Exists is false when the folder will be created.
	Exists bool `json:"exists"`
}

// CheckFolder settles whether the program can keep its files in raw. It creates nothing: the
// folder that is missing is made when the settings are saved, so typing a path never leaves one behind.
func CheckFolder(raw string) (Folder, error) {
	path, err := absFolder(raw)
	if err != nil {
		return Folder{}, err
	}
	// The nearest folder that exists decides whether writing is possible and how much room there is.
	probe, exists := path, true
	for {
		info, err := os.Stat(probe)
		if err == nil {
			if !info.IsDir() {
				return Folder{}, fmt.Errorf("%s is a file, pick a folder", probe)
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return Folder{}, err
		}
		exists = false
		parent := filepath.Dir(probe)
		if parent == probe {
			return Folder{}, fmt.Errorf("%s does not exist", path)
		}
		probe = parent
	}
	if err := writable(probe); err != nil {
		return Folder{}, fmt.Errorf("cannot write in %s: %w", probe, err)
	}
	free, err := diskspace.Free(probe)
	if err != nil {
		return Folder{}, err
	}
	return Folder{Path: path, Free: free, Low: free < LowSpace, Exists: exists}, nil
}

// PrepareFolder makes the folder, after the same checks.
func PrepareFolder(raw string) (Folder, error) {
	f, err := CheckFolder(raw)
	if err != nil {
		return Folder{}, err
	}
	if err := os.MkdirAll(f.Path, 0o700); err != nil {
		return Folder{}, err
	}
	if err := writable(f.Path); err != nil {
		return Folder{}, fmt.Errorf("cannot write in %s: %w", f.Path, err)
	}
	f.Exists = true
	return f, nil
}

func absFolder(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("type a folder")
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") || strings.HasPrefix(raw, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		raw = filepath.Join(home, raw[1:])
	}
	if !filepath.IsAbs(raw) {
		return "", errors.New(`type the whole path, for example D:\JellyfinTelegram or /srv/jellyfin-telegram`)
	}
	return filepath.Clean(raw), nil
}

func writable(dir string) error {
	f, err := os.CreateTemp(dir, ".write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// Layout is where each part goes below the one folder the user picked. Keeping the names here means
// a later version can split them without the setup page changing.
type Layout struct {
	Data    string
	Library string
}

// LayoutIn returns the folders below root.
func LayoutIn(root string) Layout {
	return Layout{Data: filepath.Join(root, "data"), Library: filepath.Join(root, "library")}
}

// Env is the settings that point the program at this layout.
func (l Layout) Env() map[string]string {
	return map[string]string{"DATA_DIR": l.Data, "LIBRARY_DIR": l.Library}
}
