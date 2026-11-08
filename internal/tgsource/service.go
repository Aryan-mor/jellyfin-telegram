package tgsource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/tgerr"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/naming"
	"github.com/Aryan-mor/jellyfin-telegram/internal/pace"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

const (
	settingsKey = "tgsource_settings"
	pageSize    = 100
	// maxFloodWait is the longest Telegram wait that is slept out inside a pass.
	maxFloodWait = 10 * time.Minute
)

// Telegram is what the service needs from the signed-in accounts.
type Telegram interface {
	Lookup(ctx context.Context, input string) (tguser.Channel, error)
	History(ctx context.Context, ref tguser.SourceRef, offsetID, limit int) (tguser.Page, error)
	Copy(ctx context.Context, ref tguser.SourceRef, ids []int) error
	HasFreeSession() bool
	HasAccount() bool
}

// Service walks the source channels. It never touches playback: a failure is recorded and the next
// pass tries again.
type Service struct {
	Cat *catalog.Catalog
	TG  Telegram
	// LibraryChannel is the channel Jellyfin plays from. It is refused as a source.
	LibraryChannel int64
	Log            *slog.Logger
	// Sleep waits between groups. Tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
	Now   func() time.Time
	// InReady reports that the library already has this lookup hashtag. Nil skips the check.
	InReady func(ctx context.Context, tag string) (bool, error)

	mu       sync.Mutex
	loaded   bool
	set      Settings
	wake     chan struct{}
	running  bool
	note     string
	lastPass time.Time
	// run is held for the length of a pass, so the settings page can wait for it to stop.
	run sync.Mutex
	// cancel stops the pass in progress when the switch is turned off.
	cancel context.CancelFunc
}

// New returns a stopped service.
func New() *Service {
	return &Service{wake: make(chan struct{}, 1)}
}

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

func (s *Service) logf(msg string, args ...any) {
	if s.Log != nil {
		s.Log.Info(msg, args...)
	}
}

// Settings returns the saved settings.
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

// Save stores new settings. A wider look-back than the last finished pass walks the channels again;
// documents already copied are skipped.
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
	s.kick()
	return nil
}

