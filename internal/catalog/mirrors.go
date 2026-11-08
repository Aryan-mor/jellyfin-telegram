package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
)

// Parts a mirror channel can receive. The order is the order they are stored and shown.
var MirrorKinds = []string{"movie", "series", "music", "video"}

// ensureMirrorUsername adds the public name to a database created before that column existed.
func ensureMirrorUsername(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('mirrors') WHERE name='username'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`ALTER TABLE mirrors ADD COLUMN username TEXT NOT NULL DEFAULT ''`)
	return err
}

const mirrorSchema = `
CREATE TABLE IF NOT EXISTS mirrors (
	id         INTEGER PRIMARY KEY,
	channel_id INTEGER NOT NULL UNIQUE,
	title      TEXT NOT NULL DEFAULT '',
	bot_id     INTEGER NOT NULL,
	kinds      TEXT NOT NULL,
	error      TEXT NOT NULL DEFAULT '',
	username   TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS mirror_sent (
	mirror_id  INTEGER NOT NULL,
	message_id INTEGER NOT NULL,
	dest_id    INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (mirror_id, message_id)
);
`

// Mirror is one channel that receives a chosen part of the main channel.
type Mirror struct {
	ID        int64
	ChannelID int64
	Title     string
	BotID     int64
	Kinds     []string
	Error     string
	// Username is the public name, empty when the channel is private.
	Username string
}

// Wants reports whether this mirror should receive a file of that section.
func (m Mirror) Wants(section string) bool {
	for _, k := range m.Kinds {
		if k == section {
			return true
		}
	}
	return false
}

// MirrorItem is a main-channel file that may still need copying.
type MirrorItem struct {
	MessageID int
	FileName  string
	Caption   string
	MIME      string
}

