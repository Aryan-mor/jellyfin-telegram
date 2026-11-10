package catalog

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
)

// Upload states. A file moves forward and is never deleted from disk before probed.
const (
	UploadQueued    = "queued"
	UploadUploading = "uploading"
	UploadSent      = "sent"
	UploadVerified  = "verified"
	UploadIndexed   = "indexed"
	UploadProbed    = "probed"
	UploadCleaned   = "cleaned"
	UploadTooBig    = "too_big"
	UploadFailed    = "failed"
	// UploadMismatch means the bytes in the channel differ from the local file. Nothing is deleted for it.
	UploadMismatch = "mismatch"
	// UploadPresent means Ready already has this title, so the file was not uploaded and is not deleted.
	UploadPresent = "present"
)

const uploadSchema = `
CREATE TABLE IF NOT EXISTS uploads (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	kind TEXT NOT NULL,
	remote_id INTEGER NOT NULL,
	owner_id INTEGER NOT NULL,
	path TEXT NOT NULL,
	size INTEGER NOT NULL,
	file_name TEXT NOT NULL,
	caption TEXT NOT NULL,
	state TEXT NOT NULL,
	message_id INTEGER NOT NULL DEFAULT 0,
	session_user INTEGER NOT NULL DEFAULT 0,
	download_id TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT '',
	updated_unix INTEGER NOT NULL,
	UNIQUE(kind, remote_id)
);`

// KindLocal marks an upload row for a video that was already on this computer. Its RemoteID is a stable number made from the file's path.
const KindLocal = "local"

// KindHome marks a file a person chose to send to the personal channel.
const KindHome = "home"

// Upload is one file on its way to the channel. Kind is KindLocal for a file the person chose in the
// panel. Older databases may still hold rows of other kinds, which nothing uploads any more.
type Upload struct {
	ID          int64
	Kind        string
	RemoteID    int
	OwnerID     int
	Path        string
	Size        int64
	FileName    string
	Caption     string
	State       string
	MessageID   int
	SessionUser int64
	DownloadID  string
	Error       string
	Updated     time.Time
}

func (c *Catalog) ensureUploads() error {
	_, err := c.db.Exec(uploadSchema)
	return err
}

// UpsertUpload inserts a new row or refreshes the path and size of an existing one.
// State, message id and error are left as they were when the row already exists.
func (c *Catalog) UpsertUpload(ctx context.Context, u Upload) (Upload, error) {
	if err := c.ensureUploads(); err != nil {
		return Upload{}, err
	}
	now := time.Now().Unix()
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO uploads (kind, remote_id, owner_id, path, size, file_name, caption, state, download_id, error, updated_unix)
		VALUES (?,?,?,?,?,?,?,?,?,'',?)
		ON CONFLICT(kind, remote_id) DO UPDATE SET
			owner_id=excluded.owner_id, path=excluded.path, size=excluded.size,
			file_name=excluded.file_name, caption=excluded.caption,
			download_id=CASE WHEN excluded.download_id='' THEN uploads.download_id ELSE excluded.download_id END,
			updated_unix=excluded.updated_unix`,
		u.Kind, u.RemoteID, u.OwnerID, u.Path, u.Size, u.FileName, u.Caption, u.State, u.DownloadID, now)
	if err != nil {
		return Upload{}, err
	}
	got, ok, err := c.FindUpload(ctx, u.Kind, u.RemoteID)
	if err != nil || !ok {
		if err == nil {
			err = errors.New("upload row missing after insert")
		}
		return Upload{}, err
	}
	return got, nil
}

// SetUpload records progress. An empty error clears the previous one.
func (c *Catalog) SetUpload(ctx context.Context, id int64, state string, messageID int, sessionUser int64, errText string) error {
	_, err := c.db.ExecContext(ctx, `
		UPDATE uploads SET state=?, message_id=?, session_user=CASE WHEN ?=0 THEN session_user ELSE ? END,
			error=?, updated_unix=? WHERE id=?`,
		state, messageID, sessionUser, sessionUser, errText, time.Now().Unix(), id)
	return err
}

// DeleteUpload forgets a row, so a file that never reached the channel can be chosen again.
func (c *Catalog) DeleteUpload(ctx context.Context, id int64) error {
	if err := c.ensureUploads(); err != nil {
		return err
	}
	_, err := c.db.ExecContext(ctx, `DELETE FROM uploads WHERE id=?`, id)
	return err
}

// FindUpload returns the row for one imported file.
func (c *Catalog) FindUpload(ctx context.Context, kind string, remoteID int) (Upload, bool, error) {
	if err := c.ensureUploads(); err != nil {
		return Upload{}, false, err
	}
	row := c.db.QueryRowContext(ctx, uploadColumns+` WHERE kind=? AND remote_id=?`, kind, remoteID)
	u, err := scanUpload(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Upload{}, false, nil
	}
	return u, err == nil, err
}

// ListUploads returns every imported file, newest activity first.
func (c *Catalog) ListUploads(ctx context.Context) ([]Upload, error) {
	if err := c.ensureUploads(); err != nil {
		return nil, err
	}
	rows, err := c.db.QueryContext(ctx, uploadColumns+` ORDER BY updated_unix DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Upload
	for rows.Next() {
		u, err := scanUpload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

const uploadColumns = `SELECT id, kind, remote_id, owner_id, path, size, file_name, caption, state, message_id, session_user, download_id, error, updated_unix FROM uploads`

func scanUpload(s scanner) (Upload, error) {
	var u Upload
	var updated int64
	err := s.Scan(&u.ID, &u.Kind, &u.RemoteID, &u.OwnerID, &u.Path, &u.Size, &u.FileName, &u.Caption,
		&u.State, &u.MessageID, &u.SessionUser, &u.DownloadID, &u.Error, &updated)
	u.Updated = time.Unix(updated, 0).UTC()
	return u, err
}

// FindFile returns a channel file with this name and size, used to avoid posting it twice.
func (c *Catalog) FindFile(ctx context.Context, name string, size int64) (media.File, bool, error) {
	row := c.db.QueryRowContext(ctx, `SELECT `+columns+` FROM files WHERE file_name=? AND size=? ORDER BY message_id LIMIT 1`, name, size)
	f, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return f, false, nil
	}
	return f, err == nil, err
}
