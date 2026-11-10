// Package newsfeed reads channels that post bot links instead of files.
// Each link is handed to the resolver. A video that passes the same rules as a direct
// copy is forwarded into the library channel, and the indexer picks it up on its next pass.
package newsfeed

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/gotd/td/tgerr"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/naming"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

const pageSize = 100

// Telegram is the signed-in account that reads channels and posts into the library channel.
type Telegram interface {
	Lookup(ctx context.Context, input string) (tguser.Channel, error)
	History(ctx context.Context, ref tguser.SourceRef, offsetID, limit int) (tguser.Page, error)
	HasFreeSession() bool
	HasAccount() bool
	ForwardSaved(ctx context.Context, ids []int) error
}

// Opener turns one bot deep link into the video messages the resolver delivered.
type Opener interface {
	Open(ctx context.Context, url string) ([]tguser.Post, error)
}

// Service walks the news channels. A failure on one link is recorded and the walk continues.
type Service struct {
	Cat *catalog.Catalog
	TG  Telegram
	// LibraryChannel is refused as a source.
	LibraryChannel int64
	Open           Opener
	Log            *slog.Logger
	Sleep          func(ctx context.Context, d time.Duration) error
	Now            func() time.Time
	// InReady reports that the library already has this lookup hashtag. Nil skips the check.
	InReady func(ctx context.Context, tag string) (bool, error)

	mu      sync.Mutex
	loaded  bool
	set     Settings
	wake    chan struct{}
	running bool
	note    string
	last    time.Time
	run     sync.Mutex
	cancel  context.CancelFunc
}

// New returns a stopped service.
func New() *Service { return &Service{wake: make(chan struct{}, 1)} }

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) sleep(ctx context.Context, d time.Duration) error {
	if s.Sleep != nil {
		return s.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Kick asks for a pass as soon as the loop is waiting.
func (s *Service) Kick() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Stop cancels the pass in progress.
func (s *Service) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Run checks the news channels until ctx ends.
func (s *Service) Run(ctx context.Context) {
	for {
		set, err := s.Settings(ctx)
		if err == nil && set.Enabled {
			if s.Pass(ctx) {
				wait := 20 * time.Second
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				case <-s.wake:
				}
				continue
			}
		}
		wait := time.Duration(max(set.EveryMinutes, 5)) * time.Minute
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-s.wake:
		}
	}
}

// Pass walks every enabled news channel once. It returns true when the account was busy, so the
// caller tries again shortly instead of waiting for the normal interval.
func (s *Service) Pass(parent context.Context) bool {
	s.run.Lock()
	defer s.run.Unlock()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	s.mu.Lock()
	s.cancel, s.running = cancel, true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.cancel, s.running = nil, false
		s.last = s.now()
		s.mu.Unlock()
	}()
	set, err := s.Settings(ctx)
	if err != nil || !set.Enabled {
		return false
	}
	ctx = tguser.WithUser(ctx, set.UserID)
	s.setNote("")
	channels, err := s.Cat.NewsChannels(ctx)
	if err != nil {
		s.setNote(err.Error())
		return false
	}
	for _, src := range channels {
		if ctx.Err() != nil || !src.Enabled {
			continue
		}
		if err := s.walk(ctx, src, set); err != nil {
			if s.record(ctx, src, err) {
				return errors.Is(err, tguser.ErrNoFreeSession)
			}
		}
	}
	return false
}

func (s *Service) setNote(text string) {
	s.mu.Lock()
	s.note = text
	s.mu.Unlock()
}

func (s *Service) record(ctx context.Context, src catalog.Source, err error) bool {
	if ctx.Err() != nil {
		return true
	}
	switch {
	case errors.Is(err, tguser.ErrNoFreeSession):
		s.setNote("Waiting for a signed-in account that is free.")
		return true
	case errors.Is(err, errDailyCap):
		s.setNote("The daily limit is reached. News links continue tomorrow.")
		return true
	}
	if _, ok := tgerr.AsFloodWait(err); ok || errors.Is(err, ErrSlowDown) {
		s.setNote("Telegram asked for a pause. News links continue on the next check.")
		return true
	}
	_ = s.Cat.SetNewsError(ctx, src.ID, err.Error())
	if s.Log != nil {
		s.Log.Warn("news channel", "channel", src.Title, "err", err)
	}
	return false
}

