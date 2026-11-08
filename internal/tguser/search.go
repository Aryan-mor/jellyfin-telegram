package tguser

import (
	"context"

	"github.com/gotd/td/tg"
)

// SearchHit is one file a global hashtag search returned.
type SearchHit struct {
	ChannelID  int64
	AccessHash int64
	MessageID  int
	Caption    string
	Size       int64
	Video      bool
	NoForward  bool
	// Duration is the running time in seconds, or zero when Telegram gave none.
	Duration int
}

// SearchHashtag searches every chat this account can see for query, which is a hashtag such as
// #tm_m_603. Only document files are returned. The library channel itself is left out.
func (m *Manager) SearchHashtag(ctx context.Context, query string) ([]SearchHit, error) {
	if m == nil || query == "" {
		return nil, nil
	}
	lv, err := m.pick(ctx)
	if err != nil {
		return nil, err
	}
	val, err := m.call(ctx, lv, func(ctx context.Context, api *tg.Client) (any, error) {
		res, err := api.MessagesSearchGlobal(ctx, &tg.MessagesSearchGlobalRequest{
			Q:          query,
			Filter:     &tg.InputMessagesFilterDocument{},
			OffsetPeer: &tg.InputPeerEmpty{},
			Limit:      20,
		})
		if err != nil {
			return nil, err
		}
		channels := map[int64]int64{}
		for _, raw := range messageChats(res) {
			ch, ok := raw.(*tg.Channel)
			if ok {
				channels[ch.ID] = ch.AccessHash
			}
		}
		var hits []SearchHit
		for _, raw := range searchMessages(res) {
			msg, ok := raw.(*tg.Message)
			if !ok {
				continue
			}
			peer, ok := msg.PeerID.(*tg.PeerChannel)
			if !ok || peer.ChannelID == m.channelID {
				continue
			}
			post, ok := postFrom(msg)
			if !ok || post.Animated || post.Round {
				continue
			}
			hits = append(hits, SearchHit{
				ChannelID: peer.ChannelID, AccessHash: channels[peer.ChannelID],
				MessageID: post.ID, Caption: post.Caption, Size: post.Size,
				Video:     post.MIME == "video/mp4" || post.MIME == "video/x-matroska" || post.MIME == "video/webm" || post.Duration > 0,
				NoForward: post.NoForward, Duration: int(post.Duration.Seconds()),
			})
		}
		return hits, nil
	})
	if err != nil {
		return nil, err
	}
	hits, _ := val.([]SearchHit)
	return hits, nil
}

func messageChats(res tg.MessagesMessagesClass) []tg.ChatClass {
	switch v := res.(type) {
	case *tg.MessagesMessages:
		return v.Chats
	case *tg.MessagesMessagesSlice:
		return v.Chats
	case *tg.MessagesChannelMessages:
		return v.Chats
	default:
		return nil
	}
}
