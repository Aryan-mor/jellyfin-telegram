package tguser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"sync"

	"github.com/gotd/td/session"
)

// record is one account on disk. The session bytes are the gotd session.
// The file is not encrypted. Treat DATA_DIR like a password.
type record struct {
	Session string `json:"session,omitempty"`
	Meta    Meta   `json:"meta"`
}

// Meta is what the panel and the uploader need, apart from the Telegram session.
type Meta struct {
	UserID     int64  `json:"user_id"`
	Phone      string `json:"phone,omitempty"`
	Username   string `json:"username,omitempty"`
	Premium    bool   `json:"premium"`
	Admin      bool   `json:"admin"`
	AccessHash int64  `json:"access_hash,omitempty"`
	ChannelID  int64  `json:"channel_id,omitempty"`
	Error      string `json:"error,omitempty"`

	// Pace overrides. A nil pointer uses the service default.
	// A stored 0 for MB/s means no speed cap.
	MBPerSec        *int    `json:"mb_per_sec,omitempty"`
	RestMinutes     *int    `json:"rest_minutes,omitempty"`
	LongEveryGB     *int    `json:"long_every_gb,omitempty"`
	LongRestMinutes *int    `json:"long_rest_minutes,omitempty"`
	DailyGB         *int    `json:"daily_gb,omitempty"`
	Hours           *string `json:"hours,omitempty"`

	SentDay   string `json:"sent_day,omitempty"`
	SentBytes int64  `json:"sent_bytes,omitempty"`
	SinceLong int64  `json:"since_long,omitempty"`
	RestUntil int64  `json:"rest_until,omitempty"`
	FloodMul  int    `json:"flood_mul,omitempty"`
	FloodDay  string `json:"flood_day,omitempty"`
}

// fileStore is a gotd session.Storage that keeps our meta in the same file.
type fileStore struct {
	path string
	mu   sync.Mutex
}

func (s *fileStore) LoadSession(context.Context) ([]byte, error) {
	rec, err := s.read()
	if errors.Is(err, os.ErrNotExist) {
		return nil, session.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if rec.Session == "" {
		return nil, session.ErrNotFound
	}
	return base64.StdEncoding.DecodeString(rec.Session)
}

func (s *fileStore) StoreSession(_ context.Context, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.readUnlocked()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	rec.Session = base64.StdEncoding.EncodeToString(data)
	return s.writeUnlocked(rec)
}

func (s *fileStore) read() (record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readUnlocked()
}

func (s *fileStore) readUnlocked() (record, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return record{}, err
	}
	var rec record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return record{}, err
	}
	return rec, nil
}

func (s *fileStore) writeMeta(meta Meta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.readUnlocked()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	rec.Meta = meta
	return s.writeUnlocked(rec)
}

func (s *fileStore) writeUnlocked(rec record) error {
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
