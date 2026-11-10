// Package catalog is the SQLite index of channel files.
package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/media"

	_ "modernc.org/sqlite" // database driver
)

// ErrNotFound is returned when a message is not in the catalog.
var ErrNotFound = errors.New("file not in catalog")

const schema = `
CREATE TABLE IF NOT EXISTS files (
	message_id INTEGER PRIMARY KEY,
	channel_id INTEGER NOT NULL,
	doc_id     INTEGER NOT NULL,
	size       INTEGER NOT NULL,
	mime       TEXT NOT NULL,
	file_name  TEXT NOT NULL,
	caption    TEXT NOT NULL,
	date_unix  INTEGER NOT NULL,
	has_thumb  INTEGER NOT NULL,
	rel_path   TEXT NOT NULL,
	probed_at  INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS files_rel_path ON files(rel_path);
CREATE TABLE IF NOT EXISTS state (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS attachments (
	message_id INTEGER PRIMARY KEY,
	doc_id     INTEGER NOT NULL,
	size       INTEGER NOT NULL,
	file_name  TEXT NOT NULL,
	reply_to   INTEGER NOT NULL
);
`

// migrations add columns to a database created by an earlier version.
var migrations = []struct{ column, ddl string }{
	{"duration_sec", `ALTER TABLE files ADD COLUMN duration_sec INTEGER NOT NULL DEFAULT 0`},
	{"subtitle_outcome", `ALTER TABLE files ADD COLUMN subtitle_outcome TEXT NOT NULL DEFAULT ''`},
	{"extract_state", `ALTER TABLE files ADD COLUMN extract_state TEXT NOT NULL DEFAULT ''`},
	{"extract_tries", `ALTER TABLE files ADD COLUMN extract_tries INTEGER NOT NULL DEFAULT 0`},
}

const columns = `message_id, channel_id, doc_id, size, mime, file_name, caption, date_unix, has_thumb, rel_path, probed_at, duration_sec, subtitle_outcome, extract_state`

// Catalog wraps the database.
type Catalog struct{ db *sql.DB }

// Open creates or opens the database at path.
func Open(path string) (*Catalog, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if _, err := db.Exec(uploadSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply upload schema: %w", err)
	}
	if _, err := db.Exec(sourceSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply source schema: %w", err)
	}
	if err := ensureSourceOrigin(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate sources: %w", err)
	}
	if _, err := db.Exec(trustSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply trust schema: %w", err)
	}
	if _, err := db.Exec(newsSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply news schema: %w", err)
	}
	if _, err := db.Exec(chanindexSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply channel index schema: %w", err)
	}
	if _, err := db.Exec(mirrorSchema + indexSharedSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply mirror schema: %w", err)
	}
	if err := ensureMirrorUsername(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate mirrors: %w", err)
	}
	if _, err := db.Exec(forumSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply forum schema: %w", err)
	}
	return &Catalog{db: db}, nil
}

func migrate(db *sql.DB) error {
	for _, m := range migrations {
		var n int
		err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('files') WHERE name=?`, m.column).Scan(&n)
		if err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		if _, err := db.Exec(m.ddl); err != nil {
			return err
		}
	}
	return nil
}

// Close closes the database.
func (c *Catalog) Close() error { return c.db.Close() }

// Put inserts a file or updates it. A changed document resets everything learned about the old one.
func (c *Catalog) Put(ctx context.Context, f media.File) error {
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO files (`+columns+`) VALUES (?,?,?,?,?,?,?,?,?,?,0,0,'','')
		ON CONFLICT(message_id) DO UPDATE SET
			channel_id=excluded.channel_id, doc_id=excluded.doc_id, size=excluded.size,
			mime=excluded.mime, file_name=excluded.file_name, caption=excluded.caption,
			date_unix=excluded.date_unix, has_thumb=excluded.has_thumb,
			rel_path=excluded.rel_path,
			probed_at=CASE WHEN files.doc_id=excluded.doc_id THEN files.probed_at ELSE 0 END,
			duration_sec=CASE WHEN files.doc_id=excluded.doc_id THEN files.duration_sec ELSE 0 END,
			subtitle_outcome=CASE WHEN files.doc_id=excluded.doc_id THEN files.subtitle_outcome ELSE '' END,
			extract_state=CASE WHEN files.doc_id=excluded.doc_id THEN files.extract_state ELSE '' END,
			extract_tries=CASE WHEN files.doc_id=excluded.doc_id THEN files.extract_tries ELSE 0 END`,
		f.MessageID, f.ChannelID, f.DocID, f.Size, f.MIME, f.FileName, f.Caption,
		f.Date.Unix(), f.HasThumb, f.RelPath)
	return err
}

