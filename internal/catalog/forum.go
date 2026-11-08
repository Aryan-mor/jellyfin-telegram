package catalog

import (
	"context"
	"database/sql"
	"errors"
)

const forumSchema = `
DROP TABLE IF EXISTS forum_claims;
CREATE TABLE IF NOT EXISTS inbox_files (
	message_id INTEGER PRIMARY KEY,
	state TEXT NOT NULL,
	ready_id INTEGER NOT NULL DEFAULT 0,
	doc_id INTEGER NOT NULL DEFAULT 0
);
`

// InboxState returns the pipeline state of one inbox message. An unknown id has an empty state.
func (c *Catalog) InboxState(ctx context.Context, messageID int) (string, int, error) {
	var state string
	var ready int
	err := c.db.QueryRowContext(ctx, `SELECT state, ready_id FROM inbox_files WHERE message_id=?`, messageID).Scan(&state, &ready)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	return state, ready, err
}

// SetInbox records how far an inbox file has gone. readyID is the Ready message, or zero.
// A docID of zero leaves a document id already stored.
func (c *Catalog) SetInbox(ctx context.Context, messageID int, state string, readyID int, docID int64) error {
	_, err := c.db.ExecContext(ctx, `INSERT INTO inbox_files (message_id, state, ready_id, doc_id) VALUES (?,?,?,?)
		ON CONFLICT(message_id) DO UPDATE SET state=excluded.state, ready_id=excluded.ready_id,
			doc_id=CASE WHEN excluded.doc_id=0 THEN inbox_files.doc_id ELSE excluded.doc_id END`,
		messageID, state, readyID, docID)
	return err
}

// CloseInboxDoc marks open inbox rows that carry this document as already published.
// It returns their message ids.
func (c *Catalog) CloseInboxDoc(ctx context.Context, docID int64) ([]int, error) {
	if docID == 0 {
		return nil, nil
	}
	rows, err := c.db.QueryContext(ctx, `SELECT message_id FROM inbox_files WHERE doc_id=? AND state='open'`, docID)
	if err != nil {
		return nil, err
	}
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(ids) == 0 {
		return nil, nil
	}
	_, err = c.db.ExecContext(ctx, `UPDATE inbox_files SET state='duplicate' WHERE doc_id=? AND state='open'`, docID)
	return ids, err
}

// InboxAfter lists up to limit message ids in state, strictly after after, oldest first.
func (c *Catalog) InboxAfter(ctx context.Context, state string, after, limit int) ([]int, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT message_id FROM inbox_files WHERE state=? AND message_id>? ORDER BY message_id LIMIT ?`, state, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// OpenInbox lists inbox files that still need a decision.
func (c *Catalog) OpenInbox(ctx context.Context) ([]int, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT message_id FROM inbox_files WHERE state=? ORDER BY message_id`, "open")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// DocPublished reports that a Telegram document is already in the library: either the indexer has
// it from a verified Ready post, or an inbox file with it was published or closed as a duplicate.
// A document id stays the same when Telegram copies a file, so this finds every copy.
func (c *Catalog) DocPublished(ctx context.Context, docID int64) (bool, error) {
	if docID == 0 {
		return false, nil
	}
	var n int
	err := c.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM files WHERE doc_id=?) +
		(SELECT COUNT(*) FROM inbox_files WHERE doc_id=? AND state IN ('ready','duplicate'))`, docID, docID).Scan(&n)
	return n > 0, err
}

// SetCaption keeps a row's caption in step with the message, after a bot edited it.
func (c *Catalog) SetCaption(ctx context.Context, messageID int, caption string) error {
	_, err := c.db.ExecContext(ctx, `UPDATE files SET caption=? WHERE message_id=?`, caption, messageID)
	return err
}
