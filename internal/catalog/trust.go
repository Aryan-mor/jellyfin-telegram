package catalog

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Origins of a source channel.
const (
	OriginManual     = "manual"
	OriginDiscovered = "discovered"
)

// Kinds of subject whose trust is counted.
const (
	TrustChannel = "channel"
	TrustKey     = "key"
)

const trustSchema = `
CREATE TABLE IF NOT EXISTS tgtrust (
	kind TEXT NOT NULL,
	id TEXT NOT NULL,
	points INTEGER NOT NULL DEFAULT 0,
	fails INTEGER NOT NULL DEFAULT 0,
	denied INTEGER NOT NULL DEFAULT 0,
	updated_unix INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (kind, id)
);
CREATE TABLE IF NOT EXISTS tgsearchseen (
	tag TEXT PRIMARY KEY,
	state TEXT NOT NULL,
	attempts INTEGER NOT NULL DEFAULT 0,
	updated_unix INTEGER NOT NULL
);`

// ensureSourceOrigin adds the origin columns to a database created before they existed.
func ensureSourceOrigin(db *sql.DB) error {
	for _, col := range []string{"origin", "origin_note"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('tgsources') WHERE name=?`, col).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		def := "''"
		if col == "origin" {
			def = "'" + OriginManual + "'"
		}
		if _, err := db.Exec(`ALTER TABLE tgsources ADD COLUMN ` + col + ` TEXT NOT NULL DEFAULT ` + def); err != nil {
			return err
		}
	}
	return nil
}

// Trust is the standing of a channel or a signer key.
type Trust struct {
	Points int  `json:"points"`
	Fails  int  `json:"fails"`
	Denied bool `json:"denied"`
}

// GetTrust returns the counters of one subject. A subject never seen has zero counters.
func (c *Catalog) GetTrust(ctx context.Context, kind, id string) (Trust, error) {
	var t Trust
	var den int
	err := c.db.QueryRowContext(ctx, `SELECT points, fails, denied FROM tgtrust WHERE kind=? AND id=?`, kind, id).Scan(&t.Points, &t.Fails, &den)
	if errors.Is(err, sql.ErrNoRows) {
		return Trust{}, nil
	}
	t.Denied = den != 0
	return t, err
}

// AllTrust returns the counters of every subject of one kind.
func (c *Catalog) AllTrust(ctx context.Context, kind string) (map[string]Trust, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT id, points, fails, denied FROM tgtrust WHERE kind=?`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Trust{}
	for rows.Next() {
		var id string
		var t Trust
		var den int
		if err := rows.Scan(&id, &t.Points, &t.Fails, &den); err != nil {
			return nil, err
		}
		t.Denied = den != 0
		out[id] = t
	}
	return out, rows.Err()
}

func (c *Catalog) bumpTrust(ctx context.Context, kind, id string, points, fails int) (Trust, error) {
	_, err := c.db.ExecContext(ctx, `INSERT INTO tgtrust (kind, id, points, fails, updated_unix) VALUES (?,?,?,?,?)
		ON CONFLICT(kind, id) DO UPDATE SET points=points+excluded.points, fails=fails+excluded.fails,
			updated_unix=excluded.updated_unix`, kind, id, points, fails, time.Now().Unix())
	if err != nil {
		return Trust{}, err
	}
	return c.GetTrust(ctx, kind, id)
}

// AddTrustPoint gives one accepted file to a subject and returns its counters.
func (c *Catalog) AddTrustPoint(ctx context.Context, kind, id string) (Trust, error) {
	return c.bumpTrust(ctx, kind, id, 1, 0)
}

// AddTrustFail records a file of this subject that failed verification.
func (c *Catalog) AddTrustFail(ctx context.Context, kind, id string) (Trust, error) {
	return c.bumpTrust(ctx, kind, id, 0, 1)
}

// SetTrustDenied puts a subject on the denylist, or takes it off.
func (c *Catalog) SetTrustDenied(ctx context.Context, kind, id string, denied bool) error {
	_, err := c.db.ExecContext(ctx, `INSERT INTO tgtrust (kind, id, denied, updated_unix) VALUES (?,?,?,?)
		ON CONFLICT(kind, id) DO UPDATE SET denied=excluded.denied, updated_unix=excluded.updated_unix`,
		kind, id, b2i(denied), time.Now().Unix())
	return err
}

// Seen states of a searched file.
const (
	SeenDone   = "done"   // copied, or already in the library
	SeenReject = "reject" // failed verification, never retried
	SeenTried  = "tried"  // a copy failed; retried until the attempt limit
)

// SeenTag returns the state and attempt count of a searched tag, or "" when it was never handled.
func (c *Catalog) SeenTag(ctx context.Context, tag string) (state string, attempts int, err error) {
	err = c.db.QueryRowContext(ctx, `SELECT state, attempts FROM tgsearchseen WHERE tag=?`, tag).Scan(&state, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	return state, attempts, err
}

// MarkSeenTag stores the outcome of a searched tag. An attempt is counted each time.
func (c *Catalog) MarkSeenTag(ctx context.Context, tag, state string) error {
	_, err := c.db.ExecContext(ctx, `INSERT INTO tgsearchseen (tag, state, attempts, updated_unix) VALUES (?,?,1,?)
		ON CONFLICT(tag) DO UPDATE SET state=excluded.state, attempts=attempts+1, updated_unix=excluded.updated_unix`,
		tag, state, time.Now().Unix())
	return err
}

// DiscoveredCount is how many source channels the search promoted.
func (c *Catalog) DiscoveredCount(ctx context.Context) (int, error) {
	var n int
	err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tgsources WHERE origin=?`, OriginDiscovered).Scan(&n)
	return n, err
}

// AddDiscoveredSource stores a channel the search promoted. A channel that is already a source is
// left as it is and reported with added=false.
func (c *Catalog) AddDiscoveredSource(ctx context.Context, s Source, note string) (Source, bool, error) {
	res, err := c.db.ExecContext(ctx, `
		INSERT INTO tgsources (channel_id, username, title, no_forwards, added_unix, origin, origin_note) VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(channel_id) DO NOTHING`,
		s.ChannelID, s.Username, s.Title, b2i(s.NoForwards), time.Now().Unix(), OriginDiscovered, note)
	if err != nil {
		return Source{}, false, err
	}
	n, _ := res.RowsAffected()
	src, err := c.sourceByChannel(ctx, s.ChannelID)
	return src, n > 0, err
}

// SourceByChannel returns the source for a channel id, and false when there is none.
func (c *Catalog) SourceByChannel(ctx context.Context, channelID int64) (Source, bool, error) {
	s, err := c.sourceByChannel(ctx, channelID)
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, false, nil
	}
	return s, err == nil, err
}
