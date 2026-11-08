// Package webui serves the built panel and setup pages.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
)

//go:embed dist
var dist embed.FS

// Assets serves /ui/assets/ and the other built files. The pages themselves are served by panel and setup.
func Assets() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.StripPrefix("/ui/", http.FileServer(http.FS(sub)))
}

// Source is the built HTML and scripts joined together, so tests can check the copy.
func Source() string {
	var b strings.Builder
	_ = fs.WalkDir(dist, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasSuffix(path, ".html") || strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".css") {
			raw, err := fs.ReadFile(dist, path)
			if err != nil {
				return err
			}
			b.Write(raw)
		}
		return nil
	})
	return b.String()
}

// Page reads one built HTML file and, for the panel, injects the access code.
func Page(name, code string) []byte {
	b, err := dist.ReadFile("dist/" + name)
	if err != nil {
		return []byte("<!doctype html><title>UI missing</title><p>The panel UI is not built.</p>")
	}
	return []byte(strings.ReplaceAll(string(b), `"%%PANEL_CODE%%"`, strconv.Quote(code)))
}
