package tgsearch

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tgerr"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/hold"
	"github.com/Aryan-mor/jellyfin-telegram/internal/naming"
	"github.com/Aryan-mor/jellyfin-telegram/internal/sign"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// Query is one title to look for. A film uses #tm_m_<tmdb>. A series uses #tm_tv_<tmdb>, and each
// episode hashtag under it is its own file.
type Query struct {
	TMDB  int
	Movie bool
	// Runtime is the film's running time in minutes when known, otherwise zero.
	Runtime int
	// Title is the name Seerr shows, and AltTitle is the original name when it differs.
	Title, AltTitle string
	Year            int
	IMDb            string
	// DigitalOut is true once a digital or physical release date has arrived.
	DigitalOut bool
	// Release is that date, or the premiere when no digital date is known yet.
	Release time.Time
	// Request is a Seerr request. Those run before anything else, and a new one is searched
	// at once. Direct-channel lookup for a request does not wait between channels.
	Request bool
}

// Text is the hashtag Telegram is asked for.
func (q Query) Text() string {
	if q.TMDB <= 0 {
		return ""
	}
	if q.Movie {
		return fmt.Sprintf("#tm_m_%d", q.TMDB)
	}
	return fmt.Sprintf("#tm_tv_%d", q.TMDB)
}

// Telegram is the signed-in account.
type Telegram interface {
	SearchHashtag(ctx context.Context, query string) ([]tguser.SearchHit, error)
	InReady(ctx context.Context, tag string) (bool, error)
	Copy(ctx context.Context, ref tguser.SourceRef, ids []int) error
}

// Wanted lists titles that are still missing.
type Wanted interface {
	Queries(ctx context.Context) ([]Query, error)
}

// Store keeps what the search learned across restarts. The catalog implements it.
type Store interface {
	GetTrust(ctx context.Context, kind, id string) (catalog.Trust, error)
	AllTrust(ctx context.Context, kind string) (map[string]catalog.Trust, error)
	AddTrustPoint(ctx context.Context, kind, id string) (catalog.Trust, error)
	AddTrustFail(ctx context.Context, kind, id string) (catalog.Trust, error)
	SetTrustDenied(ctx context.Context, kind, id string, denied bool) error
	SeenTag(ctx context.Context, tag string) (string, int, error)
	MarkSeenTag(ctx context.Context, tag, state string) error
	DiscoveredCount(ctx context.Context) (int, error)
	StateValue(ctx context.Context, key string) (string, error)
	SetStateValue(ctx context.Context, key, value string) error
}

// Promoter adds a channel to the direct file channels. It reports false when the channel was
// already one.
type Promoter interface {
	AddDiscovered(ctx context.Context, channelID int64, note string) (bool, error)
}

// Limits are the numbers that steer trust and politeness. Zero fields take the defaults.
type Limits struct {
	// PromoteAt is how many verified videos a channel must show, counted when the first file from
	// it is accepted, before it becomes a direct file channel. When the channel cannot be read,
	// the same number of accepted search hits is used instead.
	PromoteAt int
	// MaxDiscovered caps the channels the search may add. Negative turns promotion off.
	MaxDiscovered int
	// DenyAfter is how many failed files, outnumbering the accepted ones, put a channel or a key
	// on the denylist.
	DenyAfter int
	// MaxCopyTries is how many times a failing copy is tried for one file.
	MaxCopyTries int
	// QueryCooldown negative turns the growing wait off, so every pass searches again.
	// Any other value uses that wait: 6 hours on the first day, then longer.
	QueryCooldown time.Duration
	// QueryGap is the pause between two global searches in one pass.
	QueryGap time.Duration
}

func (l Limits) withDefaults() Limits {
	if l.PromoteAt <= 0 {
		l.PromoteAt = 10
	}
	if l.MaxDiscovered == 0 {
		l.MaxDiscovered = 50
	}
	if l.DenyAfter <= 0 {
		l.DenyAfter = 3
	}
	if l.MaxCopyTries <= 0 {
		l.MaxCopyTries = 3
	}
	if l.QueryCooldown == 0 {
		l.QueryCooldown = 6 * time.Hour
	}
	return l
}

