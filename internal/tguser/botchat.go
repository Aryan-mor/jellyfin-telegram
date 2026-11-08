package tguser

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// talkMu lets one conversation with a bot run at a time, so a reply is never read by the wrong
// caller.
var talkMu sync.Mutex

// TalkToBot sends text to a bot from the signed-in account and returns the bot's first answer. It
// gives up after wait.
func (m *Manager) TalkToBot(ctx context.Context, username, text string, wait time.Duration) (string, error) {
	talkMu.Lock()
	defer talkMu.Unlock()
	lv, err := m.pick(ctx)
	if err != nil {
		return "", err
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	type sent struct {
		peer *tg.InputPeerUser
		id   int
	}
	val, err := m.call(ctx, lv, func(ctx context.Context, api *tg.Client) (any, error) {
		res, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: strings.TrimPrefix(username, "@")})
		if err != nil {
			return nil, err
		}
		peer, ok := res.Peer.(*tg.PeerUser)
		if !ok {
			return nil, errors.New("that name is not a bot")
		}
		for _, raw := range res.Users {
			u, ok := raw.(*tg.User)
			if !ok || u.ID != peer.UserID {
				continue
			}
			if !u.Bot {
				return nil, errors.New("that name is not a bot")
			}
			in := &tg.InputPeerUser{UserID: u.ID, AccessHash: u.AccessHash}
			up, err := api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
				Peer: in, Message: text, RandomID: int64(binary.LittleEndian.Uint64(b[:])),
			})
			if err != nil {
				return nil, err
			}
			return sent{peer: in, id: sentID(up)}, nil
		}
		return nil, errors.New("that name is not a bot")
	})
	if err != nil {
		return "", err
	}
	s := val.(sent)

	deadline := time.Now().Add(wait)
	for {
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return "", ctx.Err()
		}
		val, err := m.call(ctx, lv, func(ctx context.Context, api *tg.Client) (any, error) {
			res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: s.peer, Limit: 10, MinID: s.id})
			if err != nil {
				return nil, err
			}
			// History is newest first. The answer to our message is the oldest one after it.
			answer := ""
			for _, raw := range searchMessages(res) {
				if msg, ok := raw.(*tg.Message); ok && !msg.Out && msg.ID > s.id {
					answer = msg.Message
				}
			}
			return answer, nil
		})
		if err != nil {
			if d, ok := tgerr.AsFloodWait(err); ok && time.Now().Add(d).Before(deadline) {
				select {
				case <-time.After(d):
					continue
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
			return "", err
		}
		if a := val.(string); a != "" {
			return a, nil
		}
		if time.Now().After(deadline) {
			return "", errors.New("the bot did not answer")
		}
	}
}

// PostRef names one post in a public channel.
type PostRef struct {
	Channel string
	Message int
}

// PostsByLink reads posts of public channels by name, with one call per channel. It returns the
// file posts among them as search hits. A post that is gone or is not a file is left out.
func (m *Manager) PostsByLink(ctx context.Context, refs []PostRef) ([]SearchHit, error) {
	if m == nil || len(refs) == 0 {
		return nil, nil
	}
	lv, err := m.pick(ctx)
	if err != nil {
		return nil, err
	}
	byChannel := map[string][]int{}
	var order []string
	for _, r := range refs {
		if _, seen := byChannel[r.Channel]; !seen {
			order = append(order, r.Channel)
		}
		byChannel[r.Channel] = append(byChannel[r.Channel], r.Message)
	}
	var hits []SearchHit
	for _, name := range order {
		ids := byChannel[name]
		for len(ids) > 0 {
			n := min(len(ids), 100)
			batch := ids[:n]
			ids = ids[n:]
			val, err := m.call(ctx, lv, func(ctx context.Context, api *tg.Client) (any, error) {
				return fetchPosts(ctx, api, name, batch)
			})
			if err != nil {
				if errors.Is(err, errNoChannel) {
					break
				}
				return hits, err
			}
			hits = append(hits, val.([]SearchHit)...)
		}
	}
	return hits, nil
}

var errNoChannel = errors.New("no such public channel")

func fetchPosts(ctx context.Context, api *tg.Client, name string, ids []int) ([]SearchHit, error) {
	res, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: name})
	if err != nil {
		if tgerr.Is(err, "USERNAME_NOT_OCCUPIED", "USERNAME_INVALID") {
			return nil, errNoChannel
		}
		return nil, err
	}
	var ch *tg.Channel
	if peer, ok := res.Peer.(*tg.PeerChannel); ok {
		for _, raw := range res.Chats {
			if c, ok := raw.(*tg.Channel); ok && c.ID == peer.ChannelID {
				ch = c
			}
		}
	}
	if ch == nil {
		return nil, errNoChannel
	}
	input := make([]tg.InputMessageClass, len(ids))
	for i, id := range ids {
		input[i] = &tg.InputMessageID{ID: id}
	}
	msgs, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}, ID: input,
	})
	if err != nil {
		if tgerr.Is(err, "CHANNEL_INVALID", "CHANNEL_PRIVATE") {
			return nil, errNoChannel
		}
		return nil, err
	}
	var hits []SearchHit
	for _, raw := range searchMessages(msgs) {
		msg, ok := raw.(*tg.Message)
		if !ok {
			continue
		}
		post, ok := postFrom(msg)
		if !ok || post.Animated || post.Round {
			continue
		}
		hits = append(hits, hitFrom(ch.ID, ch.AccessHash, post))
	}
	return hits, nil
}
