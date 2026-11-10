package tgsearch

import (
	"context"

	"github.com/Aryan-mor/jellyfin-telegram/internal/tgsource"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// nextMessage reads the single message posted under a file.
type nextMessage interface {
	Next(ctx context.Context, ref tguser.SourceRef, afterID int) (tguser.Post, bool, error)
}

// withSubtitle is the video message plus the subtitle directly under it, when that message
// belongs to the same file. A missing lookup still copies the video.
func (s *Service) withSubtitle(ctx context.Context, hit tguser.SearchHit) []int {
	ids := []int{hit.MessageID}
	next, ok := s.TG.(nextMessage)
	if !ok || hit.MessageID == 0 {
		return ids
	}
	below, found, err := next.Next(ctx, tguser.SourceRef{ChannelID: hit.ChannelID, AccessHash: hit.AccessHash}, hit.MessageID)
	if err != nil || !found || below.NoForward {
		if err != nil {
			s.warn("subtitle under the file", err)
		}
		return ids
	}
	video := tguser.Post{ID: hit.MessageID, DocID: hit.DocID, Name: hit.Name, Caption: hit.Caption}
	if tgsource.SubtitleFor(video, below) {
		ids = append(ids, below.ID)
	}
	return ids
}