// Service searches with the signed-in account and copies a found file into the library channel.
type Service struct {
	TG   Telegram
	Want Wanted
	// Requests is only the Seerr list. It is polled often so a new request is searched at once.
	Requests       Wanted
	LibraryChannel int64
	Log            *slog.Logger
	// On reports whether the switch is still on. Nil means on.
	On    func() bool
	Every time.Duration

	// Verify holds the limits of the file check. The zero value checks only the hashtag and the
	// signature; use DefaultThresholds for the normal limits.
	Verify Thresholds
	Limits Limits
	// Store persists trust and attempts. Nil keeps them in memory until the process ends.
	Store Store
	// Promote adds a trusted channel to the direct file channels. Nil turns promotion off.
	Promote Promoter
	// Lists are the direct file channels and the news channels. Nil skips those steps.
	Lists interface {
		Sources(ctx context.Context) ([]catalog.Source, error)
		NewsChannels(ctx context.Context) ([]catalog.Source, error)
	}
	// News opens a bot start link and forwards the files that match. Nil skips news search.
	News interface {
		OpenStart(ctx context.Context, src catalog.Source, hit tguser.StartHit) ([]tguser.Post, error)
		FinishStart(ctx context.Context, src catalog.Source, hit tguser.StartHit, posts []tguser.Post) error
	}
	// Scan asks Seerr to notice a file once it is in the library. Nil skips it.
	Scan func(ctx context.Context) error
	// Now and Sleep are for tests.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error

	// The maps back the state when Store is nil, and cache trust during a pass.
	seen       map[string]string
	tries      map[string]int
	sched      map[string]string
	trust      map[string]catalog.Trust
	backoffTil time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
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

// requestPoll is how often Seerr is asked for new requests. A direct-channel lookup is cheap,
// so a request that was not there on the last look is searched at once.
const requestPoll = 15 * time.Second

// Run searches until ctx ends. The full pass waits a minute so Seerr can finish starting.
// New Seerr requests are searched on their own, without waiting for that pass.
func (s *Service) Run(ctx context.Context) {
	if s.Every <= 0 {
		s.Every = 15 * time.Minute
	}
	if s.Limits.QueryGap == 0 {
		s.Limits.QueryGap = 5 * time.Second
	}
	if hold.Wait(ctx) != nil {
		return
	}
	timer := time.NewTimer(time.Minute)
	poll := time.NewTimer(requestPoll)
	defer timer.Stop()
	defer poll.Stop()
	seen := map[string]bool{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.onePass(ctx)
			timer.Reset(s.Every)
		case <-poll.C:
			s.rushRequests(ctx, seen)
			poll.Reset(requestPoll)
		case <-hold.Changed():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			s.onePass(ctx)
			timer.Reset(s.Every)
		}
	}
}

func (s *Service) onePass(ctx context.Context) {
	if ctx.Err() != nil || hold.Stopped() {
		return
	}
	work, done := hold.Begin(ctx)
	s.pass(work)
	done()
}

func hitKey(h tguser.SearchHit) string { return fmt.Sprintf("hit:%d:%d", h.ChannelID, h.MessageID) }

func (s *Service) seenState(ctx context.Context, key string) (string, int) {
	if s.Store != nil {
		st, n, err := s.Store.SeenTag(ctx, key)
		if err == nil {
			return st, n
		}
		s.warn("read search state", err)
	}
	return s.seen[key], s.tries[key]
}

func (s *Service) markSeen(ctx context.Context, key, state string) {
	if s.seen == nil {
		s.seen, s.tries = map[string]string{}, map[string]int{}
	}
	s.seen[key] = state
	s.tries[key]++
	if s.Store != nil {
		s.warn("store search state", s.Store.MarkSeenTag(ctx, key, state))
	}
}

func (s *Service) loadTrust(ctx context.Context) {
	s.trust = map[string]catalog.Trust{}
	if s.Store == nil {
		return
	}
	for _, kind := range []string{catalog.TrustChannel, catalog.TrustKey} {
		m, err := s.Store.AllTrust(ctx, kind)
		if err != nil {
			s.warn("read trust", err)
			continue
		}
		for id, t := range m {
			s.trust[kind+"/"+id] = t
		}
	}
}

func channelKey(id int64) string { return strconv.FormatInt(id, 10) }

func (s *Service) getTrust(kind, id string) catalog.Trust { return s.trust[kind+"/"+id] }

func (s *Service) setTrust(kind, id string, t catalog.Trust) {
	if s.trust == nil {
		s.trust = map[string]catalog.Trust{}
	}
	s.trust[kind+"/"+id] = t
}

