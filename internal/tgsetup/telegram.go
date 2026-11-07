package tgsetup

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/tg"
)

// botFatherChat talks to @BotFather through a user session. The session runs without updates, so
// answers are read from the chat history.
type botFatherChat struct {
	api  *tg.Client
	peer tg.InputPeerClass
	// wait is how long to look for an answer.
	wait time.Duration
}

func newBotFatherChat(ctx context.Context, api *tg.Client) (*botFatherChat, error) {
	res, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: "BotFather"})
	if err != nil {
		return nil, fmt.Errorf("find @BotFather: %w", err)
	}
	for _, raw := range res.Users {
		if u, ok := raw.(*tg.User); ok && u.Bot {
			return &botFatherChat{api: api, peer: &tg.InputPeerUser{UserID: u.ID, AccessHash: u.AccessHash}, wait: 25 * time.Second}, nil
		}
	}
	return nil, errors.New("@BotFather was not found")
}

func (c *botFatherChat) Send(ctx context.Context, text string) (int, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	res, err := c.api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer: c.peer, Message: text, RandomID: int64(binary.LittleEndian.Uint64(b[:])),
	})
	if err != nil {
		return 0, err
	}
	return sentID(res), nil
}

func (c *botFatherChat) Reply(ctx context.Context, after int) (string, error) {
	deadline := time.Now().Add(c.wait)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(700 * time.Millisecond):
		}
		res, err := c.api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: c.peer, MinID: after, Limit: 10})
		if err != nil {
			return "", err
		}
		// History comes newest first. The answer to our message is the oldest incoming one after it.
		var text string
		best := 0
		for _, raw := range messagesOf(res) {
			m, ok := raw.(*tg.Message)
			if !ok || m.Out || m.ID <= after {
				continue
			}
			if best == 0 || m.ID < best {
				best, text = m.ID, m.Message
			}
		}
		if best != 0 {
			return text, nil
		}
	}
	return "", ErrBotFatherSilent
}

func messagesOf(res tg.MessagesMessagesClass) []tg.MessageClass {
	switch r := res.(type) {
	case *tg.MessagesMessages:
		return r.Messages
	case *tg.MessagesMessagesSlice:
		return r.Messages
	case *tg.MessagesChannelMessages:
		return r.Messages
	}
	return nil
}

func sentID(res tg.UpdatesClass) int {
	switch u := res.(type) {
	case *tg.UpdateShortSentMessage:
		return u.ID
	case *tg.Updates:
		for _, raw := range u.Updates {
			if m, ok := raw.(*tg.UpdateMessageID); ok {
				return m.ID
			}
		}
	}
	return 0
}

// Created is a channel this account just made.
type Created struct {
	ID         int64
	AccessHash int64
	Title      string
}

// CreateChannel makes a private broadcast channel. The account becomes its owner.
func CreateChannel(ctx context.Context, api *tg.Client, title, about string) (Created, error) {
	res, err := api.ChannelsCreateChannel(ctx, &tg.ChannelsCreateChannelRequest{Broadcast: true, Title: title, About: about})
	if err != nil {
		return Created{}, fmt.Errorf("create the channel: %w", err)
	}
	if u, ok := res.(*tg.Updates); ok {
		for _, raw := range u.Chats {
			if ch, ok := raw.(*tg.Channel); ok {
				return Created{ID: ch.ID, AccessHash: ch.AccessHash, Title: ch.Title}, nil
			}
		}
	}
	return Created{}, errors.New("Telegram did not return the new channel")
}

// BotUser is a bot as a Telegram user.
type BotUser struct {
	ID         int64
	AccessHash int64
	Username   string
}

// ResolveBot finds a bot by username.
func ResolveBot(ctx context.Context, api *tg.Client, username string) (BotUser, error) {
	name := strings.TrimPrefix(username, "@")
	res, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: name})
	if err != nil {
		return BotUser{}, fmt.Errorf("find @%s: %w", name, err)
	}
	for _, raw := range res.Users {
		if u, ok := raw.(*tg.User); ok && u.Bot {
			return BotUser{ID: u.ID, AccessHash: u.AccessHash, Username: u.Username}, nil
		}
	}
	return BotUser{}, fmt.Errorf("@%s is not a bot", name)
}

// PromoteBot makes a bot an admin that can post, edit and delete in the channel.
func PromoteBot(ctx context.Context, api *tg.Client, channelID, accessHash int64, bot BotUser) error {
	_, err := api.ChannelsEditAdmin(ctx, &tg.ChannelsEditAdminRequest{
		Channel: &tg.InputChannel{ChannelID: channelID, AccessHash: accessHash},
		UserID:  &tg.InputUser{UserID: bot.ID, AccessHash: bot.AccessHash},
		AdminRights: tg.ChatAdminRights{
			PostMessages: true, EditMessages: true, DeleteMessages: true,
		},
		Rank: "library",
	})
	if err != nil {
		return fmt.Errorf("make @%s an admin: %w", bot.Username, err)
	}
	return nil
}

// BotAdmins lists the bots that are admins of the channel.
func BotAdmins(ctx context.Context, api *tg.Client, channelID, accessHash int64) ([]BotUser, error) {
	res, err := api.ChannelsGetParticipants(ctx, &tg.ChannelsGetParticipantsRequest{
		Channel: &tg.InputChannel{ChannelID: channelID, AccessHash: accessHash},
		Filter:  &tg.ChannelParticipantsAdmins{},
		Limit:   200,
	})
	if err != nil {
		return nil, fmt.Errorf("list the channel admins: %w", err)
	}
	list, ok := res.(*tg.ChannelsChannelParticipants)
	if !ok {
		return nil, errors.New("Telegram did not list the admins")
	}
	var out []BotUser
	for _, raw := range list.Users {
		if u, ok := raw.(*tg.User); ok && u.Bot {
			out = append(out, BotUser{ID: u.ID, AccessHash: u.AccessHash, Username: u.Username})
		}
	}
	return out, nil
}
