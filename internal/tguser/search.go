package tguser

import (
	"context"
	"fmt"
	"strings"

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
	// Name is the file name when the message has one.
	Name string
}

// TextPost is one channel post found by a text search. Links are the bot start URLs in it.
type TextPost struct {
	ChannelID  int64
	AccessHash int64
	MessageID  int
	Text       string
	Links      []string
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
			hits = append(hits, hitFrom(peer.ChannelID, channels[peer.ChannelID], post))
		}
		lv.clock.hold(searchGap)
		return hits, nil
	})
	if err != nil {
		m.did(lv, "search", query, err)
		return nil, err
	}
	hits, _ := val.([]SearchHit)
	m.did(lv, "search", fmt.Sprintf("%s · %d", query, len(hits)), nil)
	return hits, nil
}

// hitFrom turns a file post of a channel into a search hit.
func hitFrom(channelID, accessHash int64, post Post) SearchHit {
	return SearchHit{
		ChannelID: channelID, AccessHash: accessHash,
		MessageID: post.ID, Caption: post.Caption, Name: post.Name, Size: post.Size,
		Video:     post.MIME == "video/mp4" || post.MIME == "video/x-matroska" || post.MIME == "video/webm" || post.Duration > 0,
		NoForward: post.NoForward, Duration: int(post.Duration.Seconds()),
	}
}

// SearchChannel searches one channel for query and returns its video and document files.
// It is the in-channel form of SearchHashtag.
func (m *Manager) SearchChannel(ctx context.Context, ref SourceRef, query string, limit int) ([]SearchHit, error) {
	query = strings.TrimSpace(query)
	if m == nil || query == "" || (ref.ChannelID == 0 && ref.Username == "") {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	lv, err := m.pick(ctx)
	if err != nil {
		return nil, err
	}
	val, err := m.call(ctx, lv, func(ctx context.Context, api *tg.Client) (any, error) {
		peer, err := lv.peer(ctx, api, ref)
		if err != nil {
			return nil, err
		}
		if peer.ChannelID == m.channelID {
			return []SearchHit(nil), nil
		}
		res, err := api.MessagesSearch(ctx, &tg.MessagesSearchRequest{
			Peer:   peer,
			Q:      query,
			Filter: &tg.InputMessagesFilterDocument{},
			Limit:  limit,
		})
		if err != nil {
			return nil, err
		}
		var hits []SearchHit
		for _, raw := range searchMessages(res) {
			msg, ok := raw.(*tg.Message)
			if !ok {
				continue
			}
			post, ok := postFrom(msg)
			if !ok || post.Animated || post.Round {
				continue
			}
			hits = append(hits, hitFrom(peer.ChannelID, peer.AccessHash, post))
		}
		lv.clock.hold(searchGap)
		return hits, nil
	})
	if err != nil {
		m.did(lv, "search channel", query, err)
		return nil, err
	}
	hits, _ := val.([]SearchHit)
	m.did(lv, "search channel", fmt.Sprintf("%s · %d", query, len(hits)), nil)
	return hits, nil
}

// SearchPosts searches one channel for text posts. News channels post a start link instead of a file.
func (m *Manager) SearchPosts(ctx context.Context, ref SourceRef, query string, limit int) ([]TextPost, error) {
	query = strings.TrimSpace(query)
	if m == nil || query == "" || (ref.ChannelID == 0 && ref.Username == "") {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	lv, err := m.pick(ctx)
	if err != nil {
		return nil, err
	}
	val, err := m.call(ctx, lv, func(ctx context.Context, api *tg.Client) (any, error) {
		peer, err := lv.peer(ctx, api, ref)
		if err != nil {
			return nil, err
		}
		res, err := api.MessagesSearch(ctx, &tg.MessagesSearchRequest{
			Peer:   peer,
			Q:      query,
			Filter: &tg.InputMessagesFilterEmpty{},
			Limit:  limit,
		})
		if err != nil {
			return nil, err
		}
		var posts []TextPost
		for _, raw := range searchMessages(res) {
			msg, ok := raw.(*tg.Message)
			if !ok || msg.ID == 0 {
				continue
			}
			links := StartURLs(msg.Message, append(textURL(msg), buttonURLs(msg)...))
			if strings.TrimSpace(msg.Message) == "" && len(links) == 0 {
				continue
			}
			posts = append(posts, TextPost{
				ChannelID: peer.ChannelID, AccessHash: peer.AccessHash,
				MessageID: msg.ID, Text: msg.Message, Links: links,
			})
		}
		lv.clock.hold(searchGap)
		return posts, nil
	})
	if err != nil {
		m.did(lv, "search posts", query, err)
		return nil, err
	}
	posts, _ := val.([]TextPost)
	m.did(lv, "search posts", fmt.Sprintf("%s · %d", query, len(posts)), nil)
	return posts, nil
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
