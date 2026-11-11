// Package home is the personal library. A person posts in its channel, or syncs folders from
// this computer. News channels and other channels never write there.
package home

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/hold"
	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/localmedia"
	"github.com/Aryan-mor/jellyfin-telegram/internal/setup"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// EnvChannel and EnvDirs are the settings-file keys.
const (
	EnvChannel = "HOME_CHANNEL_ID"
	EnvDirs    = "HOME_MEDIA_DIRS"
)

// Sender posts into the personal channel.
type Sender interface {
	Healthy() []tguser.View
	LargestCap() int64
	UploadTo(ctx context.Context, userID, channelID int64, path, name, caption string, progress func(done, total int64)) (int, error)
}

// Service lists personal files on this computer and sends the ones a person chooses.
type Service struct {
	Channel int64
	Cat     *catalog.Catalog
	Sender  Sender
	EnvPath string
	Exclude []string
	Log     *slog.Logger

	mu       sync.Mutex
	dirs     []string
	files    []File
	folders  []localmedia.Folder
	scanned  time.Time
	scanning bool
	wake     chan struct{}
	namedID  int64
	named    string
	namedAt  time.Time
}

// channelNamer resolves a channel the signed-in account already belongs to.
type channelNamer interface {
	Lookup(ctx context.Context, input string) (tguser.Channel, error)
}

// New returns a service for these folders.
func New(dirs []string) *Service {
	return &Service{dirs: append([]string(nil), dirs...), wake: make(chan struct{}, 1)}
}

// Run uploads queued files until ctx ends. It never queues a file by itself.
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	s.scan(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if hold.Stopped() {
				continue
			}
			work, done := hold.Begin(ctx)
			s.step(work)
			done()
		case <-s.wake:
			s.step(ctx)
		case <-hold.Changed():
		}
	}
}

func (s *Service) step(ctx context.Context) {
	if s.Cat == nil || s.Sender == nil || s.Channel == 0 {
		return
	}
	rows, err := s.Cat.ListUploads(ctx)
	if err != nil {
		s.warn(err)
		return
	}
	healthy := s.Sender.Healthy()
	if len(healthy) == 0 {
		return
	}
	user := healthy[0].UserID
	for _, row := range rows {
		if row.Kind != catalog.KindHome {
			continue
		}
		if row.State != catalog.UploadQueued && row.State != catalog.UploadFailed && row.State != catalog.UploadUploading {
			continue
		}
		if err := s.Cat.SetUpload(ctx, row.ID, catalog.UploadUploading, row.MessageID, user, ""); err != nil {
			s.warn(err)
			return
		}
		id, err := s.Sender.UploadTo(ctx, user, s.Channel, row.Path, row.FileName, row.Caption, nil)
		if err != nil {
			_ = s.Cat.SetUpload(ctx, row.ID, catalog.UploadFailed, 0, user, err.Error())
			s.warn(err)
			return
		}
		if err := s.Cat.SetUpload(ctx, row.ID, catalog.UploadProbed, id, user, ""); err != nil {
			s.warn(err)
			return
		}
		return
	}
}

func (s *Service) warn(err error) {
	if s.Log != nil && err != nil {
		s.Log.Warn("home upload", "err", err)
	}
}

// AddFolder remembers a folder and scans it.
func (s *Service) AddFolder(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("type the full path of a folder")
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return errors.New("that folder does not exist on this computer")
	}
	for _, skip := range s.Exclude {
		if skip != "" && localmedia.Within(skip, abs) {
			return fmt.Errorf("%s holds the Telegram library or the downloads, which are handled already", skip)
		}
	}
	s.mu.Lock()
	for _, d := range s.dirs {
		if d == abs {
			s.mu.Unlock()
			return errors.New("that folder is already in the list")
		}
	}
	next := append(append([]string(nil), s.dirs...), abs)
	s.mu.Unlock()
	if err := s.persist(next); err != nil {
		return err
	}
	s.mu.Lock()
	s.dirs = next
	s.mu.Unlock()
	s.kick()
	return nil
}

// RemoveFolder stops scanning a folder. Files already sent stay in the channel.
func (s *Service) RemoveFolder(raw string) error {
	abs, _ := filepath.Abs(raw)
	s.mu.Lock()
	var next []string
	found := false
	for _, d := range s.dirs {
		if d == raw || d == abs {
			found = true
			continue
		}
		next = append(next, d)
	}
	s.mu.Unlock()
	if !found {
		return errors.New("that folder is not in the list")
	}
	if err := s.persist(next); err != nil {
		return err
	}
	s.mu.Lock()
	s.dirs = next
	s.mu.Unlock()
	s.kick()
	return nil
}

func (s *Service) persist(dirs []string) error {
	if s.EnvPath == "" {
		return nil
	}
	return setup.UpdateEnv(s.EnvPath, map[string]string{EnvDirs: strings.Join(dirs, "|")})
}

func (s *Service) kick() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
	go s.scan(context.Background())
}