var errDailyCap = errors.New("daily limit reached")

func (s *Service) walk(ctx context.Context, src catalog.Source, set Settings) error {
	ref := tguser.SourceRef{ChannelID: src.ChannelID, Username: src.Username}
	w := catalog.SourceWalk{Cursor: src.Cursor, ScanTop: src.ScanTop, Watermark: src.Watermark, CoveredDays: src.CoveredDays}
	if w.Cursor == 0 && !covers(w.CoveredDays, set.LookBackDays) {
		w.Watermark = 0
	}
	var cutoff time.Time
	if set.LookBackDays > 0 {
		cutoff = s.now().AddDate(0, 0, -set.LookBackDays)
	}
	floor := w.Watermark
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := s.TG.History(ctx, ref, w.Cursor, pageSize)
		if err != nil {
			return err
		}
		if w.ScanTop == 0 {
			w.ScanTop = page.Highest
		}
		done := !page.More || page.Lowest == 0 || page.Lowest <= floor
		if !cutoff.IsZero() && !page.Oldest.IsZero() && page.Oldest.Before(cutoff) {
			done = true
		}
		for _, hit := range page.Links {
			if hit.ID <= floor || (!cutoff.IsZero() && hit.Date.Before(cutoff)) {
				continue
			}
			if err := s.one(ctx, src, hit, set); err != nil {
				return err
			}
		}
		if done {
			if w.ScanTop > w.Watermark {
				w.Watermark = w.ScanTop
			}
			w.Cursor, w.ScanTop, w.CoveredDays = 0, 0, set.LookBackDays
			return s.Cat.SaveNewsWalk(ctx, src.ID, w, "")
		}
		w.Cursor = page.Lowest
		if err := s.Cat.SaveNewsWalk(ctx, src.ID, w, ""); err != nil {
			return err
		}
	}
}

func covers(have, want int) bool {
	switch {
	case have < 0:
		return false
	case have == 0:
		return true
	case want == 0:
		return false
	default:
		return have >= want
	}
}

