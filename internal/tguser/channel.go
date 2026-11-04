package tguser

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gotd/td/tg"
)

// errNoDialog means the account has no chat with the channel.
var errNoDialog = errors.New("this account has no dialog for the channel; open the channel once in Telegram")

// channelAccess finds the channel in the account's dialogs and returns its access hash.
// A hash of zero, which is what the bots use, does not work for a user account.
func channelAccess(ctx context.Context, api *tg.Client, channelID int64) (int64, error) {
	ch, err := findChannel(ctx, api, channelID)
	if err != nil {
		return 0, err
	}
	return ch.AccessHash, nil
}

// findChannel looks for the channel among the account's dialogs.
func findChannel(ctx context.Context, api *tg.Client, channelID int64) (*tg.Channel, error) {
	var offsetPeer tg.InputPeerClass = &tg.InputPeerEmpty{}
	var offsetDate, offsetID int
	for page := 0; page < 30; page++ {
		res, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			OffsetPeer: offsetPeer,
			OffsetDate: offsetDate,
			OffsetID:   offsetID,
			Limit:      100,
		})
		if err != nil {
			return nil, fmt.Errorf("read dialogs: %w", err)
		}
		chats, dialogs, messages := dialogParts(res)
		if ch, ok := channelFromChats(chats, channelID); ok {
			return ch, nil
		}
		if len(dialogs) < 100 {
			break
		}
		last := dialogs[len(dialogs)-1]
		offsetID = last.TopMessage
		offsetPeer = peerFromDialog(last, chats)
		offsetDate = messageDate(messages, last.TopMessage)
		if offsetPeer == nil {
			break
		}
	}
	return nil, errNoDialog
}

func channelFromChats(chats []tg.ChatClass, channelID int64) (*tg.Channel, bool) {
	for _, raw := range chats {
		ch, ok := raw.(*tg.Channel)
		if ok && ch.ID == channelID && ch.AccessHash != 0 {
			return ch, true
		}
	}
	return nil, false
}

func hashFromChats(chats []tg.ChatClass, channelID int64) (int64, bool) {
	for _, raw := range chats {
		ch, ok := raw.(*tg.Channel)
		if ok && ch.ID == channelID && ch.AccessHash != 0 {
			return ch.AccessHash, true
		}
	}
	return 0, false
}

func dialogParts(res tg.MessagesDialogsClass) ([]tg.ChatClass, []tg.Dialog, []tg.MessageClass) {
	switch v := res.(type) {
	case *tg.MessagesDialogs:
		return v.Chats, onlyDialogs(v.Dialogs), v.Messages
	case *tg.MessagesDialogsSlice:
		return v.Chats, onlyDialogs(v.Dialogs), v.Messages
	default:
		return nil, nil, nil
	}
}

func onlyDialogs(in []tg.DialogClass) []tg.Dialog {
	var out []tg.Dialog
	for _, d := range in {
		if v, ok := d.(*tg.Dialog); ok {
			out = append(out, *v)
		}
	}
	return out
}

func peerFromDialog(d tg.Dialog, chats []tg.ChatClass) tg.InputPeerClass {
	switch p := d.Peer.(type) {
	case *tg.PeerChannel:
		if hash, ok := hashFromChats(chats, p.ChannelID); ok {
			return &tg.InputPeerChannel{ChannelID: p.ChannelID, AccessHash: hash}
		}
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: p.ChatID}
	case *tg.PeerUser:
		return &tg.InputPeerUser{UserID: p.UserID}
	}
	return nil
}

func messageDate(messages []tg.MessageClass, id int) int {
	for _, raw := range messages {
		if m, ok := raw.(*tg.Message); ok && m.ID == id {
			return m.Date
		}
	}
	return int(time.Now().Unix())
}

// canPost reports whether the signed-in user may post in the channel.
func canPost(ctx context.Context, api *tg.Client, channelID, accessHash int64) (bool, error) {
	res, err := api.ChannelsGetParticipant(ctx, &tg.ChannelsGetParticipantRequest{
		Channel:     &tg.InputChannel{ChannelID: channelID, AccessHash: accessHash},
		Participant: &tg.InputPeerSelf{},
	})
	if err != nil {
		return false, fmt.Errorf("read channel membership: %w", err)
	}
	switch p := res.Participant.(type) {
	case *tg.ChannelParticipantCreator:
		return true, nil
	case *tg.ChannelParticipantAdmin:
		return p.AdminRights.PostMessages, nil
	default:
		return false, nil
	}
}
