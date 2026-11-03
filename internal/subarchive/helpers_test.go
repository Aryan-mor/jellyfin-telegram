package subarchive

import (
	"archive/zip"
	"bytes"
	"path/filepath"
)

func buildRaw(files map[string][]byte) ([]byte, error) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, data := range files {
		if err := writeEntry(w, name, data); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func toSlash(p string) string { return filepath.ToSlash(p) }
