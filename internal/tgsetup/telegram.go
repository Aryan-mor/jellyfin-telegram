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
		Record(">> " + text + " (" + err.Error() + ")")
		return 0, err
	}
	Record(">> " + text)
	return sentID(res), nil
}

func (c *botFatherChat) Reply(ctx context.Context, after int) (string, error) {
	m, err := c.readAfter(ctx, after)
	if err != nil {
		Record("<< " + err.Error())
		return "", err
	}
	Record("<< " + firstLine(reToken.ReplaceAllString(m.Message, "[token]")))
	return m.Message, nil
}

func (c *botFatherChat) Marked(ctx context.Context, after int) (string, int, []Button, error) {
	m, err := c.readAfter(ctx, after)
	if err != nil {
		return "", 0, nil, err
	}
	return m.Message, m.ID, buttonsOf(m), nil
}

// Press pushes an inline button. BotFather either edits that message or sends a new one.
func (c *botFatherChat) Press(ctx context.Context, msgID int, data []byte) (string, int, []Button, error) {
	prev, prevKeys := "", ""
	if msgs, err := c.history(ctx, msgID-1); err == nil {
		for _, raw := range msgs {
			if m, ok := raw.(*tg.Message); ok && m.ID == msgID {
				prev = m.Message
				prevKeys = buttonKey(buttonsOf(m))
			}
		}
	}
	req := &tg.MessagesGetBotCallbackAnswerRequest{Peer: c.peer, MsgID: msgID}
	req.SetData(data)
	if _, err := c.api.MessagesGetBotCallbackAnswer(ctx, req); err != nil {
		return "", 0, nil, err
	}
	deadline := time.Now().Add(c.wait)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", 0, nil, ctx.Err()
		case <-time.After(700 * time.Millisecond):
		}
		msgs, err := c.history(ctx, msgID-1)
		if err != nil {
			return "", 0, nil, err
		}
		var edited, newer *tg.Message
		for _, raw := range msgs {
			m, ok := raw.(*tg.Message)
			if !ok || m.Out {
				continue
			}
			if m.ID == msgID && (m.Message != prev || buttonKey(buttonsOf(m)) != prevKeys) {
				edited = m
			}
			if m.ID > msgID && (newer == nil || m.ID < newer.ID) {
				newer = m
			}
		}
		if edited != nil {
			return edited.Message, edited.ID, buttonsOf(edited), nil
		}
		if newer != nil {
			return newer.Message, newer.ID, buttonsOf(newer), nil
		}
	}
	return "", 0, nil, ErrBotFatherSilent
}

func (c *botFatherChat) readAfter(ctx context.Context, after int) (*tg.Message, error) {
	deadline := time.Now().Add(c.wait)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(700 * time.Millisecond):
		}
		msgs, err := c.history(ctx, after)
		if err != nil {
			return nil, err
		}
		// History comes newest first. The answer to our message is the oldest incoming one after it.
		var best *tg.Message
		for _, raw := range msgs {
			m, ok := raw.(*tg.Message)
			if !ok || m.Out || m.ID <= after {
				continue
			}
			if best == nil || m.ID < best.ID {
				best = m
			}
		}
		if best != nil {
			return best, nil
		}
	}
	return nil, ErrBotFatherSilent
}

func (c *botFatherChat) history(ctx context.Context, minID int) ([]tg.MessageClass, error) {
	res, err := c.api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: c.peer, MinID: minID, Limit: 15})
	if err != nil {
		return nil, err
	}
	return messagesOf(res), nil
}

func buttonsOf(m *tg.Message) []Button {
	if m == nil || m.ReplyMarkup == nil {
		return nil
	}
	var out []Button
	switch mk := m.ReplyMarkup.(type) {
	case *tg.ReplyInlineMarkup:
		for _, row := range mk.Rows {
			for _, b := range row.Buttons {
				btn := Button{Text: b.Text}
				if cb, ok := b.Type.(*tg.InlineButtonTypeCallback); ok {
					btn.Data = append([]byte(nil), cb.Data...)
				}
				out = append(out, btn)
			}
		}
	case *tg.ReplyKeyboardMarkup:
		for _, row := range mk.Rows {
			for _, b := range row.Buttons {
				out = append(out, Button{Text: b.Text})
			}
		}
	}
	return out
}

