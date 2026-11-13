package seerr

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestWatchlistTitlesReadsSeerrDB(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "config", "db", "db.sqlite3")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE watchlist (
		tmdbId INTEGER NOT NULL,
		mediaType varchar NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO watchlist (tmdbId, mediaType) VALUES (279246, 'tv'), (603, 'movie')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	got, err := WatchlistTitles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].TMDB != 279246 || !got[0].Series || got[1].TMDB != 603 || got[1].Series {
		t.Fatalf("%+v", got)
	}
}
