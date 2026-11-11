package localmedia

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/autoupload"
	"github.com/Aryan-mor/jellyfin-telegram/internal/hold"
	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/setup"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// Accounts is the set of signed-in Telegram accounts that can post to the channel.
type Accounts interface {
	Healthy() []tguser.View
	LargestCap() int64
}

// EnvKey is the settings-file key that holds the folders, separated by a vertical bar.
const EnvKey = "LOCAL_MEDIA_DIRS"

// Status says where one file stands. It is what the panel shows and what its buttons act on.
const (
	StatusLocal     = "local"     // only on this computer
	StatusWaiting   = "waiting"   // chosen, and waiting for an account or its rest to end
	StatusUploading = "uploading" // being sent now
	StatusChecking  = "checking"  // in the channel, not yet checked and read by Jellyfin
	StatusSynced    = "synced"    // in the channel and played from there: the local copy can go
	StatusProblem   = "problem"   // needs a person to look
)

// Service lists the local videos, sends the ones a person chooses, and deletes the local copy of
// the ones the channel already holds. It never uploads or deletes anything by itself.
type Service struct {
	Cat *catalog.Catalog
	// Worker must have Local set. Its Sessions are the accounts that post.
	Worker   *autoupload.Worker
	Accounts Accounts
	EnvPath  string
	// Exclude lists folders that are never scanned: the Telegram library of .strm files, and the
	// folder the download stack imports into.
	Exclude   []string
	Log       *slog.Logger
	ScanEvery time.Duration

	mu        sync.Mutex
	dirs      []string
	files     []File
	folders   []Folder
	scannedAt time.Time
	scanning  bool

	wake   chan struct{}
	rescan chan struct{}
}

// New returns a service for these folders.
func New(dirs []string) *Service {
	return &Service{dirs: append([]string(nil), dirs...), wake: make(chan struct{}, 1), rescan: make(chan struct{}, 1)}
}

// Run scans the folders and moves chosen files forward until ctx ends.
func (s *Service) Run(ctx context.Context) {
	if s.Worker != nil {
		s.Worker.Ctx = ctx
	}
	every := s.ScanEvery
	if every <= 0 {
		every = 10 * time.Minute
	}
	pass := time.NewTicker(20 * time.Second)
	defer pass.Stop()
	again := time.NewTicker(every)
	defer again.Stop()
	s.scan(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-pass.C:
			// The timer is automatic. A button still wakes the queue below.
			if hold.Stopped() {
				continue
			}
			work, done := hold.Begin(ctx)
			s.step(work)
			done()
		case <-s.wake:
			s.step(ctx)
		case <-hold.Changed():
		case <-again.C:
			s.scan(ctx)
		case <-s.rescan:
			s.scan(ctx)
		}
	}
}

func (s *Service) step(ctx context.Context) {
	if s.Worker == nil {
		return
	}
	if err := s.Worker.RunOnce(ctx); err != nil && ctx.Err() == nil && s.Log != nil {
		s.Log.Warn("local uploads", "err", err)
	}
}

func (s *Service) kick(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// Scan reads the folders again. A scan that is already running is left to finish.
func (s *Service) Scan(ctx context.Context) { s.scan(ctx) }

func (s *Service) scan(ctx context.Context) {
	s.mu.Lock()
	if s.scanning {
		s.mu.Unlock()
		return
	}
	s.scanning = true
	dirs := append([]string(nil), s.dirs...)
	s.mu.Unlock()

	files, folders := Scan(ctx, dirs, s.Exclude)

	s.mu.Lock()
	s.scanning = false
	if ctx.Err() == nil {
		s.files, s.folders, s.scannedAt = files, folders, time.Now()
	}
	s.mu.Unlock()
}

// Dirs returns the folders being scanned.
func (s *Service) Dirs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.dirs...)
}