// OpenStart opens one bot link the way a news pass does and returns the videos the resolver
// delivered. A link that was already finished is not opened again.
func (s *Service) OpenStart(ctx context.Context, src catalog.Source, hit tguser.StartHit) ([]tguser.Post, error) {
	if s == nil || s.Cat == nil || s.Open == nil {
		return nil, errors.New("news resolver is not available")
	}
	set, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	ctx = tguser.WithUser(ctx, set.UserID)
	row, ok, err := s.Cat.GetNewsLink(ctx, hit.URL)
	if err != nil {
		return nil, err
	}
	if ok && (row.State == "done" || row.State == "skipped" || row.State == "gave_up") {
		return nil, nil
	}
	if ok && row.State == "fetched" {
		if err := s.forward(ctx, src, row, nil); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if !ok {
		row = catalog.NewsLink{URL: hit.URL, SourceID: src.ID, MessageID: hit.ID, State: "pending"}
	}
	if row.Tries >= 3 {
		row.State = "gave_up"
		return nil, s.Cat.PutNewsLink(ctx, row)
	}
	s.setNote("Opening " + hit.URL)
	posts, err := s.Open.Open(ctx, hit.URL)
	if err != nil {
		if errors.Is(err, ErrSlowDown) {
			row.State, row.Detail = "pending", ""
			if perr := s.Cat.PutNewsLink(ctx, row); perr != nil {
				return nil, perr
			}
			return nil, err
		}
		row.Tries++
		row.Detail = err.Error()
		row.State = "pending"
		if row.Tries >= 3 {
			row.State = "gave_up"
		}
		if perr := s.Cat.PutNewsLink(ctx, row); perr != nil {
			return nil, perr
		}
		s.setNote(err.Error())
		return nil, nil
	}
	return posts, nil
}

// FinishStart forwards the files that matched a request and records the link so it is not opened again.
// An empty list marks the link skipped.
func (s *Service) FinishStart(ctx context.Context, src catalog.Source, hit tguser.StartHit, posts []tguser.Post) error {
	if s == nil || s.Cat == nil {
		return errors.New("news resolver is not available")
	}
	row, ok, err := s.Cat.GetNewsLink(ctx, hit.URL)
	if err != nil {
		return err
	}
	if !ok {
		row = catalog.NewsLink{URL: hit.URL, SourceID: src.ID, MessageID: hit.ID}
	}
	if len(posts) == 0 {
		row.State, row.Detail = "skipped", "no file matched the request"
		return s.Cat.PutNewsLink(ctx, row)
	}
	var ids []int
	for _, p := range posts {
		if p.ID != 0 {
			ids = append(ids, p.ID)
		}
	}
	row.State, row.SavedID, row.Detail = "fetched", ids, ""
	if err := s.Cat.PutNewsLink(ctx, row); err != nil {
		return err
	}
	return s.forward(ctx, src, row, posts)
}

func (s *Service) one(ctx context.Context, src catalog.Source, hit tguser.StartHit, set Settings) error {
	row, ok, err := s.Cat.GetNewsLink(ctx, hit.URL)
	if err != nil {
		return err
	}
	if ok && (row.State == "done" || row.State == "skipped" || row.State == "gave_up") {
		return nil
	}
	if !ok {
		row = catalog.NewsLink{URL: hit.URL, SourceID: src.ID, MessageID: hit.ID, State: "pending"}
	}
	if row.State == "fetched" {
		return s.forward(ctx, src, row, nil)
	}
	if row.Tries >= 3 {
		row.State = "gave_up"
		return s.Cat.PutNewsLink(ctx, row)
	}
	row.State, row.Detail = "pending", "opening"
	if err := s.Cat.PutNewsLink(ctx, row); err != nil {
		return err
	}
	s.setNote("Opening " + hit.URL)
	posts, err := s.Open.Open(ctx, hit.URL)
	if err != nil {
		if errors.Is(err, ErrSlowDown) {
			// Leave the link pending and do not spend a try. The next posts wait
			// on the same account clock instead of each recording their own error.
			row.State, row.Detail = "pending", ""
			if perr := s.Cat.PutNewsLink(ctx, row); perr != nil {
				return perr
			}
			return err
		}
		row.Tries++
		row.Detail = err.Error()
		row.State = "pending"
		if row.Tries >= 3 {
			row.State = "gave_up"
		}
		if perr := s.Cat.PutNewsLink(ctx, row); perr != nil {
			return perr
		}
		s.setNote(err.Error())
		return nil
	}
	if src.Strict {
		set.NeedTitle = true
	}
	var ids []int
	var keep []tguser.Post
	for _, p := range posts {
		if ok, _ := set.rules().Wanted(p); !ok {
			continue
		}
		same, err := s.Cat.CopiedFile(ctx, p.Name, p.Size)
		if err != nil {
			return err
		}
		if same {
			continue
		}
		if tag := naming.BaseTag(naming.LookupTag(naming.Parse(p.Name, p.Caption))); tag != "" && s.InReady != nil {
			yes, err := s.InReady(ctx, tag)
			if err != nil {
				return err
			}
			if yes {
				if err := s.Cat.MarkCopied(ctx, s.now(), p.DocID, src.ID, p.ID, p.Name, p.Size, catalog.CopyPresent, "already in Ready"); err != nil {
					return err
				}
				continue
			}
		}
		ids = append(ids, p.ID)
		keep = append(keep, p)
	}
	_ = s.Cat.AddNewsCounts(ctx, src.ID, 1, 0)
	if len(keep) == 0 {
		row.State, row.Detail = "skipped", "no video passed the rules"
		return s.Cat.PutNewsLink(ctx, row)
	}
	if set.MaxPerDay > 0 {
		day := s.now()
		start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
		have, err := s.Cat.CopyCount(ctx, start)
		if err != nil {
			return err
		}
		if have+len(keep) > set.MaxPerDay {
			return errDailyCap
		}
	}
	row.State, row.SavedID, row.Detail = "fetched", ids, ""
	if err := s.Cat.PutNewsLink(ctx, row); err != nil {
		return err
	}
	if err := s.forward(ctx, src, row, keep); err != nil {
		return err
	}
	if set.PauseSeconds > 0 {
		return s.sleep(ctx, time.Duration(set.PauseSeconds)*time.Second)
	}
	return nil
}

func (s *Service) forward(ctx context.Context, src catalog.Source, row catalog.NewsLink, posts []tguser.Post) error {
	if err := s.TG.ForwardSaved(ctx, row.SavedID); err != nil {
		if _, ok := tgerr.AsFloodWait(err); ok || errors.Is(err, ErrSlowDown) {
			row.Detail = ""
			_ = s.Cat.PutNewsLink(ctx, row)
			return err
		}
		row.Detail = err.Error()
		_ = s.Cat.PutNewsLink(ctx, row)
		return err
	}
	row.State = "done"
	if err := s.Cat.PutNewsLink(ctx, row); err != nil {
		return err
	}
	for _, p := range posts {
		if err := s.Cat.MarkCopied(ctx, s.now(), p.DocID, src.ID, p.ID, p.Name, p.Size, catalog.CopyDone, ""); err != nil {
			return err
		}
	}
	return s.Cat.AddNewsCounts(ctx, src.ID, 0, len(posts))
}

// Add looks a channel up with the account chosen for this tab and stores it.
func (s *Service) Add(ctx context.Context, input string) (catalog.Source, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return catalog.Source{}, err
	}
	ctx = tguser.WithUser(ctx, set.UserID)
	ch, err := s.TG.Lookup(ctx, input)
	if err != nil {
		if errors.Is(err, tguser.ErrNoFreeSession) {
			return catalog.Source{}, errors.New("sign in a Telegram account that is an admin of the library channel first")
		}
		return catalog.Source{}, err
	}
	if ch.ID == s.LibraryChannel {
		return catalog.Source{}, errors.New("that is the library channel itself")
	}
	src, err := s.Cat.AddNews(ctx, catalog.Source{ChannelID: ch.ID, Username: ch.Username, Title: ch.Title, NoForwards: ch.NoForwards})
	if err != nil {
		return catalog.Source{}, err
	}
	s.Kick()
	return src, nil
}

