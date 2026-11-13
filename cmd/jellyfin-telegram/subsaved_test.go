package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/Aryan-mor/jellyfin-telegram/internal/subtitles"

	_ "modernc.org/sqlite"
)

func TestWatchlistSeriesCoversEveryEpisode(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "config", "db", "db.sqlite3")
	if err := sqlOpenWatchlist(dbPath); err != nil {
		t.Fatal(err)
	}
	s := &savedTitles{root: dir}
	ctx := context.Background()
	if !s.allows(ctx, subtitles.Meta{TMDB: 279246, Season: 1, Episode: 4}) {
		t.Fatal("an episode of a watchlisted series was skipped")
	}
	if s.allows(ctx, subtitles.Meta{TMDB: 279246}) {
		t.Fatal("the series id was treated as a film")
	}
	if !s.allows(ctx, subtitles.Meta{TMDB: 603}) {
		t.Fatal("a watchlisted film was skipped")
	}
}

func sqlOpenWatchlist(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE watchlist (tmdbId integer, mediaType text)`); err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO watchlist (tmdbId, mediaType) VALUES (279246, 'tv'), (603, 'movie')`)
	return err
}