func (s *Service) pass(ctx context.Context) {
	if s.On != nil && !s.On() {
		return
	}
	if s.TG == nil || s.Want == nil {
		return
	}
	if s.now().Before(s.backoffTil) {
		return
	}
	queries, err := s.Want.Queries(ctx)
	if err != nil {
		s.warn("list wanted titles", err)
		return
	}
	s.loadTrust(ctx)
	s.searchAll(ctx, queries)
}

// rushRequests searches a Seerr request the moment it appears. Titles already on a schedule
// stay there. A title this call did not reach (the copy cap) stays unseen, so the next poll
// tries it.
func (s *Service) rushRequests(ctx context.Context, seen map[string]bool) {
	if s == nil || s.Requests == nil || hold.Stopped() {
		return
	}
	queries, err := s.Requests.Queries(ctx)
	if err != nil {
		s.warn("list seerr requests", err)
		return
	}
	live := map[string]bool{}
	var fresh []Query
	for _, q := range queries {
		q.Request = true
		key := q.Text()
		if key == "" {
			continue
		}
		live[key] = true
		if seen[key] {
			continue
		}
		if s.coolingDown(ctx, q) {
			seen[key] = true
			continue
		}
		fresh = append(fresh, q)
	}
	for key := range seen {
		if !live[key] {
			delete(seen, key)
		}
	}
	if len(fresh) == 0 || ctx.Err() != nil {
		return
	}
	if s.Log != nil {
		s.Log.Info("new seerr request", "count", len(fresh), "tmdb", fresh[0].TMDB)
	}
	work, done := hold.Begin(ctx)
	handled := s.searchAll(work, fresh)
	done()
	for _, key := range handled {
		seen[key] = true
	}
}

// searchAll runs the queries in order. Seerr requests look in the direct channels first.
// It returns the queries it finished. A query left behind by the copy cap is not in the list.
func (s *Service) searchAll(ctx context.Context, queries []Query) []string {
	if s.On != nil && !s.On() {
		return nil
	}
	if s.TG == nil {
		return nil
	}
	if s.now().Before(s.backoffTil) {
		return nil
	}
	s.loadTrust(ctx)
	sort.SliceStable(queries, func(i, j int) bool {
		return queries[i].Request && !queries[j].Request
	})
	copied, searches := 0, 0
	var handled []string
	for _, q := range queries {
		if copied >= 3 {
			return handled
		}
		if q.Text() == "" || s.coolingDown(ctx, q) {
			continue
		}
		before := copied
		var notes []stepNote
		// A request looks in the direct channels first. That search does not wait between channels.
		if q.Request {
			found, stop, have := s.searchDirect(ctx, q, &searches, &copied)
			notes = append(notes, stepNote{"direct", found, copied > before})
			if stop {
				s.logSteps(ctx, q, notes)
				return handled
			}
			if copied > before || have {
				s.noteSearched(ctx, q)
				s.logSteps(ctx, q, notes)
				handled = append(handled, q.Text())
				continue
			}
			before = copied
		}
		found, stop := s.searchHashtag(ctx, q, &searches, &copied)
		notes = append(notes, stepNote{"hashtag", found, copied > before})
		if stop {
			s.logSteps(ctx, q, notes)
			return handled
		}
		// A request film that the direct channels missed still checks news.
		if copied == before && q.Movie && q.Request {
			found, stop = s.searchNews(ctx, q, &searches, &copied)
			notes = append(notes, stepNote{"news", found, copied > before})
			if stop {
				s.logSteps(ctx, q, notes)
				return handled
			}
		}
		// Films that the hashtag did not copy are looked up in the direct channels, then in news.
		if copied == before && q.Movie && !q.Request {
			before = copied
			var have bool
			found, stop, have = s.searchDirect(ctx, q, &searches, &copied)
			notes = append(notes, stepNote{"direct", found, copied > before})
			if stop {
				s.logSteps(ctx, q, notes)
				return handled
			}
			if copied == before && !have {
				found, stop = s.searchNews(ctx, q, &searches, &copied)
				notes = append(notes, stepNote{"news", found, copied > before})
				if stop {
					s.logSteps(ctx, q, notes)
					return handled
				}
			}
		}
		s.noteSearched(ctx, q)
		s.logSteps(ctx, q, notes)
		handled = append(handled, q.Text())
	}
	return handled
}