// Get returns one file.
func (c *Catalog) Get(ctx context.Context, messageID int) (media.File, error) {
	row := c.db.QueryRowContext(ctx, `SELECT `+columns+` FROM files WHERE message_id=?`, messageID)
	f, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return media.File{}, ErrNotFound
	}
	return f, err
}

// All returns every file ordered by message id.
func (c *Catalog) All(ctx context.Context) ([]media.File, error) {
	return c.query(ctx, `SELECT `+columns+` FROM files ORDER BY message_id`)
}

// Unprobed returns files Jellyfin has not probed yet.
func (c *Catalog) Unprobed(ctx context.Context) ([]media.File, error) {
	return c.query(ctx, `SELECT `+columns+` FROM files WHERE probed_at=0 AND rel_path NOT LIKE 'Music/%' ORDER BY message_id`)
}

// Pending returns files that still need a probe or a subtitle check.
func (c *Catalog) Pending(ctx context.Context) ([]media.File, error) {
	return c.query(ctx, `SELECT `+columns+` FROM files WHERE (probed_at=0 OR subtitle_outcome='') AND rel_path NOT LIKE 'Music/%' ORDER BY message_id`)
}

// ExtractPending returns probed files whose embedded subtitles still need handling, leaving out
// files that failed too many times.
func (c *Catalog) ExtractPending(ctx context.Context, maxTries int) ([]media.File, error) {
	return c.query(ctx, `SELECT `+columns+` FROM files
		WHERE probed_at>0 AND extract_state='' AND extract_tries<? AND rel_path NOT LIKE 'Music/%' ORDER BY message_id`, maxTries)
}

// ExtractDone returns files whose subtitles are kept in the channel.
func (c *Catalog) ExtractDone(ctx context.Context) ([]media.File, error) {
	return c.query(ctx, `SELECT `+columns+` FROM files WHERE extract_state='done' ORDER BY message_id`)
}

// SetExtractState records how far the handling of embedded subtitles has come.
func (c *Catalog) SetExtractState(ctx context.Context, messageID int, state string) error {
	_, err := c.db.ExecContext(ctx, `UPDATE files SET extract_state=?, extract_tries=0 WHERE message_id=?`, state, messageID)
	return err
}

// ResetExtractTries gives files that failed before another chance, for example after a restart.
func (c *Catalog) ResetExtractTries(ctx context.Context) error {
	_, err := c.db.ExecContext(ctx, `UPDATE files SET extract_tries=0 WHERE extract_tries>0`)
	return err
}

// AddExtractTry counts a failed attempt.
func (c *Catalog) AddExtractTry(ctx context.Context, messageID int) error {
	_, err := c.db.ExecContext(ctx, `UPDATE files SET extract_tries=extract_tries+1 WHERE message_id=?`, messageID)
	return err
}