func buttonKey(buttons []Button) string {
	var b strings.Builder
	for _, btn := range buttons {
		b.WriteString(btn.Text)
		b.WriteByte(0)
	}
	return b.String()
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

// archiveFolder is Telegram's Archive. Folder 0 is the main chat list.
const archiveFolder = 1

// ArchiveChannel moves a channel into Archive. It is done once, when the channel is created.
// Taking it back out later is left to the person; nothing puts it back.
func ArchiveChannel(ctx context.Context, api *tg.Client, channelID, accessHash int64) error {
	_, err := api.FoldersEditPeerFolders(ctx, []tg.InputFolderPeer{{
		Peer:     &tg.InputPeerChannel{ChannelID: channelID, AccessHash: accessHash},
		FolderID: archiveFolder,
	}})
	if err != nil {
		return fmt.Errorf("move the channel to Archive: %w", err)
	}
	return nil
}

// muteForever is Telegram's "mute until" for a mute with no end.
const muteForever = 2147483647

// MuteChannel silences a channel for this account for good. Like Archive it is done once, when the
// channel is first set up; turning notifications back on later is left to the person.
func MuteChannel(ctx context.Context, api *tg.Client, channelID, accessHash int64) error {
	_, err := api.AccountUpdateNotifySettings(ctx, &tg.AccountUpdateNotifySettingsRequest{
		Peer:     &tg.InputNotifyPeer{Peer: &tg.InputPeerChannel{ChannelID: channelID, AccessHash: accessHash}},
		Settings: tg.InputPeerNotifySettings{MuteUntil: muteForever},
	})
	if err != nil {
		return fmt.Errorf("mute the channel: %w", err)
	}
	return nil
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

// LibraryReport describes every channel whose title contains "jellyfin", in the main list and in
// Archive, and names the bot admins. It is the check that the channel and the bots really exist.
func LibraryReport(ctx context.Context, api *tg.Client) ([]string, error) {
	var lines []string
	for _, folder := range []int{0, 1} {
		where := "chats"
		if folder == 1 {
			where = "archive"
		}
		channels, err := channelsInFolder(ctx, api, folder)
		if err != nil {
			return lines, err
		}
		for _, ch := range channels {
			if !strings.Contains(strings.ToLower(ch.Title), "jellyfin") {
				continue
			}
			admins, err := BotAdmins(ctx, api, ch.ID, ch.AccessHash)
			if err != nil {
				lines = append(lines, fmt.Sprintf("channel %q id=%d in %s: %v", ch.Title, ch.ID, where, err))
				continue
			}
			names := make([]string, 0, len(admins))
			for _, a := range admins {
				if a.Username != "" {
					names = append(names, "@"+a.Username)
				}
			}
			if len(names) == 0 {
				names = []string{"none"}
			}
			lines = append(lines, fmt.Sprintf("channel %q id=%d in %s admins=%s", ch.Title, ch.ID, where, strings.Join(names, " ")))
		}
	}
	return lines, nil
}

func channelsInFolder(ctx context.Context, api *tg.Client, folder int) ([]*tg.Channel, error) {
	req := &tg.MessagesGetDialogsRequest{OffsetPeer: &tg.InputPeerEmpty{}, Limit: 100}
	if folder != 0 {
		req.SetFolderID(folder)
	}
	res, err := api.MessagesGetDialogs(ctx, req)
	if err != nil {
		return nil, err
	}
	var chats []tg.ChatClass
	switch v := res.(type) {
	case *tg.MessagesDialogs:
		chats = v.Chats
	case *tg.MessagesDialogsSlice:
		chats = v.Chats
	default:
		return nil, nil
	}
	var out []*tg.Channel
	seen := map[int64]bool{}
	for _, raw := range chats {
		ch, ok := raw.(*tg.Channel)
		if !ok || ch.AccessHash == 0 || seen[ch.ID] {
			continue
		}
		seen[ch.ID] = true
		out = append(out, ch)
	}
	return out, nil
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
