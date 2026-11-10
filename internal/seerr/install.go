package seerr

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Aryan-mor/jellyfin-telegram/internal/managed"
)

// Bundle is an installed Seerr: the Node that runs it and the folder it runs in.
type Bundle struct {
	Node string
	App  string
}

func bundleAt(dir string) Bundle {
	node := "node"
	if runtime.GOOS == "windows" {
		node = "node.exe"
	}
	return Bundle{
		Node: filepath.Join(dir, "seerr", node),
		App:  filepath.Join(dir, "seerr", "app"),
	}
}

func (b Bundle) entry() string { return filepath.Join(b.App, "dist", "index.js") }

// Install makes sure this system's bundle is present below root and returns where it is. Nothing
// else is needed on the machine: no Docker and no Node.
func Install(ctx context.Context, root string, client *http.Client, progress managed.Progress) (Bundle, error) {
	art, err := bundleFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Bundle{}, err
	}
	return install(ctx, root, art, client, progress)
}

// install keeps a finished install marked, so an interrupted download starts over instead of
// running a partial copy.
func install(ctx context.Context, root string, art managed.Artifact, client *http.Client, progress managed.Progress) (Bundle, error) {
	versions := filepath.Join(root, "bundle")
	dir := filepath.Join(versions, Version)
	b := bundleAt(dir)
	marker := filepath.Join(dir, ".installed")
	// The marker holds the hash of the bundle that was unpacked. A bundle rebuilt for the same Seerr
	// version has another hash, and replaces the old one instead of being skipped.
	want := Version + " " + art.SHA256 + "\n"
	if have, err := os.ReadFile(marker); err == nil && string(have) == want {
		return b, nil
	}
	if art.SHA256 == "" {
		return Bundle{}, errors.New("this Seerr build has not been published yet, so it cannot be downloaded")
	}
	if err := os.RemoveAll(dir); err != nil {
		return Bundle{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Bundle{}, err
	}
	if err := managed.Download(ctx, art, "Seerr", dir, client, progress); err != nil {
		os.RemoveAll(dir)
		return Bundle{}, err
	}
	for _, p := range []string{b.Node, b.entry()} {
		if _, err := os.Stat(p); err != nil {
			os.RemoveAll(dir)
			return Bundle{}, fmt.Errorf("the Seerr download holds no %s", p)
		}
	}
	if err := os.WriteFile(marker, []byte(want), 0o644); err != nil {
		return Bundle{}, err
	}
	// An older version is no use once the new one runs, and Seerr is large.
	if entries, err := os.ReadDir(versions); err == nil {
		for _, e := range entries {
			if e.Name() != Version {
				_ = os.RemoveAll(filepath.Join(versions, e.Name()))
			}
		}
	}
	return b, nil
}
