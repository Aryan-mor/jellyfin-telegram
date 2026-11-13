package seerr

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// WatchlistTitles lists every film and series someone saved in Seerr's watchlist.
// The Seerr HTTP API is per-user; for the managed bundle the SQLite db is enough.
func WatchlistTitles(root string) ([]OpenTitle, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	dbPath := filepath.Join(root, "config", "db", "db.sqlite3")
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT DISTINCT tmdbId, mediaType FROM watchlist WHERE tmdbId > 0`)
	if err != nil {
		return nil, fmt.Errorf("read watchlist: %w", err)
	}
	defer rows.Close()
	var out []OpenTitle
	seen := map[string]bool{}
	for rows.Next() {
		var tmdb int
		var kind string
		if err := rows.Scan(&tmdb, &kind); err != nil {
			return nil, err
		}
		series := strings.EqualFold(kind, "tv")
		id := fmt.Sprintf("%t:%d", series, tmdb)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, OpenTitle{TMDB: tmdb, Series: series})
	}
	return out, rows.Err()
}
