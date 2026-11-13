package tgsearch

import (
	"context"

	"github.com/Aryan-mor/jellyfin-telegram/internal/subtitles"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// Remote searches Telegram for a subtitle file. A nil manager searches nothing.
func Remote(m *tguser.Manager) subtitles.RemoteSource {
	if m == nil {
		return nil
	}
	return telegramRemote{m}
}

type telegramRemote struct{ m *tguser.Manager }

func (t telegramRemote) Search(ctx context.Context, query string) ([]subtitles.RemoteHit, error) {
	hits, err := t.m.SearchHashtag(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]subtitles.RemoteHit, len(hits))
	for i, h := range hits {
		out[i] = subtitles.RemoteHit{
			Name: h.Name, Caption: h.Caption, Video: h.Video,
			ChannelID: h.ChannelID, AccessHash: h.AccessHash, MessageID: h.MessageID, Size: h.Size,
		}
	}
	return out, nil
}

func (t telegramRemote) Next(ctx context.Context, channelID, accessHash int64, afterID int) (subtitles.RemoteHit, bool, error) {
	post, ok, err := t.m.Next(ctx, tguser.SourceRef{ChannelID: channelID, AccessHash: accessHash}, afterID)
	if err != nil || !ok {
		return subtitles.RemoteHit{}, false, err
	}
	return subtitles.RemoteHit{
		Name: post.Name, Caption: post.Caption, Video: post.Duration > 0 || post.MIME == "video/mp4",
		ChannelID: channelID, AccessHash: accessHash, MessageID: post.ID, Size: post.Size,
	}, true, nil
}

func (t telegramRemote) Download(ctx context.Context, channelID, accessHash int64, messageID int) (string, []byte, error) {
	return t.m.DownloadMessage(ctx, tguser.SourceRef{ChannelID: channelID, AccessHash: accessHash}, messageID, 0)
}
