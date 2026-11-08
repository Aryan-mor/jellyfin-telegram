package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

const newsSchema = `
CREATE TABLE IF NOT EXISTS tgnews (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	channel_id INTEGER NOT NULL UNIQUE,
	username TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL DEFAULT '',
	no_forwards INTEGER NOT NULL DEFAULT 0,
	enabled INTEGER NOT NULL DEFAULT 1,
	strict INTEGER NOT NULL DEFAULT 0,
	cursor INTEGER NOT NULL DEFAULT 0,
	scan_top INTEGER NOT NULL DEFAULT 0,
	watermark INTEGER NOT NULL DEFAULT 0,
	covered_days INTEGER NOT NULL DEFAULT -1,
	last_scan_unix INTEGER NOT NULL DEFAULT 0,
	error TEXT NOT NULL DEFAULT '',
	found INTEGER NOT NULL DEFAULT 0,
	copied INTEGER NOT NULL DEFAULT 0,
	added_unix INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS tgnewslinks (
	url TEXT PRIMARY KEY,
	source_id INTEGER NOT NULL,
	message_id INTEGER NOT NULL,
	state TEXT NOT NULL,
	detail TEXT NOT NULL DEFAULT '',
	saved_ids TEXT NOT NULL DEFAULT '',
	tries INTEGER NOT NULL DEFAULT 0,
	updated_unix INTEGER NOT NULL
);`

const newsColumns = `SELECT id, channel_id, username, title, no_forwards, enabled, strict, cursor, scan_top, watermark, covered_days, last_scan_unix, error, found, copied, added_unix, 'manual', '' FROM tgnews`

// NewsLink is one bot deep link taken from a news post.
type NewsLink struct {
	URL       string
	SourceID  int64
	MessageID int
	// State is pending, fetched, done, skipped or gave_up.
	State   string
	Detail  string
	SavedID []int
	Tries   int
}

// AddNews stores a news channel, or refreshes the details of one already stored.
func (c *Catalog) AddNews(ctx context.Context, s Source) (Source, error) {
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO tgnews (channel_id, username, title, no_forwards, added_unix) VALUES (?,?,?,?,?)
		ON CONFLICT(channel_id) DO UPDATE SET username=excluded.username, title=excluded.title,
			no_forwards=excluded.no_forwards`,
		s.ChannelID, s.Username, s.Title, b2i(s.NoForwards), time.Now().Unix())
	if err != nil {
		return Source{}, err
	}
	return scanSource(c.db.QueryRowContext(ctx, newsColumns+` WHERE channel_id=?`, s.ChannelID))
}

// NewsChannels lists every news channel in the order it was added.
func (c *Catalog) NewsChannels(ctx context.Context) ([]Source, error) {
	rows, err := c.db.QueryContext(ctx, newsColumns+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Source
	for rows.Next() {
		s, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteNews forgets a news channel. Files already forwarded stay in the library.
func (c *Catalog) DeleteNews(ctx context.Context, id int64) error {
	_, err := c.db.ExecContext(ctx, `DELETE FROM tgnews WHERE id=?`, id)
	return err
}

// SetNewsEnabled pauses or resumes one news channel.
func (c *Catalog) SetNewsEnabled(ctx context.Context, id int64, on bool) error {
	_, err := c.db.ExecContext(ctx, `UPDATE tgnews SET enabled=? WHERE id=?`, b2i(on), id)
	return err
}

// SaveNewsWalk stores progress and clears the last error.
func (c *Catalog) SaveNewsWalk(ctx context.Context, id int64, w SourceWalk, errText string) error {
	_, err := c.db.ExecContext(ctx, `UPDATE tgnews SET cursor=?, scan_top=?, watermark=?, covered_days=?,
		last_scan_unix=?, error=? WHERE id=?`,
		w.Cursor, w.ScanTop, w.Watermark, w.CoveredDays, time.Now().Unix(), errText, id)
	return err
}

// SetNewsError records why a news channel cannot be read.
func (c *Catalog) SetNewsError(ctx context.Context, id int64, errText string) error {
	_, err := c.db.ExecContext(ctx, `UPDATE tgnews SET error=?, last_scan_unix=? WHERE id=?`, errText, time.Now().Unix(), id)
	return err
}

// AddNewsCounts adds to the totals shown for a news channel.
func (c *Catalog) AddNewsCounts(ctx context.Context, id int64, found, copied int) error {
	_, err := c.db.ExecContext(ctx, `UPDATE tgnews SET found=found+?, copied=copied+? WHERE id=?`, found, copied, id)
	return err
}

// RestartNewsWalk makes the next pass look at the window again. Links already finished are skipped.
func (c *Catalog) RestartNewsWalk(ctx context.Context) error {
	_, err := c.db.ExecContext(ctx, `UPDATE tgnews SET cursor=0, scan_top=0, watermark=0, covered_days=-1`)
	return err
}

// GetNewsLink reads one deep link, or false when it has not been seen.
func (c *Catalog) GetNewsLink(ctx context.Context, rawURL string) (NewsLink, bool, error) {
	var n NewsLink
	var ids string
	err := c.db.QueryRowContext(ctx, `SELECT url, source_id, message_id, state, detail, saved_ids, tries FROM tgnewslinks WHERE url=?`, rawURL).
		Scan(&n.URL, &n.SourceID, &n.MessageID, &n.State, &n.Detail, &ids, &n.Tries)
	if errors.Is(err, sql.ErrNoRows) {
		return NewsLink{}, false, nil
	}
	if err != nil {
		return NewsLink{}, false, err
	}
	n.SavedID = parseIDs(ids)
	return n, true, nil
}

// PutNewsLink inserts or replaces one deep link.
func (c *Catalog) PutNewsLink(ctx context.Context, n NewsLink) error {
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO tgnewslinks (url, source_id, message_id, state, detail, saved_ids, tries, updated_unix)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(url) DO UPDATE SET source_id=excluded.source_id, message_id=excluded.message_id,
			state=excluded.state, detail=excluded.detail, saved_ids=excluded.saved_ids,
			tries=excluded.tries, updated_unix=excluded.updated_unix`,
		n.URL, n.SourceID, n.MessageID, n.State, n.Detail, formatIDs(n.SavedID), n.Tries, time.Now().Unix())
	return err
}

// NewsRecent is one line of the news-tab recent list.
type NewsRecent struct {
	URL      string `json:"url"`
	SourceID int64  `json:"source_id"`
	State    string `json:"state"`
	Detail   string `json:"detail,omitempty"`
	Updated  int64  `json:"updated"`
}

// RecentNews lists the newest bot links this tab has opened or refused.
func (c *Catalog) RecentNews(ctx context.Context, limit int) ([]NewsRecent, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT url, source_id, state, detail, updated_unix FROM tgnewslinks
		ORDER BY updated_unix DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NewsRecent
	for rows.Next() {
		var r NewsRecent
		if err := rows.Scan(&r.URL, &r.SourceID, &r.State, &r.Detail, &r.Updated); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func formatIDs(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}

func parseIDs(raw string) []int {
	if raw == "" {
		return nil
	}
	var out []int
	for _, p := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}
