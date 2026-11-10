package tgpool

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

// talkMu lets one conversation with an index bot run at a time, so a reply is never read by the
// wrong caller.
var talkMu sync.Mutex

// TalkToBot sends text to another bot from a connected library bot and returns that bot's first
// answer. The signed-in account is not used. It gives up after wait.
func (p *Pool) TalkToBot(ctx context.Context, username, text string, wait time.Duration) (string, error) {
	if p == nil {
		return "", errors.New("no bot is connected")
	}
	talkMu.Lock()
	defer talkMu.Unlock()
	b := p.readyBot()
	if b == nil {
		return "", errors.New("no bot is connected")
	}
	api, _, err := b.conn()
	if err != nil {
		return "", err
	}
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	res, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: strings.TrimPrefix(username, "@")})
	if err != nil {
		return "", err
	}
	peer, ok := res.Peer.(*tg.PeerUser)
	if !ok {
		return "", errors.New("that name is not a bot")
	}
	var in *tg.InputPeerUser
	for _, user := range res.Users {
		u, ok := user.(*tg.User)
		if !ok || u.ID != peer.UserID {
			continue
		}
		if !u.Bot {
			return "", errors.New("that name is not a bot")
		}
		in = &tg.InputPeerUser{UserID: u.ID, AccessHash: u.AccessHash}
		break
	}
	if in == nil {
		return "", errors.New("that name is not a bot")
	}
	up, err := api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer: in, Message: text, RandomID: int64(binary.LittleEndian.Uint64(raw[:])),
	})
	if err != nil {
		return "", err
	}
	sent := talkSentID(up)
	deadline := time.Now().Add(wait)
	for {
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return "", ctx.Err()
		}
		hist, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: in, Limit: 10, MinID: sent})
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
		answer := ""
		for _, msg := range talkMessages(hist) {
			if m, ok := msg.(*tg.Message); ok && !m.Out && m.ID > sent {
				answer = m.Message
			}
		}
		if answer != "" {
			return answer, nil
		}
		if time.Now().After(deadline) {
			return "", errors.New("the bot did not answer")
		}
	}
}

func (p *Pool) readyBot() *bot {
	for _, s := range p.slots {
		b, ok := s.w.(*bot)
		if !ok {
			continue
		}
		if _, _, err := b.conn(); err == nil {
			return b
		}
	}
	return nil
}

func talkMessages(res tg.MessagesMessagesClass) []tg.MessageClass {
	switch v := res.(type) {
	case *tg.MessagesMessages:
		return v.Messages
	case *tg.MessagesMessagesSlice:
		return v.Messages
	case *tg.MessagesChannelMessages:
		return v.Messages
	default:
		return nil
	}
}

func talkSentID(res tg.UpdatesClass) int {
	switch v := res.(type) {
	case *tg.Updates:
		for _, u := range v.Updates {
			switch n := u.(type) {
			case *tg.UpdateNewMessage:
				if m, ok := n.Message.(*tg.Message); ok {
					return m.ID
				}
			case *tg.UpdateMessageID:
				return n.ID
			}
		}
	case *tg.UpdateShortSentMessage:
		return v.ID
	}
	return 0
}
