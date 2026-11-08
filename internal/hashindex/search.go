package hashindex

import (
	"context"
	"log/slog"
	"strings"

	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// Account is the signed-in account the search wraps.
type Account interface {
	SearchHashtag(ctx context.Context, query string) ([]tguser.SearchHit, error)
	InReady(ctx context.Context, tag string) (bool, error)
	Copy(ctx context.Context, ref tguser.SourceRef, ids []int) error
	PostsByLink(ctx context.Context, refs []tguser.PostRef) ([]tguser.SearchHit, error)
}

// Searcher adds the index to the global hashtag search. Wanted titles from requests and from
// trending go through the same search, so both use the index without extra code.
type Searcher struct {
	Account
	Index *Client
	// Skip reports channels whose posts should not be offered, such as this library's own mirrors.
	Skip func(channel string) bool
	// MaxLinks caps the links read from the index for one title. Zero means 60.
	MaxLinks int
	Log      *slog.Logger
}

// patterns are the index searches that cover one wanted hashtag. A film's own tag may carry
// language letters after it, so both forms are asked. A series asks for every episode.
func patterns(query string) []string {
	switch {
	case strings.HasPrefix(query, "#tm_m_"):
		return []string{query, query + "_*"}
	case strings.HasPrefix(query, "#tm_tv_"):
		return []string{query + "_*"}
	}
	return nil
}

// SearchHashtag runs the global search and then asks the index. A failure of one is not a
// failure of the other: the global search keeps its flood wait handling, and the index adds hits
// when it can.
func (s *Searcher) SearchHashtag(ctx context.Context, query string) ([]tguser.SearchHit, error) {
	hits, searchErr := s.Account.SearchHashtag(ctx, query)
	if s.Index == nil {
		return hits, searchErr
	}
	extra, err := s.fromIndex(ctx, query)
	if err != nil && s.Log != nil {
		s.Log.Warn("hashtag index search failed", "query", query, "err", err)
	}
	if len(extra) == 0 {
		return hits, searchErr
	}
	seen := map[[2]int64]bool{}
	for _, h := range hits {
		seen[[2]int64{h.ChannelID, int64(h.MessageID)}] = true
	}
	for _, h := range extra {
		if k := [2]int64{h.ChannelID, int64(h.MessageID)}; !seen[k] {
			seen[k] = true
			hits = append(hits, h)
		}
	}
	return hits, nil
}

func (s *Searcher) fromIndex(ctx context.Context, query string) ([]tguser.SearchHit, error) {
	limit := s.MaxLinks
	if limit <= 0 {
		limit = 60
	}
	var refs []tguser.PostRef
	var firstErr error
	for _, p := range patterns(query) {
		entries, err := s.Index.Query(ctx, p)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		for _, e := range entries {
			if s.Skip != nil && s.Skip(e.Channel) {
				continue
			}
			if len(refs) < limit {
				refs = append(refs, tguser.PostRef{Channel: e.Channel, Message: e.Message})
			}
		}
	}
	if len(refs) == 0 {
		return nil, firstErr
	}
	hits, err := s.Account.PostsByLink(ctx, refs)
	if err != nil {
		return hits, err
	}
	return hits, firstErr
}
