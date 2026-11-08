package catalog

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const sourceSchema = `
CREATE TABLE IF NOT EXISTS tgsources (
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
CREATE TABLE IF NOT EXISTS tgcopied (
	doc_id INTEGER PRIMARY KEY,
	source_id INTEGER NOT NULL,
	message_id INTEGER NOT NULL,
	file_name TEXT NOT NULL,
	size INTEGER NOT NULL,
	state TEXT NOT NULL,
	error TEXT NOT NULL DEFAULT '',
	updated_unix INTEGER NOT NULL
);`

// Outcomes of looking at one document in a source channel.
const (
	CopyDone    = "copied"
	CopyFailed  = "failed"
	CopyPresent = "present"
)

// Source is one channel the signed-in account reads and copies videos from.
type Source struct {
	ID        int64  `json:"id"`
	ChannelID int64  `json:"channel_id"`
	Username  string `json:"username,omitempty"`
	Title     string `json:"title"`
	// NoForwards is true when the channel owner forbids copying out of it.
	NoForwards bool `json:"no_forwards"`
	Enabled    bool `json:"enabled"`
	// Strict keeps only files whose name shows a year or an episode number. It suits a channel that
	// posts everything.
	Strict bool `json:"strict"`
	// Cursor is where an interrupted walk resumes, and zero when none is in progress.
	Cursor int `json:"-"`
	// ScanTop is the newest message of the walk in progress.
	ScanTop int `json:"-"`
	// Watermark is the newest message every older walk has fully handled.
	Watermark int `json:"watermark"`
	// CoveredDays is how far back the last finished walk reached. Zero means all of it, and -1 none yet.
	CoveredDays int    `json:"-"`
	LastScan    int64  `json:"last_scan"`
	Error       string `json:"error,omitempty"`
	Found       int    `json:"found"`
	Copied      int    `json:"copied"`
	Added       int64  `json:"added"`
	InProgress  bool   `json:"in_progress"`
}

const sourceColumns = `SELECT id, channel_id, username, title, no_forwards, enabled, strict, cursor, scan_top, watermark, covered_days, last_scan_unix, error, found, copied, added_unix FROM tgsources`

func scanSource(s scanner) (Source, error) {
	var src Source
	var nf, en, st int
	err := s.Scan(&src.ID, &src.ChannelID, &src.Username, &src.Title, &nf, &en, &st, &src.Cursor, &src.ScanTop,
		&src.Watermark, &src.CoveredDays, &src.LastScan, &src.Error, &src.Found, &src.Copied, &src.Added)
	src.NoForwards = nf != 0
	src.Enabled = en != 0
	src.Strict = st != 0
	src.InProgress = src.Cursor != 0
	return src, err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// AddSource stores a channel, or refreshes the details of one already stored.
func (c *Catalog) AddSource(ctx context.Context, s Source) (Source, error) {
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO tgsources (channel_id, username, title, no_forwards, added_unix) VALUES (?,?,?,?,?)
		ON CONFLICT(channel_id) DO UPDATE SET username=excluded.username, title=excluded.title,
			no_forwards=excluded.no_forwards`,
		s.ChannelID, s.Username, s.Title, b2i(s.NoForwards), time.Now().Unix())
	if err != nil {
		return Source{}, err
	}
	return c.sourceByChannel(ctx, s.ChannelID)
}

func (c *Catalog) sourceByChannel(ctx context.Context, channelID int64) (Source, error) {
	return scanSource(c.db.QueryRowContext(ctx, sourceColumns+` WHERE channel_id=?`, channelID))
}

// Source returns one source.
func (c *Catalog) Source(ctx context.Context, id int64) (Source, bool, error) {
	s, err := scanSource(c.db.QueryRowContext(ctx, sourceColumns+` WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, false, nil
	}
	return s, err == nil, err
}

// Sources lists every source in the order it was added.
func (c *Catalog) Sources(ctx context.Context) ([]Source, error) {
	rows, err := c.db.QueryContext(ctx, sourceColumns+` ORDER BY id`)
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

// DeleteSource forgets a channel. What was already copied stays in the library.
func (c *Catalog) DeleteSource(ctx context.Context, id int64) error {
	_, err := c.db.ExecContext(ctx, `DELETE FROM tgsources WHERE id=?`, id)
	return err
}

// SetSourceEnabled pauses or resumes one channel.
func (c *Catalog) SetSourceEnabled(ctx context.Context, id int64, on bool) error {
	_, err := c.db.ExecContext(ctx, `UPDATE tgsources SET enabled=? WHERE id=?`, b2i(on), id)
	return err
}

// SetSourceStrict turns the stricter name check on or off for one channel.
func (c *Catalog) SetSourceStrict(ctx context.Context, id int64, on bool) error {
	_, err := c.db.ExecContext(ctx, `UPDATE tgsources SET strict=? WHERE id=?`, b2i(on), id)
	return err
}

// CopiedFile reports whether a file with this name and size was already copied or is in the library.
// The same film posted by two channels has two document ids, so the id alone does not catch it.
func (c *Catalog) CopiedFile(ctx context.Context, name string, size int64) (bool, error) {
	var n int
	err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tgcopied WHERE file_name=? AND size=? AND state=?`, name, size, CopyDone).Scan(&n)
	if err != nil || n > 0 {
		return n > 0, err
	}
	err = c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE file_name=? AND size=?`, name, size).Scan(&n)
	return n > 0, err
}