func (s *Service) kick() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run checks the channels until ctx ends.
func (s *Service) Run(ctx context.Context) {
	for {
		set, err := s.Settings(ctx)
		if err == nil && set.Enabled {
			if s.Pass(ctx) {
				select {
				case <-ctx.Done():
					return
				case <-time.After(20 * time.Second):
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

// Pass walks every enabled channel once. It returns true when no account was free, so the caller
// tries again shortly instead of waiting for the normal interval.
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
		s.lastPass = s.now()
		s.mu.Unlock()
	}()

	set, err := s.Settings(ctx)
	if err != nil || !set.Enabled {
		return false
	}
	s.setNote("")
	sources, err := s.Cat.Sources(ctx)
	if err != nil {
		s.setNote(err.Error())
		return false
	}
	for _, src := range sources {
		if ctx.Err() != nil || !src.Enabled {
			continue
		}
		if src.NoForwards {
			continue
		}
		if err := s.walk(ctx, src, set); err != nil {
			if stop := s.record(ctx, src, err); stop {
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

// record stores why a channel failed. It reports true when the whole pass should stop.
func (s *Service) record(ctx context.Context, src catalog.Source, err error) (stop bool) {
	if ctx.Err() != nil {
		return true
	}
	switch {
	case errors.Is(err, tguser.ErrNoFreeSession):
		s.setNote("Waiting for a signed-in account that is free.")
		return true
	case errors.Is(err, errDailyCap):
		s.setNote("The daily limit is reached. Copying continues tomorrow.")
		return true
	}
	if _, ok := tgerr.AsFloodWait(err); ok {
		s.setNote("Telegram asked for a pause. Copying continues on the next check.")
		return true
	}
	msg := friendly(err)
	if tgerr.Is(err, "CHAT_FORWARDS_RESTRICTED") {
		// Remember it, so the page can say why and the channel is not tried again.
		_ = s.Cat.SetSourceError(ctx, src.ID, msg)
		_, _ = s.Cat.AddSource(ctx, catalog.Source{ChannelID: src.ChannelID, Username: src.Username, Title: src.Title, NoForwards: true})
		return false
	}
	_ = s.Cat.SetSourceError(ctx, src.ID, msg)
	if s.Log != nil {
		s.Log.Warn("copying from a channel", "channel", src.Title, "err", err)
	}
	return false
}

func friendly(err error) string {
	switch {
	case tgerr.Is(err, "CHAT_FORWARDS_RESTRICTED"):
		return "The owner of this channel forbids copying its files. Nothing can be taken from it."
	case tgerr.Is(err, "CHANNEL_PRIVATE", "CHANNEL_INVALID"):
		return "This account cannot read the channel any more. Join it again, or remove it from the list."
	case tgerr.Is(err, "CHAT_ADMIN_REQUIRED", "CHAT_WRITE_FORBIDDEN"):
		return "This account may not post in the library channel."
	}
	return err.Error()
}

var errDailyCap = errors.New("daily limit reached")

// walk goes through one channel from its newest message downwards, until it meets what an earlier
// walk handled or the look-back limit. Progress is saved after every page, so a restart resumes.
func (s *Service) walk(ctx context.Context, src catalog.Source, set Settings) error {
	ref := tguser.SourceRef{ChannelID: src.ChannelID, Username: src.Username}
	w := catalog.SourceWalk{Cursor: src.Cursor, ScanTop: src.ScanTop, Watermark: src.Watermark, CoveredDays: src.CoveredDays}
	if w.Cursor == 0 {
		// A new walk. If an earlier one covered less time than is wanted now, it starts again from the top.
		if !covers(w.CoveredDays, set.LookBackDays) {
			w.Watermark = 0
		}
		w.ScanTop = 0
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
			// The first page starts at the newest message, so its highest id is the top of this walk.
			w.ScanTop = page.Highest
		}
		done := !page.More || page.Lowest == 0 || page.Lowest <= floor
		if !cutoff.IsZero() && !page.Oldest.IsZero() && page.Oldest.Before(cutoff) {
			done = true
		}
		var take []tguser.Post
		for _, p := range page.Posts {
			if p.ID > floor && (cutoff.IsZero() || !p.Date.Before(cutoff)) {
				take = append(take, p)
			}
		}
		if err := s.copyPosts(ctx, src, take, set); err != nil {
			return err
		}
		if done {
			if w.ScanTop > w.Watermark {
				w.Watermark = w.ScanTop
			}
			w.Cursor, w.ScanTop, w.CoveredDays = 0, 0, set.LookBackDays
			return s.Cat.SaveSourceWalk(ctx, src.ID, w, "")
		}
		w.Cursor = page.Lowest
		if err := s.Cat.SaveSourceWalk(ctx, src.ID, w, ""); err != nil {
			return err
		}
	}
}

// covers says whether a walk made with have days of look-back already handled want days.
// Zero days means all of the history, and -1 means no walk has finished yet.
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

// copyPosts forwards the posts that pass the rules, oldest first and ten at a time.
func (s *Service) copyPosts(ctx context.Context, src catalog.Source, posts []tguser.Post, set Settings) error {
	if src.Strict {
		set.NeedTitle = true
	}
	var want []tguser.Post
	inPage := map[string]bool{}
	for _, p := range posts {
		if ok, _ := set.Wanted(p); !ok {
			continue
		}
		key := fmt.Sprintf("%s|%d", p.Name, p.Size)
		if inPage[key] {
			continue
		}
		if same, err := s.Cat.CopiedFile(ctx, p.Name, p.Size); err != nil {
			return err
		} else if same {
			continue
		}
		inPage[key] = true
		if done, err := s.Cat.CopiedDoc(ctx, p.DocID); err != nil {
			return err
		} else if done {
			continue
		}
		want = append(want, p)
	}
	sort.Slice(want, func(i, j int) bool { return want[i].ID < want[j].ID })
	ref := tguser.SourceRef{ChannelID: src.ChannelID, Username: src.Username}
	for len(want) > 0 {
		// A random burst of 20 to 50 videos goes out, then a random rest of 10 seconds up to the
		// pause setting, so the beat is never the same twice.
		n := min(pace.Burst(), len(want))
		batch := want[:n]
		want = want[n:]
		if set.MaxPerDay > 0 {
			day := s.now()
			start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
			have, err := s.Cat.CopyCount(ctx, start)
			if err != nil {
				return err
			}
			if have >= set.MaxPerDay {
				return errDailyCap
			}
			// A burst never goes past the cap: send what is left of today's allowance.
			if room := set.MaxPerDay - have; len(batch) > room {
				want = append(batch[room:len(batch):len(batch)], want...)
				batch = batch[:room]
			}
		}
		if err := s.copyBatch(ctx, src, ref, batch); err != nil {
			return err
		}
		if len(want) > 0 && set.PauseSeconds > 0 {
			if err := s.sleep(ctx, pace.Pause(time.Duration(set.PauseSeconds)*time.Second)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) copyBatch(ctx context.Context, src catalog.Source, ref tguser.SourceRef, batch []tguser.Post) error {
	var ids []int
	var send []tguser.Post
	for _, p := range batch {
		if p.NoForward {
			if err := s.mark(ctx, src, p, catalog.CopyFailed, "this post is protected from copying"); err != nil {
				return err
			}
			continue
		}
		if tag := naming.LookupTag(naming.Parse(p.Name, p.Caption)); tag != "" && s.InReady != nil {
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
		send = append(send, p)
	}
	if len(ids) == 0 {
		return nil
	}
	err := s.forward(ctx, ref, ids)
	if err == nil {
		for _, p := range send {
			if err := s.mark(ctx, src, p, catalog.CopyDone, ""); err != nil {
				return err
			}
		}
		s.logf("copied videos", "channel", src.Title, "count", len(send))
		return nil
	}
	// A message deleted since it was listed fails the whole group. Try them one by one so the rest go through.
	if len(send) > 1 && tgerr.Is(err, "MESSAGE_ID_INVALID", "MESSAGE_ID_EMPTY") {
		for _, p := range send {
			one := s.forward(ctx, ref, []int{p.ID})
			switch {
			case one == nil:
				if err := s.mark(ctx, src, p, catalog.CopyDone, ""); err != nil {
					return err
				}
			case tgerr.Is(one, "MESSAGE_ID_INVALID", "MESSAGE_ID_EMPTY"):
				if err := s.mark(ctx, src, p, catalog.CopyFailed, "the post was deleted"); err != nil {
					return err
				}
			default:
				return one
			}
		}
		return nil
	}
	return err
}

// forward sends once and sleeps out a short Telegram wait before trying a second time.
func (s *Service) forward(ctx context.Context, ref tguser.SourceRef, ids []int) error {
	err := s.TG.Copy(ctx, ref, ids)
	if wait, ok := tgerr.AsFloodWait(err); ok && wait > 0 && wait <= maxFloodWait {
		s.setNote(fmt.Sprintf("Telegram asked for a pause of %d seconds.", int(wait.Seconds())))
		if serr := s.sleep(ctx, wait+time.Second); serr != nil {
			return serr
		}
		return s.TG.Copy(ctx, ref, ids)
	}
	return err
}

func (s *Service) mark(ctx context.Context, src catalog.Source, p tguser.Post, state, errText string) error {
	if err := s.Cat.MarkCopied(ctx, s.now(), p.DocID, src.ID, p.ID, p.Name, p.Size, state, errText); err != nil {
		return err
	}
	ok := 0
	if state == catalog.CopyDone {
		ok = 1
	}
	return s.Cat.AddSourceCounts(ctx, src.ID, 1, ok)
}

// Add looks a channel up with the signed-in account and stores it.
func (s *Service) Add(ctx context.Context, input string) (catalog.Source, error) {
	ch, err := s.TG.Lookup(ctx, input)
	if err != nil {
		if errors.Is(err, tguser.ErrNoFreeSession) {
			return catalog.Source{}, errors.New("sign in a Telegram account that is an admin of the library channel first, and wait until it is not uploading")
		}
		return catalog.Source{}, err
	}
	if ch.ID == s.LibraryChannel {
		return catalog.Source{}, errors.New("that is the library channel itself. Give the channel to copy from")
	}
	src, err := s.Cat.AddSource(ctx, catalog.Source{ChannelID: ch.ID, Username: ch.Username, Title: ch.Title, NoForwards: ch.NoForwards})
	if err != nil {
		return catalog.Source{}, err
	}
	if src.NoForwards {
		_ = s.Cat.SetSourceError(ctx, src.ID, friendly(tgerr.New(400, "CHAT_FORWARDS_RESTRICTED")))
		src.Error = friendly(tgerr.New(400, "CHAT_FORWARDS_RESTRICTED"))
	}
	s.kick()
	return src, nil
}

// AddDiscovered adds a channel the Telegram search found trustworthy. It reports false when the
// channel is already a source, cannot be copied from, or is the library channel. The channel is
// marked as discovered, so the panel can tell it from one a person added.
func (s *Service) AddDiscovered(ctx context.Context, channelID int64, note string) (bool, error) {
	if channelID == s.LibraryChannel {
		return false, nil
	}
	if _, found, err := s.Cat.SourceByChannel(ctx, channelID); err != nil || found {
		return false, err
	}
	ch, err := s.TG.Lookup(ctx, fmt.Sprintf("-100%d", channelID))
	if err != nil {
		return false, err
	}
	if ch.NoForwards {
		return false, nil
	}
	src, added, err := s.Cat.AddDiscoveredSource(ctx, catalog.Source{ChannelID: ch.ID, Username: ch.Username, Title: ch.Title}, note)
	if err != nil {
		return false, err
	}
	if added {
		s.logf("a discovered channel was added to the direct file channels", "channel", src.ChannelID, "note", note)
		s.kick()
	}
	return added, nil
}

// Remove forgets a channel. What it already gave the library stays.
func (s *Service) Remove(ctx context.Context, id int64) error { return s.Cat.DeleteSource(ctx, id) }

// Strict makes one channel keep only files whose name shows a year or an episode number.
func (s *Service) Strict(ctx context.Context, id int64, on bool) error {
	return s.Cat.SetSourceStrict(ctx, id, on)
}

// Pause stops or resumes one channel.
func (s *Service) Pause(ctx context.Context, id int64, enabled bool) error {
	if err := s.Cat.SetSourceEnabled(ctx, id, enabled); err != nil {
		return err
	}
	s.kick()
	return nil
}

// CheckNow asks for a pass right away.
func (s *Service) CheckNow() { s.kick() }

// Rescan makes every channel be walked again from the top, within the look-back limit.
func (s *Service) Rescan(ctx context.Context) error {
	if err := s.Cat.RestartSourceWalk(ctx); err != nil {
		return err
	}
	s.kick()
	return nil
}

// Status is the JSON the panel shows.
func (s *Service) Status(ctx context.Context) (map[string]any, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	sources, err := s.Cat.Sources(ctx)
	if err != nil {
		return nil, err
	}
	if sources == nil {
		sources = []catalog.Source{}
	}
	recent, err := s.Cat.RecentCopies(ctx, 15)
	if err != nil {
		return nil, err
	}
	if recent == nil {
		recent = []catalog.CopyRow{}
	}
	day := s.now()
	today, _ := s.Cat.CopyCount(ctx, time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location()))
	s.mu.Lock()
	defer s.mu.Unlock()
	last := int64(0)
	if !s.lastPass.IsZero() {
		last = s.lastPass.Unix()
	}
	return map[string]any{
		"settings": set, "sources": sources, "recent": recent, "today": today,
		"running": s.running, "note": s.note, "last_pass": last,
		"account_free": s.TG.HasFreeSession(),
	}, nil
}

// DescribeWindow turns days into words for logs.
func DescribeWindow(days int) string {
	if days == 0 {
		return "all history"
	}
	return strings.TrimSpace(fmt.Sprintf("%d days", days))
}
