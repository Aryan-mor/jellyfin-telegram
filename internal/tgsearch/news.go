package tgsearch

import (
	"context"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// searchNews looks through news channels for a post about one film, opens its bot link,
// and forwards a delivered file only when the title and the file check both pass.
func (s *Service) searchNews(ctx context.Context, q Query, searches, copied *int) (int, bool) {
	if !q.Movie || s.Lists == nil || s.News == nil || copied == nil || *copied >= 3 {
		return 0, false
	}
	finder, ok := s.TG.(channelSearch)
	if !ok {
		return 0, false
	}
	texts := searchTexts(q)
	if len(texts) == 0 {
		return 0, false
	}
	channels, err := s.Lists.NewsChannels(ctx)
	if err != nil {
		s.warn("list news channels", err)
		return 0, false
	}
	found := 0
	used := 0
	for _, src := range channels {
		if !src.Enabled || src.ChannelID == 0 || src.ChannelID == s.LibraryChannel || used >= channelBudget || *copied >= 3 {
			continue
		}
		ref := tguser.SourceRef{ChannelID: src.ChannelID, Username: src.Username}
		for _, text := range texts {
			if used >= channelBudget || *copied >= 3 {
				break
			}
			if !s.gap(ctx, searches) {
				return found, true
			}
			used++
			posts, err := finder.SearchPosts(ctx, ref, text, 20)
			if err != nil {
				s.searchFailed(err)
				return found, true
			}
			found += len(posts)
			done, stop := s.takeNews(ctx, q, src, posts, copied)
			if stop {
				return found, true
			}
			if done {
				return found, false
			}
		}
	}
	return found, false
}

func (s *Service) takeNews(ctx context.Context, q Query, src catalog.Source, posts []tguser.TextPost, copied *int) (done, stop bool) {
	tag := q.Text()
	for _, post := range posts {
		hit := tguser.SearchHit{ChannelID: post.ChannelID, MessageID: post.MessageID, Caption: post.Text}
		if ok, _ := MatchTitle(q, hit); !ok {
			continue
		}
		if len(post.Links) == 0 {
			continue
		}
		if st, _ := s.seenState(ctx, tag); st == catalog.SeenDone {
			return true, false
		}
		ready, err := s.TG.InReady(ctx, tag)
		if err != nil {
			s.warn("telegram search", err)
			return false, true
		}
		if ready {
			s.markSeen(ctx, tag, catalog.SeenDone)
			return true, false
		}
		for _, link := range post.Links {
			start := tguser.StartHit{ID: post.MessageID, URL: link}
			got, err := s.News.OpenStart(ctx, src, start)
			if err != nil {
				s.searchFailed(err)
				return false, true
			}
			if len(got) == 0 {
				continue
			}
			var keep []tguser.Post
			for _, p := range got {
				file := tguser.SearchHit{
					MessageID: p.ID, Caption: p.Caption, Name: p.Name, Size: p.Size,
					Video: true, Duration: int(p.Duration.Seconds()),
				}
				if ok, why := MatchTitle(q, file); !ok {
					if s.Log != nil {
						s.Log.Info("rejected a file found on Telegram", "tag", tag, "why", why)
					}
					continue
				}
				if ok, why := checkFile(q, file, file.Duration, s.Verify); !ok {
					if s.Log != nil {
						s.Log.Info("rejected a file found on Telegram", "tag", tag, "why", why)
					}
					continue
				}
				keep = append(keep, p)
			}
			if err := s.News.FinishStart(ctx, src, start, keep); err != nil {
				s.searchFailed(err)
				return false, true
			}
			if len(keep) == 0 {
				continue
			}
			s.markSeen(ctx, tag, catalog.SeenDone)
			*copied++
			s.scanLibrary()
			if s.Log != nil {
				s.Log.Info("copied a file found on Telegram", "tag", tag, "channel", src.ChannelID)
			}
			return true, false
		}
	}
	return false, false
}