type stepNote struct {
	step   string
	found  int
	copied bool
}

func (s *Service) logSteps(ctx context.Context, q Query, notes []stepNote) {
	if s.Log == nil {
		return
	}
	next := ""
	if due := s.dueAt(ctx, q); !due.IsZero() {
		next = due.UTC().Format(time.RFC3339)
	}
	for _, n := range notes {
		s.Log.Info("searched title", "tmdb", q.TMDB, "step", n.step, "found", n.found, "copied", n.copied, "next", next)
	}
}

func (s *Service) searchHashtag(ctx context.Context, q Query, searches, copied *int) (int, bool) {
	if !s.gap(ctx, searches) {
		return 0, true
	}
	hits, err := s.TG.SearchHashtag(ctx, q.Text())
	if err != nil {
		s.searchFailed(err)
		return 0, true
	}
	if !s.handle(ctx, q, hits, copied) {
		return len(hits), true
	}
	return len(hits), false
}

// gap pauses between Telegram searches in one pass. The first search of the pass does not wait.
func (s *Service) gap(ctx context.Context, searches *int) bool {
	if *searches > 0 && s.sleep(ctx, s.Limits.QueryGap) != nil {
		return false
	}
	*searches++
	return true
}

func (s *Service) scanLibrary() {
	if s.Scan == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		s.warn("ask Seerr to scan the library", s.Scan(ctx))
	}()
}

// searchFailed backs off for as long as Telegram asked when it answered with a flood wait.
func (s *Service) searchFailed(err error) {
	s.warn("telegram search", err)
	if wait, ok := tgerr.AsFloodWait(err); ok {
		if wait < time.Minute {
			wait = time.Minute
		}
		s.backoffTil = s.now().Add(wait)
	}
}

// handle works through the hits of one query. It returns false when the pass should stop.
func (s *Service) handle(ctx context.Context, q Query, hits []tguser.SearchHit, copied *int) bool {
	lim := s.Limits.withDefaults()
	skip := map[string]bool{}
	rejected := map[string]bool{}
	for *copied < 3 {
		hit, tag, ok := s.choose(ctx, q, hits, skip, rejected)
		if !ok {
			return true
		}
		if st, n := s.seenState(ctx, tag); st == catalog.SeenDone || (st == catalog.SeenTried && n >= lim.MaxCopyTries) {
			skip[tag] = true
			continue
		}
		ready, err := s.TG.InReady(ctx, naming.BaseTag(tag))
		if err != nil {
			s.warn("telegram search", err)
			return false
		}
		if ready {
			skip[tag] = true
			s.markSeen(ctx, tag, catalog.SeenDone)
			continue
		}
		v := Verify(q, hit, tag, s.Verify)
		if !v.OK {
			rejected[hitKey(hit)] = true
			s.markSeen(ctx, hitKey(hit), catalog.SeenReject)
			s.penalise(ctx, hit, v, lim)
			if s.Log != nil {
				s.Log.Info("rejected a file found on Telegram", "tag", tag, "channel", hit.ChannelID, "why", v.Reason)
			}
			continue
		}
		err = s.TG.Copy(ctx, tguser.SourceRef{ChannelID: hit.ChannelID, AccessHash: hit.AccessHash}, s.withSubtitle(ctx, hit))
		if err != nil {
			s.warn("copy "+tag, err)
			s.markSeen(ctx, tag, catalog.SeenTried)
			rejected[hitKey(hit)] = true
			continue
		}
		skip[tag] = true
		s.markSeen(ctx, tag, catalog.SeenDone)
		*copied++
		s.scanLibrary()
		s.reward(ctx, hit, v, lim)
		if s.Log != nil {
			s.Log.Info("copied a file found on Telegram", "tag", tag, "channel", hit.ChannelID)
		}
	}
	return true
}