// PutAttachment stores a subtitle file found in the channel.
func (c *Catalog) PutAttachment(ctx context.Context, a media.Attachment) error {
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO attachments (message_id, doc_id, size, file_name, reply_to) VALUES (?,?,?,?,?)
		ON CONFLICT(message_id) DO UPDATE SET doc_id=excluded.doc_id, size=excluded.size,
			file_name=excluded.file_name, reply_to=excluded.reply_to`,
		a.MessageID, a.DocID, a.Size, a.FileName, a.ReplyTo)
	return err
}

// Attachments returns every subtitle file known in the channel.
func (c *Catalog) Attachments(ctx context.Context) ([]media.Attachment, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT message_id, doc_id, size, file_name, reply_to FROM attachments ORDER BY message_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []media.Attachment
	for rows.Next() {
		var a media.Attachment
		if err := rows.Scan(&a.MessageID, &a.DocID, &a.Size, &a.FileName, &a.ReplyTo); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteAttachment forgets a subtitle file.
func (c *Catalog) DeleteAttachment(ctx context.Context, messageID int) error {
	_, err := c.db.ExecContext(ctx, `DELETE FROM attachments WHERE message_id=?`, messageID)
	return err
}

// SetDuration stores the running time reported by a probe.
func (c *Catalog) SetDuration(ctx context.Context, messageID, seconds int) error {
	_, err := c.db.ExecContext(ctx, `UPDATE files SET duration_sec=? WHERE message_id=?`, seconds, messageID)
	return err
}

// SetSubtitleOutcome records a finished subtitle check.
func (c *Catalog) SetSubtitleOutcome(ctx context.Context, messageID int, outcome string) error {
	_, err := c.db.ExecContext(ctx, `UPDATE files SET subtitle_outcome=? WHERE message_id=?`, outcome, messageID)
	return err
}

// PathTaken reports whether another message already uses relPath.
func (c *Catalog) PathTaken(ctx context.Context, relPath string, messageID int) (bool, error) {
	var n int
	err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE rel_path=? AND message_id<>?`,
		relPath, messageID).Scan(&n)
	return n > 0, err
}

// MarkProbed records a successful probe.
func (c *Catalog) MarkProbed(ctx context.Context, messageID int, at time.Time) error {
	_, err := c.db.ExecContext(ctx, `UPDATE files SET probed_at=? WHERE message_id=?`, at.Unix(), messageID)
	return err
}

// Delete removes one file.
func (c *Catalog) Delete(ctx context.Context, messageID int) error {
	_, err := c.db.ExecContext(ctx, `DELETE FROM files WHERE message_id=?`, messageID)
	return err
}

// SwitchSubtitleLanguage stores the configured language. When it differs from the stored one,
// every finished check is cleared so files are checked again, and it reports true.
func (c *Catalog) SwitchSubtitleLanguage(ctx context.Context, code string) (bool, error) {
	var old string
	err := c.db.QueryRowContext(ctx, `SELECT value FROM state WHERE key='subtitle_language'`).Scan(&old)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if old == code {
		return false, nil
	}
	if _, err := c.db.ExecContext(ctx, `INSERT INTO state(key,value) VALUES('subtitle_language',?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, code); err != nil {
		return false, err
	}
	// Rows from before this setting existed were checked for the language now being stored.
	if old == "" {
		return false, nil
	}
	_, err = c.db.ExecContext(ctx, `UPDATE files SET subtitle_outcome=''`)
	return true, err
}

// LastMessageID returns the highest message id seen in the channel.
func (c *Catalog) LastMessageID(ctx context.Context) (int, error) {
	var v string
	err := c.db.QueryRowContext(ctx, `SELECT value FROM state WHERE key='last_message_id'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(v)
}

// SetLastMessageID stores the highest message id seen in the channel.
func (c *Catalog) SetLastMessageID(ctx context.Context, id int) error {
	_, err := c.db.ExecContext(ctx, `INSERT INTO state(key,value) VALUES('last_message_id',?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.Itoa(id))
	return err
}

type scanner interface{ Scan(...any) error }

func scan(s scanner) (media.File, error) {
	var f media.File
	var date, probed int64
	err := s.Scan(&f.MessageID, &f.ChannelID, &f.DocID, &f.Size, &f.MIME, &f.FileName, &f.Caption,
		&date, &f.HasThumb, &f.RelPath, &probed, &f.DurationSeconds, &f.SubtitleOutcome, &f.ExtractState)
	if err != nil {
		return f, err
	}
	f.Date = time.Unix(date, 0).UTC()
	if probed > 0 {
		f.ProbedAt = time.Unix(probed, 0).UTC()
	}
	return f, nil
}

func (c *Catalog) query(ctx context.Context, q string, args ...any) ([]media.File, error) {
	rows, err := c.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []media.File
	for rows.Next() {
		f, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