// AddFolder starts scanning a folder and remembers it in the settings file.
func (s *Service) AddFolder(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("type the full path of a folder")
	}
	if strings.ContainsAny(raw, "|\"'`\r\n") || strings.Contains(raw, " #") {
		return errors.New("that path has a character the settings file cannot hold: | \" ' ` or a space before #")
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
		if skip != "" && Within(skip, abs) {
			return fmt.Errorf("%s holds the Telegram library or the downloads, which are handled already", skip)
		}
	}
	s.mu.Lock()
	for _, d := range s.dirs {
		if sameDir(d, abs) {
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
	s.kick(s.rescan)
	return nil
}

// RemoveFolder stops scanning a folder. Files in it are not touched, and rows already sent stay.
func (s *Service) RemoveFolder(raw string) error {
	s.mu.Lock()
	var next []string
	found := false
	for _, d := range s.dirs {
		if sameDir(d, raw) {
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
	// Its files leave the list right away instead of at the next scan.
	var keep []File
	for _, f := range s.files {
		if !sameDir(f.Root, raw) {
			keep = append(keep, f)
		}
	}
	s.files = keep
	s.mu.Unlock()
	s.kick(s.rescan)
	return nil
}

func (s *Service) persist(dirs []string) error {
	if s.EnvPath == "" {
		return nil
	}
	return setup.UpdateEnv(s.EnvPath, map[string]string{EnvKey: strings.Join(dirs, "|")})
}

func sameDir(a, b string) bool {
	x, err1 := filepath.Abs(a)
	y, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return a == b
	}
	return Within(x, y) && Within(y, x)
}

// FileView is one file as the panel shows it.
type FileView struct {
	File
	Status string `json:"status"`
	// State is the upload row's own state, such as failed or mismatch, for the buttons to choose by.
	State    string `json:"state,omitempty"`
	Note     string `json:"note,omitempty"`
	Progress int64  `json:"progress,omitempty"`
	Total    int64  `json:"total,omitempty"`
}

// Group is one film, or one series, with its files.
type Group struct {
	Title string     `json:"title"`
	Year  int        `json:"year,omitempty"`
	Bytes int64      `json:"bytes"`
	Files []FileView `json:"files"`
}

// Category is Movies, Shows or Other.
type Category struct {
	ID     string  `json:"id"`
	Label  string  `json:"label"`
	Count  int     `json:"count"`
	Bytes  int64   `json:"bytes"`
	Groups []Group `json:"groups"`
}

// Total counts files.
type Total struct {
	Count int   `json:"count"`
	Bytes int64 `json:"bytes"`
}

// Account is a signed-in account as the local tab needs it.
type Account struct {
	Name     string `json:"name"`
	Busy     bool   `json:"busy"`
	Progress int64  `json:"progress,omitempty"`
	Total    int64  `json:"total,omitempty"`
}

// View is everything the Local files tab shows.
type View struct {
	Folders    []Folder         `json:"folders"`
	Scanned    int64            `json:"scanned"`
	Scanning   bool             `json:"scanning"`
	Categories []Category       `json:"categories"`
	Totals     map[string]Total `json:"totals"`
	Accounts   []Account        `json:"accounts"`
	Cap        int64            `json:"cap"`
	Problem    string           `json:"problem,omitempty"`
}

// stale rows are ignored: a row belongs to a file only while the file has the same size and time.
func matches(row catalog.Upload, f File) bool {
	return row.Size == f.Size && int64(row.OwnerID) == f.ModTime.Unix()
}

func active(state string) bool {
	switch state {
	case catalog.UploadUploading, catalog.UploadSent, catalog.UploadVerified, catalog.UploadIndexed:
		return true
	}
	return false
}

func (s *Service) rows(ctx context.Context) (map[int64]catalog.Upload, error) {
	if s.Cat == nil {
		return nil, nil
	}
	all, err := s.Cat.ListUploads(ctx)
	if err != nil {
		return nil, err
	}
	out := map[int64]catalog.Upload{}
	for _, r := range all {
		if r.Kind == catalog.KindLocal {
			out[int64(r.RemoteID)] = r
		}
	}
	return out, nil
}

// State builds the tab from the last scan and the upload rows.
func (s *Service) State(ctx context.Context) (View, error) {
	rows, err := s.rows(ctx)
	if err != nil {
		return View{}, err
	}
	s.mu.Lock()
	files := append([]File(nil), s.files...)
	folders := append([]Folder(nil), s.folders...)
	dirs := append([]string(nil), s.dirs...)
	v := View{Scanned: s.scannedAt.Unix(), Scanning: s.scanning, Totals: map[string]Total{}}
	if s.scannedAt.IsZero() {
		v.Scanned = 0
	}
	s.mu.Unlock()

	for _, d := range dirs {
		got := Folder{Path: d}
		for _, f := range folders {
			if f.Path == d {
				got = f
			}
		}
		v.Folders = append(v.Folders, got)
	}
	if v.Folders == nil {
		v.Folders = []Folder{}
	}

	views := map[int64]tguser.View{}
	if s.Accounts != nil {
		v.Cap = s.Accounts.LargestCap()
		for _, a := range s.Accounts.Healthy() {
			views[a.UserID] = a
			name := "@" + a.Username
			if a.Username == "" {
				name = fmt.Sprintf("user %d", a.UserID)
			}
			v.Accounts = append(v.Accounts, Account{Name: name, Busy: a.Busy, Progress: a.Progress, Total: a.Total})
		}
	}
	if v.Accounts == nil {
		v.Accounts = []Account{}
	}

	cats := map[string]*Category{
		KindMovie:   {ID: "movies", Label: "Movies", Groups: []Group{}},
		KindEpisode: {ID: "shows", Label: "Shows", Groups: []Group{}},
		KindOther:   {ID: "other", Label: "Other videos", Groups: []Group{}},
	}
	type key struct {
		kind, title string
		year        int
	}
	at := map[key]int{}
	for _, f := range files {
		row, has := rows[f.ID]
		fv := FileView{File: f}
		fv.Status, fv.Note = status(row, has, f)
		if fv.Status != StatusLocal {
			fv.State = row.State
		}
		if fv.Status == StatusUploading {
			if a, ok := views[row.SessionUser]; ok && a.Busy {
				fv.Progress, fv.Total = a.Progress, a.Total
			}
		}
		cat := cats[f.Kind]
		k := key{f.Kind, strings.ToLower(f.Title), f.Year}
		if f.Kind == KindOther {
			k = key{f.Kind, strings.ToLower(f.Rel), 0}
		}
		i, ok := at[k]
		if !ok {
			cat.Groups = append(cat.Groups, Group{Title: f.Title, Year: f.Year, Files: []FileView{}})
			i = len(cat.Groups) - 1
			at[k] = i
			if f.Kind == KindOther {
				cat.Groups[i].Title = f.Name
			}
		}
		g := &cat.Groups[i]
		g.Files = append(g.Files, fv)
		g.Bytes += f.Size
		cat.Count++
		cat.Bytes += f.Size

		bucket := fv.Status
		switch bucket {
		case StatusWaiting, StatusUploading, StatusChecking:
			bucket = "busy"
		}
		t := v.Totals[bucket]
		t.Count++
		t.Bytes += f.Size
		v.Totals[bucket] = t
	}
	for _, kind := range []string{KindMovie, KindEpisode, KindOther} {
		v.Categories = append(v.Categories, *cats[kind])
	}
	for _, name := range []string{StatusLocal, "busy", StatusSynced, StatusProblem} {
		if _, ok := v.Totals[name]; !ok {
			v.Totals[name] = Total{}
		}
	}
	if s.Accounts == nil {
		v.Problem = "Telegram accounts are not available, so nothing can be uploaded."
	} else if len(v.Accounts) == 0 {
		v.Problem = "No Telegram account is signed in. Sign in with an account that is an admin of the channel."
	}
	return v, nil
}

func status(row catalog.Upload, has bool, f File) (string, string) {
	if !has {
		return StatusLocal, ""
	}
	if !active(row.State) && !matches(row, f) {
		return StatusLocal, ""
	}
	switch row.State {
	case catalog.UploadQueued:
		return StatusWaiting, strings.TrimPrefix(row.Error, "waiting: ")
	case catalog.UploadUploading:
		return StatusUploading, strings.TrimPrefix(row.Error, "waiting: ")
	case catalog.UploadSent, catalog.UploadVerified, catalog.UploadIndexed:
		return StatusChecking, ""
	case catalog.UploadProbed:
		return StatusSynced, ""
	case catalog.UploadTooBig:
		return StatusProblem, firstText(row.Error, "larger than every signed-in account can send")
	case catalog.UploadFailed:
		return StatusProblem, firstText(row.Error, "the upload failed, and will be tried again") + " It will be tried again."
	case catalog.UploadMismatch:
		return StatusProblem, firstText(row.Error, "the channel holds other bytes than this file")
	}
	return StatusLocal, ""
}

func firstText(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (s *Service) byID() map[int64]File {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[int64]File, len(s.files))
	for _, f := range s.files {
		out[f.ID] = f
	}
	return out
}

// Result is what an action did.
type Result struct {
	Done    int      `json:"done"`
	Skipped int      `json:"skipped"`
	Bytes   int64    `json:"bytes"`
	Errors  []string `json:"errors,omitempty"`
}

// Queue marks files to be sent. A file already sent, or on its way, is skipped.
func (s *Service) Queue(ctx context.Context, ids []int64) (Result, error) {
	var res Result
	if s.Accounts == nil || s.Cat == nil {
		return res, errors.New("Telegram accounts are not available, so nothing can be uploaded")
	}
	if len(s.Accounts.Healthy()) == 0 {
		return res, errors.New("sign in a Telegram account that is an admin of the channel first")
	}
	limit := s.Accounts.LargestCap()
	files := s.byID()
	rows, err := s.rows(ctx)
	if err != nil {
		return res, err
	}
	for _, id := range ids {
		f, ok := files[id]
		if !ok || int64(int(id)) != id {
			res.Skipped++
			continue
		}
		row, has := rows[id]
		if has {
			if active(row.State) {
				res.Skipped++
				continue
			}
			if matches(row, f) {
				switch row.State {
				case catalog.UploadQueued, catalog.UploadFailed, catalog.UploadTooBig, catalog.UploadProbed:
					res.Skipped++
					continue
				}
			}
		}
		got, err := s.Cat.UpsertUpload(ctx, catalog.Upload{
			Kind: catalog.KindLocal, RemoteID: int(id), OwnerID: int(f.ModTime.Unix()),
			Path: f.Path, Size: f.Size, FileName: f.UploadName, Caption: f.Caption, State: catalog.UploadQueued,
		})
		if err != nil {
			return res, err
		}
		state, note := catalog.UploadQueued, ""
		if limit > 0 && f.Size > limit {
			state, note = catalog.UploadTooBig, "larger than every signed-in account can send"
		}
		if err := s.Cat.SetUpload(ctx, got.ID, state, 0, 0, note); err != nil {
			return res, err
		}
		res.Done++
		res.Bytes += f.Size
	}
	s.kick(s.wake)
	return res, nil
}

// Unqueue takes files back out of the line, so they count as local again.
func (s *Service) Unqueue(ctx context.Context, ids []int64) (Result, error) {
	var res Result
	files := s.byID()
	rows, err := s.rows(ctx)
	if err != nil {
		return res, err
	}
	for _, id := range ids {
		row, has := rows[id]
		_, known := files[id]
		if !has || !known {
			res.Skipped++
			continue
		}
		switch row.State {
		case catalog.UploadQueued, catalog.UploadFailed, catalog.UploadTooBig, catalog.UploadMismatch:
			if err := s.Cat.DeleteUpload(ctx, row.ID); err != nil {
				return res, err
			}
			res.Done++
		default:
			res.Skipped++
		}
	}
	return res, nil
}

// Remove deletes the local copy of files the channel already holds. It checks each one again
// right before deleting: the file must be unchanged since it was sent, Jellyfin must have read it from
// the channel, and the channel's file must have the same size.
func (s *Service) Remove(ctx context.Context, ids []int64) (Result, error) {
	var res Result
	files := s.byID()
	rows, err := s.rows(ctx)
	if err != nil {
		return res, err
	}
	dirs := s.Dirs()
	deleted := map[int64]bool{}
	for _, id := range ids {
		f, ok := files[id]
		row, has := rows[id]
		if !ok || !has || row.State != catalog.UploadProbed || !matches(row, f) {
			res.Skipped++
			continue
		}
		if why := s.refuse(ctx, f, row, dirs); why != "" {
			res.Skipped++
			res.Errors = append(res.Errors, f.Name+": "+why)
			continue
		}
		if err := os.Remove(f.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			res.Skipped++
			res.Errors = append(res.Errors, f.Name+": "+err.Error())
			continue
		}
		pruneEmpty(filepath.Dir(f.Path), f.Root)
		deleted[id] = true
		res.Done++
		res.Bytes += f.Size
		if s.Log != nil {
			s.Log.Info("deleted a local video that is in the channel", "file", f.Rel, "message", row.MessageID)
		}
	}
	if len(deleted) > 0 {
		s.mu.Lock()
		keep := s.files[:0:0]
		for _, f := range s.files {
			if !deleted[f.ID] {
				keep = append(keep, f)
			}
		}
		s.files = keep
		s.mu.Unlock()
	}
	return res, nil
}

// RemoveSynced deletes every local file that the channel holds.
func (s *Service) RemoveSynced(ctx context.Context) (Result, error) {
	v, err := s.State(ctx)
	if err != nil {
		return Result{}, err
	}
	var ids []int64
	for _, c := range v.Categories {
		for _, g := range c.Groups {
			for _, f := range g.Files {
				if f.Status == StatusSynced {
					ids = append(ids, f.ID)
				}
			}
		}
	}
	return s.Remove(ctx, ids)
}

// refuse returns why a file must stay, or an empty string when deleting it is safe.
func (s *Service) refuse(ctx context.Context, f File, row catalog.Upload, dirs []string) string {
	inside := false
	for _, d := range dirs {
		if Within(d, f.Path) {
			inside = true
			break
		}
	}
	if !inside {
		return "it is no longer inside a listed folder"
	}
	info, err := os.Stat(f.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ""
		}
		return err.Error()
	}
	if info.Size() != row.Size || info.ModTime().Unix() != int64(row.OwnerID) {
		return "the file changed after it was sent"
	}
	if s.Cat == nil || row.MessageID == 0 {
		return "the channel message is not known"
	}
	got, err := s.Cat.Get(ctx, row.MessageID)
	if err != nil {
		return "the channel no longer lists this file"
	}
	if got.Size != info.Size() {
		return "the channel file has another size"
	}
	return ""
}

// pruneEmpty removes folders that the delete left empty, up to but not including root. A folder
// that still holds anything is kept, because os.Remove refuses it.
func pruneEmpty(dir, root string) {
	for dir != root && Within(root, dir) {
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}