// NormalizeKinds keeps known parts, in MirrorKinds order. An empty or unknown list is an error.
func NormalizeKinds(in []string) ([]string, error) {
	have := map[string]bool{}
	for _, k := range in {
		ok := false
		for _, known := range MirrorKinds {
			if k == known {
				ok = true
			}
		}
		if !ok {
			return nil, errors.New("choose films, series, music or general video")
		}
		have[k] = true
	}
	out := make([]string, 0, len(have))
	for _, k := range MirrorKinds {
		if have[k] {
			out = append(out, k)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("choose at least one part to mirror")
	}
	return out, nil
}

// Mirrors returns every mirror channel.
func (c *Catalog) Mirrors(ctx context.Context) ([]Mirror, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT id, channel_id, title, bot_id, kinds, error, username FROM mirrors ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Mirror
	for rows.Next() {
		m, err := scanMirror(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if out == nil {
		out = []Mirror{}
	}
	return out, rows.Err()
}

type mirrorScanner interface {
	Scan(dest ...any) error
}

func scanMirror(s mirrorScanner) (Mirror, error) {
	var m Mirror
	var kinds string
	if err := s.Scan(&m.ID, &m.ChannelID, &m.Title, &m.BotID, &kinds, &m.Error, &m.Username); err != nil {
		return Mirror{}, err
	}
	if kinds != "" {
		m.Kinds = strings.Split(kinds, ",")
	} else {
		m.Kinds = []string{}
	}
	return m, nil
}

// AddMirror stores a mirror. The channel can be listed once.
func (c *Catalog) AddMirror(ctx context.Context, channelID, botID int64, title string, kinds []string) (Mirror, error) {
	kinds, err := NormalizeKinds(kinds)
	if err != nil {
		return Mirror{}, err
	}
	res, err := c.db.ExecContext(ctx, `INSERT INTO mirrors (channel_id, title, bot_id, kinds) VALUES (?,?,?,?)`,
		channelID, title, botID, strings.Join(kinds, ","))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Mirror{}, errors.New("that channel is already a mirror")
		}
		return Mirror{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Mirror{}, err
	}
	return c.mirror(ctx, id)
}

// UpdateMirror changes the bot and the parts of one mirror, and stores a fresh title.
func (c *Catalog) UpdateMirror(ctx context.Context, id, botID int64, title string, kinds []string) (Mirror, error) {
	kinds, err := NormalizeKinds(kinds)
	if err != nil {
		return Mirror{}, err
	}
	res, err := c.db.ExecContext(ctx, `UPDATE mirrors SET bot_id=?, title=?, kinds=?, error='' WHERE id=?`,
		botID, title, strings.Join(kinds, ","), id)
	if err != nil {
		return Mirror{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Mirror{}, err
	}
	if n == 0 {
		return Mirror{}, errors.New("that mirror is not listed")
	}
	return c.mirror(ctx, id)
}

// SetMirrorUsername stores the public name of a mirror. An empty name means the channel is private.
func (c *Catalog) SetMirrorUsername(ctx context.Context, id int64, username string) error {
	res, err := c.db.ExecContext(ctx, `UPDATE mirrors SET username=? WHERE id=?`, strings.TrimPrefix(username, "@"), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("that mirror is not listed")
	}
	return nil
}

func (c *Catalog) mirror(ctx context.Context, id int64) (Mirror, error) {
	row := c.db.QueryRowContext(ctx, `SELECT id, channel_id, title, bot_id, kinds, error, username FROM mirrors WHERE id=?`, id)
	m, err := scanMirror(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Mirror{}, errors.New("that mirror is not listed")
	}
	return m, err
}

// DeleteMirror forgets a mirror and which messages were copied there.
func (c *Catalog) DeleteMirror(ctx context.Context, id int64) error {
	res, err := c.db.ExecContext(ctx, `DELETE FROM mirrors WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("that mirror is not listed")
	}
	_, err = c.db.ExecContext(ctx, `DELETE FROM mirror_sent WHERE mirror_id=?`, id)
	return err
}

// SetMirrorError records the last copy failure. An empty string clears it.
func (c *Catalog) SetMirrorError(ctx context.Context, id int64, msg string) error {
	_, err := c.db.ExecContext(ctx, `UPDATE mirrors SET error=? WHERE id=?`, msg, id)
	return err
}

// MirrorSent counts messages copied into one mirror.
func (c *Catalog) MirrorSent(ctx context.Context, id int64) (int, error) {
	var n int
	err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mirror_sent WHERE mirror_id=?`, id).Scan(&n)
	return n, err
}

// Unmirrored returns main-channel files that have not been copied to this mirror yet.
func (c *Catalog) Unmirrored(ctx context.Context, id int64) ([]MirrorItem, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT message_id, file_name, caption, mime FROM files f
		WHERE NOT EXISTS (SELECT 1 FROM mirror_sent s WHERE s.mirror_id=? AND s.message_id=f.message_id)
		ORDER BY message_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MirrorItem
	for rows.Next() {
		var it MirrorItem
		if err := rows.Scan(&it.MessageID, &it.FileName, &it.Caption, &it.MIME); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// MarkSent records that a main-channel message was copied. destID is the new message, or zero when
// Telegram did not report one.
func (c *Catalog) MarkSent(ctx context.Context, mirrorID int64, messageID, destID int) error {
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO mirror_sent (mirror_id, message_id, dest_id) VALUES (?,?,?)
		ON CONFLICT(mirror_id, message_id) DO UPDATE SET dest_id=excluded.dest_id`,
		mirrorID, messageID, destID)
	return err
}

// ArchiveOf returns the subtitle archive that answers a message. A row written right after the
// archive was sent has no document id until the next sync, and is not counted yet.
func (c *Catalog) ArchiveOf(ctx context.Context, messageID int) (media.Attachment, bool, error) {
	var a media.Attachment
	err := c.db.QueryRowContext(ctx, `
		SELECT message_id, doc_id, size, file_name, reply_to FROM attachments
		WHERE reply_to=? AND doc_id<>0 AND file_name LIKE 'jftg-subs-%' ORDER BY message_id LIMIT 1`, messageID).
		Scan(&a.MessageID, &a.DocID, &a.Size, &a.FileName, &a.ReplyTo)
	if errors.Is(err, sql.ErrNoRows) {
		return media.Attachment{}, false, nil
	}
	return a, err == nil, err
}

// UnmirroredArchives returns the subtitle archives of videos that were copied to this mirror and
// that have not been copied themselves. It catches up an archive that was not there yet when its
// video went.
func (c *Catalog) UnmirroredArchives(ctx context.Context, id int64) ([]int, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT a.message_id FROM attachments a
		WHERE a.doc_id<>0 AND a.file_name LIKE 'jftg-subs-%'
		  AND EXISTS (SELECT 1 FROM mirror_sent v WHERE v.mirror_id=? AND v.message_id=a.reply_to)
		  AND NOT EXISTS (SELECT 1 FROM mirror_sent s WHERE s.mirror_id=? AND s.message_id=a.message_id)
		ORDER BY a.message_id`, id, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var m int
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
