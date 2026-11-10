package tgsearch

import (
	"context"
	"sort"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

const channelBudget = 15

// channelSearch is the signed-in account, or the hashtag index wrapper around it.
type channelSearch interface {
	SearchChannel(ctx context.Context, ref tguser.SourceRef, query string, limit int) ([]tguser.SearchHit, error)
	SearchPosts(ctx context.Context, ref tguser.SourceRef, query string, limit int) ([]tguser.TextPost, error)
}

// searchDirect looks through the direct file channels for one film. It stops at the first
// channel that yields a file the title check and the size check both accept.
func (s *Service) searchDirect(ctx context.Context, q Query, searches, copied *int) (found int, stop, have bool) {
	if !q.Movie || s.Lists == nil || copied == nil || *copied >= 3 {
		return 0, false, false
	}
	finder, ok := s.TG.(channelSearch)
	if !ok {
		return 0, false, false
	}
	texts := searchTexts(q)
	if len(texts) == 0 {
		return 0, false, false
	}
	channels, err := s.directChannels(ctx)
	if err != nil {
		s.warn("list direct channels", err)
		return 0, false, false
	}
	used := 0
	for _, src := range channels {
		if used >= channelBudget || *copied >= 3 {
			break
		}
		ref := tguser.SourceRef{ChannelID: src.ChannelID, Username: src.Username}
		for _, text := range texts {
			if used >= channelBudget {
				break
			}
			if !s.gap(ctx, searches) {
				return found, true, false
			}
			used++
			hits, err := finder.SearchChannel(ctx, ref, text, 20)
			if err != nil {
				s.searchFailed(err)
				return found, true, false
			}
			found += len(hits)
			switch s.takeFile(ctx, q, hits, copied) {
			case takeCopied:
				return found, false, false
			case takeHave:
				return found, false, true
			case takeStop:
				return found, true, false
			}
		}
	}
	return found, false, false
}

const (
	takeNone = iota
	takeCopied
	takeHave
	takeStop
)

func (s *Service) directChannels(ctx context.Context) ([]catalog.Source, error) {
	all, err := s.Lists.Sources(ctx)
	if err != nil {
		return nil, err
	}
	var on []catalog.Source
	for _, src := range all {
		if !src.Enabled || src.NoForwards || src.ChannelID == 0 || src.ChannelID == s.LibraryChannel {
			continue
		}
		if s.getTrust(catalog.TrustChannel, channelKey(src.ChannelID)).Denied {
			continue
		}
		on = append(on, src)
	}
	sort.SliceStable(on, func(i, j int) bool {
		pi := s.getTrust(catalog.TrustChannel, channelKey(on[i].ChannelID)).Points
		pj := s.getTrust(catalog.TrustChannel, channelKey(on[j].ChannelID)).Points
		if pi != pj {
			return pi > pj
		}
		return on[i].ID < on[j].ID
	})
	return on, nil
}

// takeFile copies the first hit that matches the title and passes the file check.
func (s *Service) takeFile(ctx context.Context, q Query, hits []tguser.SearchHit, copied *int) int {
	tag := q.Text()
	for _, hit := range hits {
		if hit.NoForward || hit.MessageID == 0 || hit.ChannelID == 0 || hit.ChannelID == s.LibraryChannel {
			continue
		}
		if ok, _ := MatchTitle(q, hit); !ok {
			continue
		}
		if st, _ := s.seenState(ctx, tag); st == catalog.SeenDone {
			return takeHave
		}
		ready, err := s.TG.InReady(ctx, tag)
		if err != nil {
			s.warn("telegram search", err)
			return takeStop
		}
		if ready {
			s.markSeen(ctx, tag, catalog.SeenDone)
			return takeHave
		}
		if ok, reason := checkFile(q, hit, hit.Duration, s.Verify); !ok {
			if s.Log != nil {
				s.Log.Info("rejected a file found on Telegram", "tag", tag, "channel", hit.ChannelID, "why", reason)
			}
			continue
		}
		err = s.TG.Copy(ctx, tguser.SourceRef{ChannelID: hit.ChannelID, AccessHash: hit.AccessHash}, s.withSubtitle(ctx, hit))
		if err != nil {
			s.warn("copy "+tag, err)
			s.markSeen(ctx, tag, catalog.SeenTried)
			continue
		}
		s.markSeen(ctx, tag, catalog.SeenDone)
		*copied++
		s.scanLibrary()
		if s.Log != nil {
			s.Log.Info("copied a file found on Telegram", "tag", tag, "channel", hit.ChannelID)
		}
		return takeCopied
	}
	return takeNone
}