// Sync queues every file that is not already on its way. Nothing is sent until Run picks it up.
func (s *Service) Sync(ctx context.Context) (int, error) {
	if s.Channel == 0 {
		return 0, errors.New("set the personal channel first, then restart")
	}
	if s.Sender == nil || len(s.Sender.Healthy()) == 0 {
		return 0, errors.New("sign in a Telegram account that can post in the personal channel")
	}
	if s.Cat == nil {
		return 0, errors.New("the catalog is not open")
	}
	s.scan(ctx)
	rows, err := s.rows(ctx)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	files := append([]File(nil), s.files...)
	s.mu.Unlock()
	n := 0
	limit := s.Sender.LargestCap()
	for _, f := range files {
		if int64(int(f.ID)) != f.ID {
			continue
		}
		if row, ok := rows[f.ID]; ok && row.State != catalog.UploadFailed && row.State != catalog.UploadTooBig {
			continue
		}
		got, err := s.Cat.UpsertUpload(ctx, catalog.Upload{
			Kind: catalog.KindHome, RemoteID: int(f.ID), OwnerID: int(f.Mod.Unix()),
			Path: f.Path, Size: f.Size, FileName: f.Name, Caption: f.Name, State: catalog.UploadQueued,
		})
		if err != nil {
			return n, err
		}
		state, note := catalog.UploadQueued, ""
		if limit > 0 && f.Size > limit {
			state, note = catalog.UploadTooBig, "larger than every signed-in account can send"
		}
		if err := s.Cat.SetUpload(ctx, got.ID, state, 0, 0, note); err != nil {
			return n, err
		}
		if state == catalog.UploadQueued {
			n++
		}
	}
	s.kick()
	return n, nil
}

func (s *Service) rows(ctx context.Context) (map[int64]catalog.Upload, error) {
	all, err := s.Cat.ListUploads(ctx)
	if err != nil {
		return nil, err
	}
	out := map[int64]catalog.Upload{}
	for _, r := range all {
		if r.Kind == catalog.KindHome {
			out[int64(r.RemoteID)] = r
		}
	}
	return out, nil
}

// File is one video or photo on disk.
type File struct {
	ID   int64     `json:"id"`
	Path string    `json:"-"`
	Name string    `json:"name"`
	Size int64     `json:"size"`
	Mod  time.Time `json:"-"`
	Kind string    `json:"kind"`
}

// View is what the Home tab shows.
type View struct {
	Channel  int64               `json:"channel"`
	Title    string              `json:"title,omitempty"`
	Running  int64               `json:"running"`
	Folders  []localmedia.Folder `json:"folders"`
	Files    []FileView          `json:"files"`
	Scanned  int64               `json:"scanned"`
	Scanning bool                `json:"scanning"`
	Problem  string              `json:"problem,omitempty"`
}

// FileView adds the upload state.
type FileView struct {
	File
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

// State builds the tab.
func (s *Service) State(ctx context.Context) (View, error) {
	v := View{Channel: s.fileChannel(), Running: s.Channel, Folders: []localmedia.Folder{}, Files: []FileView{}}
	if v.Channel == 0 {
		v.Problem = "No personal channel yet. Set one, make every bot an admin of it, and restart."
	} else if v.Channel != s.Channel {
		v.Problem = "Restart jellyfin-telegram to use this channel."
	} else if s.Sender == nil || len(s.Sender.Healthy()) == 0 {
		v.Problem = "Sign in a Telegram account that can post in the personal channel."
	}
	var rows map[int64]catalog.Upload
	if s.Cat != nil {
		var err error
		rows, err = s.rows(ctx)
		if err != nil {
			return View{}, err
		}
	}
	v.Title = s.channelTitle(ctx, v.Channel)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.scanned.IsZero() {
		v.Scanned = s.scanned.Unix()
	}
	v.Scanning = s.scanning
	if s.folders != nil {
		v.Folders = append([]localmedia.Folder(nil), s.folders...)
	}
	for _, f := range s.files {
		fv := FileView{File: f, Status: "local"}
		if row, ok := rows[f.ID]; ok {
			switch row.State {
			case catalog.UploadProbed, catalog.UploadIndexed, catalog.UploadVerified, catalog.UploadSent:
				fv.Status = "synced"
			case catalog.UploadQueued, catalog.UploadUploading:
				fv.Status = "uploading"
			case catalog.UploadFailed, catalog.UploadTooBig, catalog.UploadMismatch:
				fv.Status = "problem"
				fv.Note = row.Error
			}
		}
		v.Files = append(v.Files, fv)
	}
	return v, nil
}

// channelTitle is the Telegram name of the personal channel. A miss waits a minute before asking again.
func (s *Service) channelTitle(ctx context.Context, id int64) string {
	if id == 0 || s.Sender == nil {
		return ""
	}
	s.mu.Lock()
	if s.namedID == id && (s.named != "" || time.Since(s.namedAt) < time.Minute) {
		name := s.named
		s.mu.Unlock()
		return name
	}
	s.mu.Unlock()
	namer, ok := s.Sender.(channelNamer)
	if !ok {
		return ""
	}
	ch, err := namer.Lookup(ctx, strconv.FormatInt(id, 10))
	s.mu.Lock()
	s.namedID = id
	s.namedAt = time.Now()
	if err == nil {
		s.named = ch.Title
	} else {
		s.named = ""
	}
	name := s.named
	s.mu.Unlock()
	return name
}

func (s *Service) fileChannel() int64 {
	if s.EnvPath == "" {
		return s.Channel
	}
	m, err := godotenv.Read(s.EnvPath)
	if err != nil {
		return s.Channel
	}
	raw := strings.TrimSpace(m[EnvChannel])
	if raw == "" {
		return 0
	}
	id, err := config.ParseChannelID(raw)
	if err != nil {
		return s.Channel
	}
	return id
}