// SourceWalk is the progress of one walk through a channel's history.
type SourceWalk struct {
	Cursor, ScanTop, Watermark, CoveredDays int
}

// SaveSourceWalk stores progress and clears the last error.
func (c *Catalog) SaveSourceWalk(ctx context.Context, id int64, w SourceWalk, errText string) error {
	_, err := c.db.ExecContext(ctx, `UPDATE tgsources SET cursor=?, scan_top=?, watermark=?, covered_days=?,
		last_scan_unix=?, error=? WHERE id=?`,
		w.Cursor, w.ScanTop, w.Watermark, w.CoveredDays, time.Now().Unix(), errText, id)
	return err
}

// SetSourceError records why a channel cannot be read, without touching its progress.
func (c *Catalog) SetSourceError(ctx context.Context, id int64, errText string) error {
	_, err := c.db.ExecContext(ctx, `UPDATE tgsources SET error=?, last_scan_unix=? WHERE id=?`, errText, time.Now().Unix(), id)
	return err
}

// AddSourceCounts adds to the totals shown for a channel.
func (c *Catalog) AddSourceCounts(ctx context.Context, id int64, found, copied int) error {
	_, err := c.db.ExecContext(ctx, `UPDATE tgsources SET found=found+?, copied=copied+? WHERE id=?`, found, copied, id)
	return err
}

// RestartSourceWalk makes the next pass look at the whole window again. Documents already copied are skipped.
func (c *Catalog) RestartSourceWalk(ctx context.Context) error {
	_, err := c.db.ExecContext(ctx, `UPDATE tgsources SET cursor=0, scan_top=0, watermark=0, covered_days=-1`)
	return err
}

// CopyOrigin is the channel a document was copied from. ok is false when this document was not copied.
func (c *Catalog) CopyOrigin(ctx context.Context, docID int64) (Source, bool, error) {
	var id int64
	err := c.db.QueryRowContext(ctx, `SELECT source_id FROM tgcopied WHERE doc_id=? AND state=?`, docID, CopyDone).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, false, nil
	}
	if err != nil {
		return Source{}, false, err
	}
	return c.Source(ctx, id)
}

// CopiedDoc reports whether this document was already copied, or already refused for good.
func (c *Catalog) CopiedDoc(ctx context.Context, docID int64) (bool, error) {
	var n int
	err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tgcopied WHERE doc_id=? AND state IN (?,?)`, docID, CopyDone, CopyPresent).Scan(&n)
	if err != nil || n > 0 {
		return n > 0, err
	}
	err = c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE doc_id=?`, docID).Scan(&n)
	return n > 0, err
}

// MarkCopied remembers a document so no other channel, and no later pass, posts it again.
func (c *Catalog) MarkCopied(ctx context.Context, at time.Time, docID, sourceID int64, messageID int, name string, size int64, state, errText string) error {
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO tgcopied (doc_id, source_id, message_id, file_name, size, state, error, updated_unix)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(doc_id) DO UPDATE SET source_id=excluded.source_id, message_id=excluded.message_id,
			file_name=excluded.file_name, size=excluded.size, state=excluded.state, error=excluded.error,
			updated_unix=excluded.updated_unix`,
		docID, sourceID, messageID, name, size, state, errText, at.Unix())
	return err
}

// CopyCount is how many documents have been copied in total.
func (c *Catalog) CopyCount(ctx context.Context, since time.Time) (int, error) {
	var n int
	err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tgcopied WHERE state=? AND updated_unix>=?`, CopyDone, since.Unix()).Scan(&n)
	return n, err
}

// RecentCopies lists the newest documents copied or refused.
func (c *Catalog) RecentCopies(ctx context.Context, limit int) ([]CopyRow, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT source_id, file_name, size, state, error, updated_unix FROM tgcopied
		ORDER BY updated_unix DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CopyRow
	for rows.Next() {
		var r CopyRow
		if err := rows.Scan(&r.SourceID, &r.Name, &r.Size, &r.State, &r.Error, &r.Updated); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CopyRow is one line of the recent-copies list.
type CopyRow struct {
	SourceID int64  `json:"source_id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	State    string `json:"state"`
	Error    string `json:"error,omitempty"`
	Updated  int64  `json:"updated"`
}

// StateValue reads a stored string, or "" when there is none.
func (c *Catalog) StateValue(ctx context.Context, key string) (string, error) {
	var v string
	err := c.db.QueryRowContext(ctx, `SELECT value FROM state WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetStateValue stores a string under key.
func (c *Catalog) SetStateValue(ctx context.Context, key, value string) error {
	_, err := c.db.ExecContext(ctx, `INSERT INTO state(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}
