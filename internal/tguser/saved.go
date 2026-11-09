package tguser

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"

	"github.com/gotd/td/tg"
)

// Saved is one message in Saved Messages newer than a request we sent.
type Saved struct {
	ID   int
	Text string
	Post Post
}

// SendSaved posts text into Saved Messages and returns the new message id.
// The resolver on this same account reads that chat.
func (m *Manager) SendSaved(ctx context.Context, text string) (int, error) {
	lv, err := m.pick(ctx)
	if err != nil {
		return 0, err
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	val, err := m.call(ctx, lv, func(ctx context.Context, api *tg.Client) (any, error) {
		res, err := api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
			Peer:     &tg.InputPeerSelf{},
			Message:  text,
			RandomID: int64(binary.LittleEndian.Uint64(b[:])),
		})
		if err != nil {
			return 0, err
		}
		return sentID(res), nil
	})
	if err != nil {
		return 0, err
	}
	return val.(int), nil
}

// SavedAfter lists Saved Messages newer than afterID, oldest first, up to 100.
func (m *Manager) SavedAfter(ctx context.Context, afterID int) ([]Saved, error) {
	lv, err := m.pick(ctx)
	if err != nil {
		return nil, err
	}
	val, err := m.call(ctx, lv, func(ctx context.Context, api *tg.Client) (any, error) {
		res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer: &tg.InputPeerSelf{}, Limit: 100,
		})
		if err != nil {
			return nil, err
		}
		var out []Saved
		for _, raw := range searchMessages(res) {
			msg, ok := raw.(*tg.Message)
			if !ok || msg.ID <= afterID {
				continue
			}
			item := Saved{ID: msg.ID, Text: msg.Message}
			if p, ok := postFrom(msg); ok {
				item.Post = p
			}
			out = append(out, item)
		}
		// History comes newest first. Callers walk it in arrival order.
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	return val.([]Saved), nil
}

// ForwardSaved copies messages from Saved Messages into the library channel.
// Telegram keeps the file on its own servers. The indexer then sees an ordinary post.
func (m *Manager) ForwardSaved(ctx context.Context, ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	lv, err := m.pick(ctx)
	if err != nil {
		return err
	}
	random := make([]int64, len(ids))
	for i := range random {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return err
		}
		random[i] = int64(binary.LittleEndian.Uint64(b[:]))
	}
	_, err = m.call(ctx, lv, func(ctx context.Context, api *tg.Client) (any, error) {
		req := &tg.MessagesForwardMessagesRequest{
			FromPeer:   &tg.InputPeerSelf{},
			ToPeer:     &tg.InputPeerChannel{ChannelID: m.channelID, AccessHash: lv.meta.AccessHash},
			ID:         ids,
			RandomID:   random,
			DropAuthor: true,
		}
		_, err := api.MessagesForwardMessages(ctx, req)
		if err == nil {
			lv.clock.hold(forwardGap(len(ids)))
		}
		return nil, err
	})
	m.did(lv, "forward", fmt.Sprintf("%d saved", len(ids)), err)
	return err
}
