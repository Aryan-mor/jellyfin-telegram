package tgsearch

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tgerr"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
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
	// PromoteAt is how many accepted files make a channel a direct file channel.
	PromoteAt int
	// MaxDiscovered caps the channels the search may add. Negative turns promotion off.
	MaxDiscovered int
	// DenyAfter is how many failed files, outnumbering the accepted ones, put a channel or a key
	// on the denylist.
	DenyAfter int
	// MaxCopyTries is how many times a failing copy is tried for one file.
	MaxCopyTries int
	// QueryCooldown is how long a title is left alone after it was searched. Negative turns it off.
	QueryCooldown time.Duration
	// QueryGap is the pause between two global searches in one pass.
	QueryGap time.Duration
}

func (l Limits) withDefaults() Limits {
	if l.PromoteAt <= 0 {
		l.PromoteAt = 10
	}
	if l.MaxDiscovered == 0 {
		l.MaxDiscovered = 20
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
	TG             Telegram
	Want           Wanted
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
	// Now and Sleep are for tests.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error

	// The maps back the state when Store is nil, and cache trust during a pass.
	seen       map[string]string
	tries      map[string]int
	searched   map[string]time.Time
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

// Run searches until ctx ends. The first pass waits a minute so Seerr can finish starting.
func (s *Service) Run(ctx context.Context) {
	if s.Every <= 0 {
		s.Every = 15 * time.Minute
	}
	if s.Limits.QueryGap == 0 {
		s.Limits.QueryGap = 5 * time.Second
	}
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		s.pass(ctx)
		timer.Reset(s.Every)
	}
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

func queryKey(q Query) string { return "tgsearch.q." + q.Text() }

// coolingDown reports that this title was searched lately.
func (s *Service) coolingDown(ctx context.Context, q Query) bool {
	cool := s.Limits.withDefaults().QueryCooldown
	if cool < 0 {
		return false
	}
	var last time.Time
	if s.Store != nil {
		raw, err := s.Store.StateValue(ctx, queryKey(q))
		if unix, perr := strconv.ParseInt(raw, 10, 64); err == nil && perr == nil {
			last = time.Unix(unix, 0)
		}
	} else {
		last = s.searched[queryKey(q)]
	}
	return !last.IsZero() && s.now().Sub(last) < cool
}

func (s *Service) noteSearched(ctx context.Context, q Query) {
	if s.searched == nil {
		s.searched = map[string]time.Time{}
	}
	s.searched[queryKey(q)] = s.now()
	if s.Store != nil {
		s.warn("store search time", s.Store.SetStateValue(ctx, queryKey(q), strconv.FormatInt(s.now().Unix(), 10)))
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
		s.warn("telegram search", err)
		return
	}
	s.loadTrust(ctx)
	copied, searches := 0, 0
	for _, q := range queries {
		if copied >= 3 {
			return
		}
		if q.Text() == "" || s.coolingDown(ctx, q) {
			continue
		}
		// One global search at a time, with a pause between them.
		if searches > 0 && s.sleep(ctx, s.Limits.QueryGap) != nil {
			return
		}
		searches++
		hits, err := s.TG.SearchHashtag(ctx, q.Text())
		if err != nil {
			s.searchFailed(err)
			return
		}
		s.noteSearched(ctx, q)
		if !s.handle(ctx, q, hits, &copied) {
			return
		}
	}
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
		err = s.TG.Copy(ctx, tguser.SourceRef{ChannelID: hit.ChannelID, AccessHash: hit.AccessHash}, []int{hit.MessageID})
		if err != nil {
			s.warn("copy "+tag, err)
			s.markSeen(ctx, tag, catalog.SeenTried)
			rejected[hitKey(hit)] = true
			continue
		}
		skip[tag] = true
		s.markSeen(ctx, tag, catalog.SeenDone)
		*copied++
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

// reward gives the accepted file's channel and signer a point, and promotes a channel that earned
// enough of them.
func (s *Service) reward(ctx context.Context, hit tguser.SearchHit, v Verdict, lim Limits) {
	s.addPoint(ctx, catalog.TrustChannel, channelKey(hit.ChannelID))
	if v.KeyID != "" {
		s.addPoint(ctx, catalog.TrustKey, v.KeyID)
	}
	t := s.getTrust(catalog.TrustChannel, channelKey(hit.ChannelID))
	if s.Promote == nil || lim.MaxDiscovered < 0 || t.Denied || t.Points < lim.PromoteAt {
		return
	}
	if s.Store != nil {
		n, err := s.Store.DiscoveredCount(ctx)
		if err != nil {
			s.warn("count discovered channels", err)
			return
		}
		if n >= lim.MaxDiscovered {
			return
		}
	}
	added, err := s.Promote.AddDiscovered(ctx, hit.ChannelID, fmt.Sprintf("%d verified files", t.Points))
	if err != nil {
		s.warn("add discovered channel", err)
		return
	}
	if added && s.Log != nil {
		s.Log.Info("a channel earned enough trust to become a direct file channel", "channel", hit.ChannelID, "points", t.Points)
	}
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