// choose picks the best remaining candidate: signed first, then the more trusted channel and key,
// then a video, then the larger file. Denied channels and keys and rejected hits are left out.
func (s *Service) choose(ctx context.Context, q Query, hits []tguser.SearchHit, skip, rejected map[string]bool) (tguser.SearchHit, string, bool) {
	var usable []tguser.SearchHit
	for _, h := range hits {
		if rejected[hitKey(h)] || s.getTrust(catalog.TrustChannel, channelKey(h.ChannelID)).Denied {
			continue
		}
		if st, _ := s.seenState(ctx, hitKey(h)); st == catalog.SeenReject {
			continue
		}
		if id := signerOf(h); id != "" && s.getTrust(catalog.TrustKey, id).Denied {
			continue
		}
		usable = append(usable, h)
	}
	return chooseWith(q, usable, skip, s.LibraryChannel, func(h tguser.SearchHit) int {
		score := s.getTrust(catalog.TrustChannel, channelKey(h.ChannelID)).Points
		if id := signerOf(h); id != "" {
			score += s.getTrust(catalog.TrustKey, id).Points
		}
		return score
	})
}

// signerOf is the key id of an authentic signature in the caption, or "".
func signerOf(h tguser.SearchHit) string {
	meta, ok := sign.ParseCaption(h.Caption)
	if !ok || !meta.Authentic() {
		return ""
	}
	return sign.KeyID(meta.PubKey)
}

// channelHistory reads the videos of one channel. The signed-in account and the hashtag searcher
// both provide it.
type channelHistory interface {
	History(ctx context.Context, ref tguser.SourceRef, offsetID, limit int) (tguser.Page, error)
}

// slotFreeer drops one discovered channel that is banned or no longer posting, so a new one can
// take its place. Promotion still works when the promoter does not implement this.
type slotFreeer interface {
	FreeSlot(ctx context.Context) (bool, error)
}

// reward gives the accepted file's channel and signer a point. The first accepted file makes the
// search read that channel's other videos and document-videos and verify them then. Enough of
// those, and the channel becomes a direct file channel.
func (s *Service) reward(ctx context.Context, hit tguser.SearchHit, v Verdict, lim Limits) {
	s.addPoint(ctx, catalog.TrustChannel, channelKey(hit.ChannelID))
	if v.KeyID != "" {
		s.addPoint(ctx, catalog.TrustKey, v.KeyID)
	}
	t := s.getTrust(catalog.TrustChannel, channelKey(hit.ChannelID))
	if s.Promote == nil || lim.MaxDiscovered < 0 || t.Denied {
		return
	}
	verified := t.Points
	if hist, ok := s.TG.(channelHistory); ok {
		n, err := s.auditChannel(ctx, hit, hist, lim.PromoteAt)
		if err != nil {
			s.warn("read the channel before adding it", err)
		} else {
			verified = n
		}
	}
	if verified < lim.PromoteAt {
		return
	}
	if s.Store != nil {
		n, err := s.Store.DiscoveredCount(ctx)
		if err != nil {
			s.warn("count discovered channels", err)
			return
		}
		if n >= lim.MaxDiscovered {
			free, ok := s.Promote.(slotFreeer)
			if !ok {
				return
			}
			freed, err := free.FreeSlot(ctx)
			if err != nil {
				s.warn("drop a direct channel that is not usable", err)
				return
			}
			if !freed {
				return
			}
		}
	}
	added, err := s.Promote.AddDiscovered(ctx, hit.ChannelID, fmt.Sprintf("%d verified files", verified))
	if err != nil {
		s.warn("add discovered channel", err)
		return
	}
	if added && s.Log != nil {
		s.Log.Info("a channel's videos verified, so it became a direct file channel", "channel", hit.ChannelID, "verified", verified)
	}
}

// auditChannel counts real videos and document-videos in the channel, including the file just
// accepted. It stops once want of them pass. Two pages are enough to see a channel that posts films.
func (s *Service) auditChannel(ctx context.Context, hit tguser.SearchHit, hist channelHistory, want int) (int, error) {
	if want < 1 {
		want = 1
	}
	ref := tguser.SourceRef{ChannelID: hit.ChannelID, AccessHash: hit.AccessHash}
	seenHit := false
	verified := 0
	offset := 0
	var firstErr error
	for pageN := 0; pageN < 2 && verified < want; pageN++ {
		page, err := hist.History(ctx, ref, offset, 100)
		if err != nil {
			if verified == 0 && !seenHit {
				return 0, err
			}
			firstErr = err
			break
		}
		for _, p := range page.Posts {
			if p.ID == hit.MessageID {
				seenHit = true
			}
			if credibleVideo(p, s.Verify) {
				verified++
			}
		}
		if !page.More || page.Lowest == 0 || page.Lowest == offset {
			break
		}
		offset = page.Lowest
	}
	if !seenHit {
		verified++
	}
	return verified, firstErr
}

