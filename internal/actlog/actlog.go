// Package actlog records what each Telegram account and bot did, for the panel.
package actlog

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // database driver
)

const keepRows = 8000

// KindAll lists every actor's actions together instead of one actor's.
const KindAll = "all"

const schema = `
CREATE TABLE IF NOT EXISTS activity (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	at_unix  INTEGER NOT NULL,
	kind     TEXT NOT NULL,
	actor_id INTEGER NOT NULL,
	name     TEXT NOT NULL,
	action   TEXT NOT NULL,
	detail   TEXT NOT NULL,
	ok       INTEGER NOT NULL,
	error    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS activity_actor ON activity(kind, actor_id, id);
`

// Journal is the writer accounts and bots call. A nil store is not a Journal.
type Journal interface {
	Note(kind string, actorID int64, name, action, detail string, err error)
}

// Entry is one recorded action.
type Entry struct {
	ID      int64  `json:"id"`
	At      int64  `json:"at"`
	Kind    string `json:"kind"`
	ActorID int64  `json:"actor_id"`
	Name    string `json:"name"`
	Action  string `json:"action"`
	Detail  string `json:"detail"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

// Store is the SQLite log.
type Store struct{ db *sql.DB }

// Open creates or opens the log at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Note appends one action. A write failure is ignored so the work itself still finishes.
func (s *Store) Note(kind string, actorID int64, name, action, detail string, err error) {
	if s == nil || s.db == nil || actorID == 0 || action == "" {
		return
	}
	if kind != "account" && kind != "bot" {
		return
	}
	msg := ""
	ok := 1
	if err != nil {
		ok = 0
		msg = clip(err.Error(), 300)
	}
	_, _ = s.db.Exec(`
		INSERT INTO activity (at_unix, kind, actor_id, name, action, detail, ok, error)
		VALUES (?,?,?,?,?,?,?,?)`,
		time.Now().Unix(), kind, actorID, clip(name, 80), clip(action, 80), clip(oneLine(detail), 300), ok, msg)
	_, _ = s.db.Exec(`
		DELETE FROM activity WHERE id <= COALESCE((
			SELECT id FROM activity ORDER BY id DESC LIMIT 1 OFFSET ?
		), 0)`, keepRows)
}

// List returns one page of an actor's actions, newest first.
func (s *Store) List(kind string, actorID int64, page, limit int) ([]Entry, int, error) {
	if s == nil || s.db == nil {
		return []Entry{}, 0, nil
	}
	if limit < 1 {
		limit = 40
	}
	if limit > 100 {
		limit = 100
	}
	if page < 1 {
		page = 1
	}
	// Kind "all" is the live log: every account and bot together, newest first.
	where, args := `kind=? AND actor_id=?`, []any{kind, actorID}
	if kind == KindAll {
		where, args = `1=1`, nil
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM activity WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(`
		SELECT id, at_unix, kind, actor_id, name, action, detail, ok, error
		FROM activity WHERE `+where+`
		ORDER BY id DESC LIMIT ? OFFSET ?`, append(args, limit, (page-1)*limit)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var ok int
		if err := rows.Scan(&e.ID, &e.At, &e.Kind, &e.ActorID, &e.Name, &e.Action, &e.Detail, &ok, &e.Error); err != nil {
			return nil, 0, err
		}
		e.OK = ok == 1
		out = append(out, e)
	}
	return out, total, rows.Err()
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
