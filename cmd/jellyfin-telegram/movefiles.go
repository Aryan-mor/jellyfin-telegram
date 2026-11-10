package main

import (
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// setupFiles are what the setup page leaves in the data folder before the person has chosen where
// everything lives: the Telegram sign-in and the bot sessions.
var setupFiles = []string{"users", "sessions"}

// moveSetupFiles carries them to the chosen data folder. Anything already there is left alone,
// because it is newer than a copy from before the choice.
func moveSetupFiles(from, to string, log *slog.Logger) error {
	from, err := filepath.Abs(from)
	if err != nil {
		return err
	}
	to, err = filepath.Abs(to)
	if err != nil {
		return err
	}
	if strings.EqualFold(from, to) {
		return nil
	}
	for _, name := range setupFiles {
		src, dst := filepath.Join(from, name), filepath.Join(to, name)
		if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		if err := os.MkdirAll(to, 0o700); err != nil {
			return err
		}
		// A rename is instant on one drive. Across drives it fails and the files are copied.
		if err := os.Rename(src, dst); err == nil {
			continue
		}
		if err := copyTree(src, dst); err != nil {
			return err
		}
		if err := os.RemoveAll(src); err != nil && log != nil {
			log.Warn("could not remove the old copy", "path", src, "err", err)
		}
	}
	return nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}