func (s *Service) addPoint(ctx context.Context, kind, id string) {
	t := s.getTrust(kind, id)
	t.Points++
	if s.Store != nil {
		if nt, err := s.Store.AddTrustPoint(ctx, kind, id); err == nil {
			t = nt
		} else {
			s.warn("store trust", err)
		}
	}
	s.setTrust(kind, id, t)
}

// penalise counts a failed file against its channel, and against its signer when the signature was
// genuine. A forged signature never blames the key it names.
func (s *Service) penalise(ctx context.Context, hit tguser.SearchHit, v Verdict, lim Limits) {
	s.addFail(ctx, catalog.TrustChannel, channelKey(hit.ChannelID), lim)
	if v.BlameKey && v.KeyID != "" {
		s.addFail(ctx, catalog.TrustKey, v.KeyID, lim)
	}
}

// addFail counts a failure and denylists a subject that fails repeatedly and more often than it
// succeeds.
func (s *Service) addFail(ctx context.Context, kind, id string, lim Limits) {
	t := s.getTrust(kind, id)
	t.Fails++
	if s.Store != nil {
		if nt, err := s.Store.AddTrustFail(ctx, kind, id); err == nil {
			t = nt
		} else {
			s.warn("store trust", err)
		}
	}
	if !t.Denied && t.Fails >= lim.DenyAfter && t.Fails > t.Points {
		t.Denied = true
		if s.Store != nil {
			s.warn("store denylist", s.Store.SetTrustDenied(ctx, kind, id, true))
		}
		if s.Log != nil {
			s.Log.Warn("denylisted after repeated failed files", "kind", kind)
		}
	}
	s.setTrust(kind, id, t)
}

func (s *Service) warn(what string, err error) {
	if s.Log != nil && err != nil {
		s.Log.Warn(what, "err", err)
	}
}

var (
	// A language suffix may follow the title: _<audio> or _<audio>_<sub>, and "none" may fill the audio slot.
	reMovie = regexp.MustCompile(`#tm_m_(\d+)(?:_(?:none|[a-z]{2,3})(?:_[a-z]{2,3})?)?\b`)
	reTV    = regexp.MustCompile(`#tm_tv_(\d+)_(\d+)_(\d+)(?:_(?:none|[a-z]{2,3})(?:_[a-z]{2,3})?)?\b`)
)

// Choose picks the best file for one query. The returned tag is the exact hashtag of that file.
// skip holds tags already considered. library is this installation's own channel and is ignored.
func Choose(q Query, hits []tguser.SearchHit, skip map[string]bool, library int64) (tguser.SearchHit, string, bool) {
	return chooseWith(q, hits, skip, library, nil)
}

// chooseWith is Choose with a trust score: among files that are equal otherwise, the higher score wins.
func chooseWith(q Query, hits []tguser.SearchHit, skip map[string]bool, library int64, score func(tguser.SearchHit) int) (tguser.SearchHit, string, bool) {
	var best tguser.SearchHit
	var tag string
	found := false
	for _, hit := range hits {
		if hit.NoForward || hit.MessageID == 0 || hit.ChannelID == 0 || hit.ChannelID == library {
			continue
		}
		exact := tagOf(q, hit.Caption)
		if exact == "" || skip[exact] {
			continue
		}
		if !found || better(hit, best, score) {
			best, tag, found = hit, exact, true
		}
	}
	return best, tag, found
}

func better(a, b tguser.SearchHit, score func(tguser.SearchHit) int) bool {
	as, bs := strings.Contains(a.Caption, "jftg:"), strings.Contains(b.Caption, "jftg:")
	if as != bs {
		return as
	}
	if score != nil {
		if x, y := score(a), score(b); x != y {
			return x > y
		}
	}
	if a.Video != b.Video {
		return a.Video
	}
	return a.Size > b.Size
}

func tagOf(q Query, caption string) string {
	if q.Movie {
		for _, m := range reMovie.FindAllStringSubmatch(caption, -1) {
			id, _ := strconv.Atoi(m[1])
			if id == q.TMDB {
				return m[0]
			}
		}
		return ""
	}
	for _, m := range reTV.FindAllStringSubmatch(caption, -1) {
		id, _ := strconv.Atoi(m[1])
		if id == q.TMDB {
			return m[0]
		}
	}
	return ""
}