// Remove forgets a news channel.
func (s *Service) Remove(ctx context.Context, id int64) error { return s.Cat.DeleteNews(ctx, id) }

// Pause stops or resumes one news channel.
func (s *Service) Pause(ctx context.Context, id int64, enabled bool) error {
	if err := s.Cat.SetNewsEnabled(ctx, id, enabled); err != nil {
		return err
	}
	s.Kick()
	return nil
}

// Rescan walks the news channels from the top again. Finished links are not opened twice.
func (s *Service) Rescan(ctx context.Context) error {
	if err := s.Cat.RestartNewsWalk(ctx); err != nil {
		return err
	}
	s.Kick()
	return nil
}

// View is what the News channels tab draws.
func (s *Service) View(ctx context.Context) (map[string]any, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	channels, err := s.Cat.NewsChannels(ctx)
	if err != nil {
		return nil, err
	}
	if channels == nil {
		channels = []catalog.Source{}
	}
	s.mu.Lock()
	running, note, last := s.running, s.note, s.last
	s.mu.Unlock()
	var unix int64
	if !last.IsZero() {
		unix = last.Unix()
	}
	recent, err := s.Cat.RecentNews(ctx, 15)
	if err != nil {
		return nil, err
	}
	if recent == nil {
		recent = []catalog.NewsRecent{}
	}
	out := map[string]any{
		"settings": set, "channels": channels, "recent": recent, "running": running, "note": note, "last_pass": unix,
	}
	return out, nil
}

// Settings returns the saved news rules.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settingsLocked(ctx)
}

func (s *Service) settingsLocked(ctx context.Context) (Settings, error) {
	if s.loaded {
		return s.set, nil
	}
	raw, err := s.Cat.StateValue(ctx, settingsKey)
	if err != nil {
		return Defaults(), err
	}
	s.set, s.loaded = decodeSettings(raw), true
	return s.set, nil
}

// Save stores this tab's rules. It does not change the direct-file tab.
func (s *Service) Save(ctx context.Context, next Settings) error {
	if err := next.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	prev, err := s.settingsLocked(ctx)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	raw, _ := json.Marshal(next)
	if err := s.Cat.SetStateValue(ctx, settingsKey, string(raw)); err != nil {
		s.mu.Unlock()
		return err
	}
	s.set = next
	cancel := s.cancel
	s.mu.Unlock()
	if prev.Enabled && !next.Enabled && cancel != nil {
		cancel()
	}
	s.Kick()
	return nil
}
