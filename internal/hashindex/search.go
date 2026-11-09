package hashindex

import (
	"context"
	"errors"
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

// Searcher asks the hashtag index before the chats this account already has. Wanted titles from
// requests and from trending go through the same search, so both use the index without extra code.
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

// SearchHashtag asks the hashtag index first. Posts there can name channels this account has not
// joined, which is how a new direct channel is found. When the index has nothing, the account
// searches the chats it already has, once.
func (s *Searcher) SearchHashtag(ctx context.Context, query string) ([]tguser.SearchHit, error) {
	if s.Index == nil {
		return s.Account.SearchHashtag(ctx, query)
	}
	extra, err := s.fromIndex(ctx, query)
	if err != nil && s.Log != nil {
		s.Log.Warn("hashtag index search failed", "query", query, "err", err)
	}
	if len(extra) > 0 {
		return extra, nil
	}
	hits, searchErr := s.Account.SearchHashtag(ctx, query)
	if searchErr != nil {
		return hits, searchErr
	}
	return hits, nil
}

// History reads a channel through the signed-in account, so a found channel can be checked at once.
func (s *Searcher) History(ctx context.Context, ref tguser.SourceRef, offsetID, limit int) (tguser.Page, error) {
	h, ok := s.Account.(interface {
		History(context.Context, tguser.SourceRef, int, int) (tguser.Page, error)
	})
	if !ok || h == nil {
		return tguser.Page{}, errors.New("the signed-in account cannot read channel history")
	}
	return h.History(ctx, ref, offsetID, limit)
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
