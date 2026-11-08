package tgsearch

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// Query is one title to look for. A film uses #tm_m_<tmdb>. A series uses #tm_tv_<tmdb>, and each
// episode hashtag under it is its own file.
type Query struct {
	TMDB  int
	Movie bool
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

// Service searches with the signed-in account and copies a found file into the library channel.
type Service struct {
	TG             Telegram
	Want           Wanted
	LibraryChannel int64
	Log            *slog.Logger
	// On reports whether the switch is still on. Nil means on.
	On    func() bool
	Every time.Duration

	seen map[string]bool
}

// Run searches until ctx ends. The first pass waits a minute so Seerr can finish starting.
func (s *Service) Run(ctx context.Context) {
	if s.Every <= 0 {
		s.Every = 15 * time.Minute
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

func (s *Service) pass(ctx context.Context) {
	if s.On != nil && !s.On() {
		return
	}
	if s.TG == nil || s.Want == nil {
		return
	}
	queries, err := s.Want.Queries(ctx)
	if err != nil {
		s.warn("telegram search", err)
		return
	}
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	copied := 0
	for _, q := range queries {
		if copied >= 3 || q.Text() == "" {
			return
		}
		hits, err := s.TG.SearchHashtag(ctx, q.Text())
		if err != nil {
			s.warn("telegram search", err)
			return
		}
		skip := map[string]bool{}
		for copied < 3 {
			hit, tag, ok := Choose(q, hits, skip, s.LibraryChannel)
			if !ok {
				break
			}
			skip[tag] = true
			if s.seen[tag] {
				continue
			}
			ready, err := s.TG.InReady(ctx, tag)
			if err != nil {
				s.warn("telegram search", err)
				return
			}
			if ready {
				s.seen[tag] = true
				continue
			}
			err = s.TG.Copy(ctx, tguser.SourceRef{ChannelID: hit.ChannelID, AccessHash: hit.AccessHash}, []int{hit.MessageID})
			if err != nil {
				s.warn("copy "+tag, err)
				continue
			}
			s.seen[tag] = true
			copied++
			if s.Log != nil {
				s.Log.Info("copied a file found on Telegram", "tag", tag, "channel", hit.ChannelID)
			}
		}
	}
}

func (s *Service) warn(what string, err error) {
	if s.Log != nil && err != nil {
		s.Log.Warn(what, "err", err)
	}
}

var (
	reMovie = regexp.MustCompile(`#tm_m_(\d+)\b`)
	reTV    = regexp.MustCompile(`#tm_tv_(\d+)_(\d+)_(\d+)\b`)
)

// Choose picks the best file for one query. The returned tag is the exact hashtag of that file.
// skip holds tags already considered. library is this installation's own channel and is ignored.
func Choose(q Query, hits []tguser.SearchHit, skip map[string]bool, library int64) (tguser.SearchHit, string, bool) {
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
		if !found || better(hit, best) {
			best, tag, found = hit, exact, true
		}
	}
	return best, tag, found
}

func better(a, b tguser.SearchHit) bool {
	as, bs := strings.Contains(a.Caption, "jftg:"), strings.Contains(b.Caption, "jftg:")
	if as != bs {
		return as
	}
	if a.Video != b.Video {
		return a.Video
	}
	return a.Size > b.Size
}

func tagOf(q Query, caption string) string {
	if q.Movie {
		for _, m := range reMovie.FindAllStringSubmatch(caption, -1) {
			if m[0] == q.Text() {
				return m[0]
			}
		}
		return ""
	}
	prefix := q.Text() + "_"
	for _, m := range reTV.FindAllStringSubmatch(caption, -1) {
		if strings.HasPrefix(m[0], prefix) {
			return m[0]
		}
	}
	return ""
}
