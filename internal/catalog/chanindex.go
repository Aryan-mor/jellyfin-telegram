package catalog

import "context"

const chanindexSchema = `
CREATE TABLE IF NOT EXISTS chanindex_voted (
  ref TEXT NOT NULL,
  tag TEXT NOT NULL,
  kind TEXT NOT NULL,
  source_id INTEGER NOT NULL,
  voted INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (ref, tag)
);`

// WatchChannelIndex remembers a channel added from the channel index, so a later vote can be sent once.
func (c *Catalog) WatchChannelIndex(ctx context.Context, ref, tag, kind string, sourceID int64) error {
	_, err := c.db.ExecContext(ctx,
		`INSERT INTO chanindex_voted(ref, tag, kind, source_id) VALUES(?,?,?,?)
		 ON CONFLICT(ref, tag) DO UPDATE SET kind=excluded.kind, source_id=excluded.source_id`,
		ref, tag, kind, sourceID)
	return err
}

// ChannelIndexPending is a watched channel that has not been voted on yet.
type ChannelIndexPending struct {
	Ref      string
	Tag      string
	Kind     string
	SourceID int64
	Copied   int
}

// ChannelIndexPending returns watched channels and how many files each has brought in.
func (c *Catalog) ChannelIndexPending(ctx context.Context) ([]ChannelIndexPending, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT ref, tag, kind, source_id FROM chanindex_voted WHERE voted=0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChannelIndexPending
	for rows.Next() {
		var p ChannelIndexPending
		if err := rows.Scan(&p.Ref, &p.Tag, &p.Kind, &p.SourceID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		table := "tgsources"
		if out[i].Kind == "news" {
			table = "tgnews"
		}
		_ = c.db.QueryRowContext(ctx, `SELECT copied FROM `+table+` WHERE id=?`, out[i].SourceID).Scan(&out[i].Copied)
	}
	return out, nil
}

// MarkChannelIndexVoted records that the vote was sent.
func (c *Catalog) MarkChannelIndexVoted(ctx context.Context, ref, tag string) error {
	_, err := c.db.ExecContext(ctx, `UPDATE chanindex_voted SET voted=1 WHERE ref=? AND tag=?`, ref, tag)
	return err
}
