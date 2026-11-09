package tguser

import (
	"context"

	"github.com/gotd/td/tg"
)

// Lent is one signed-in account handed to another package. Each Telegram call is one short job on
// that account's own connection loop, so a long task built from many calls does not hold the loop
// and uploads keep their turn between them. The methods are the ones telegram-file-resolver calls.
type Lent struct {
	m  *Manager
	lv *live
}

// Lend picks a free healthy account, or the one WithUser names, and returns it.
func (m *Manager) Lend(ctx context.Context) (*Lent, error) {
	lv, err := m.pick(ctx)
	if err != nil {
		return nil, err
	}
	return &Lent{m: m, lv: lv}, nil
}

// UserID is the Telegram id of the lent account.
func (l *Lent) UserID() int64 { return l.lv.meta.UserID }

// PostFrom reads a video or file message the way the library importer needs it.
func PostFrom(msg *tg.Message) (Post, bool) { return postFrom(msg) }

func lent[T any](ctx context.Context, l *Lent, fn func(context.Context, *tg.Client) (T, error)) (T, error) {
	val, err := l.m.call(ctx, l.lv, func(ctx context.Context, api *tg.Client) (any, error) { return fn(ctx, api) })
	var zero T
	if err != nil {
		return zero, err
	}
	// A link can take several calls. They share this account's clock.
	l.lv.clock.hold(resolverGap)
	out, _ := val.(T)
	return out, nil
}

func (l *Lent) ContactsResolveUsername(ctx context.Context, r *tg.ContactsResolveUsernameRequest) (*tg.ContactsResolvedPeer, error) {
	return lent(ctx, l, func(ctx context.Context, a *tg.Client) (*tg.ContactsResolvedPeer, error) {
		return a.ContactsResolveUsername(ctx, r)
	})
}

func (l *Lent) ContactsBlock(ctx context.Context, r *tg.ContactsBlockRequest) (bool, error) {
	return lent(ctx, l, func(ctx context.Context, a *tg.Client) (bool, error) { return a.ContactsBlock(ctx, r) })
}

func (l *Lent) ContactsUnblock(ctx context.Context, r *tg.ContactsUnblockRequest) (bool, error) {
	return lent(ctx, l, func(ctx context.Context, a *tg.Client) (bool, error) { return a.ContactsUnblock(ctx, r) })
}

func (l *Lent) MessagesSendMessage(ctx context.Context, r *tg.MessagesSendMessageRequest) (tg.UpdatesClass, error) {
	return lent(ctx, l, func(ctx context.Context, a *tg.Client) (tg.UpdatesClass, error) { return a.MessagesSendMessage(ctx, r) })
}

func (l *Lent) MessagesGetHistory(ctx context.Context, r *tg.MessagesGetHistoryRequest) (tg.MessagesMessagesClass, error) {
	return lent(ctx, l, func(ctx context.Context, a *tg.Client) (tg.MessagesMessagesClass, error) {
		return a.MessagesGetHistory(ctx, r)
	})
}

func (l *Lent) MessagesImportChatInvite(ctx context.Context, hash string) (tg.MessagesChatInviteJoinResultClass, error) {
	return lent(ctx, l, func(ctx context.Context, a *tg.Client) (tg.MessagesChatInviteJoinResultClass, error) {
		return a.MessagesImportChatInvite(ctx, hash)
	})
}

func (l *Lent) MessagesCheckChatInvite(ctx context.Context, hash string) (tg.ChatInviteClass, error) {
	return lent(ctx, l, func(ctx context.Context, a *tg.Client) (tg.ChatInviteClass, error) {
		return a.MessagesCheckChatInvite(ctx, hash)
	})
}

func (l *Lent) MessagesForwardMessages(ctx context.Context, r *tg.MessagesForwardMessagesRequest) (tg.UpdatesClass, error) {
	return lent(ctx, l, func(ctx context.Context, a *tg.Client) (tg.UpdatesClass, error) {
		return a.MessagesForwardMessages(ctx, r)
	})
}

func (l *Lent) MessagesDeleteHistory(ctx context.Context, r *tg.MessagesDeleteHistoryRequest) (*tg.MessagesAffectedHistory, error) {
	return lent(ctx, l, func(ctx context.Context, a *tg.Client) (*tg.MessagesAffectedHistory, error) {
		return a.MessagesDeleteHistory(ctx, r)
	})
}

func (l *Lent) MessagesDeleteChatUser(ctx context.Context, r *tg.MessagesDeleteChatUserRequest) (tg.UpdatesClass, error) {
	return lent(ctx, l, func(ctx context.Context, a *tg.Client) (tg.UpdatesClass, error) {
		return a.MessagesDeleteChatUser(ctx, r)
	})
}

func (l *Lent) ChannelsJoinChannel(ctx context.Context, c tg.InputChannelClass) (tg.MessagesChatInviteJoinResultClass, error) {
	return lent(ctx, l, func(ctx context.Context, a *tg.Client) (tg.MessagesChatInviteJoinResultClass, error) {
		return a.ChannelsJoinChannel(ctx, c)
	})
}

func (l *Lent) ChannelsLeaveChannel(ctx context.Context, c tg.InputChannelClass) (tg.UpdatesClass, error) {
	return lent(ctx, l, func(ctx context.Context, a *tg.Client) (tg.UpdatesClass, error) {
		return a.ChannelsLeaveChannel(ctx, c)
	})
}

func (l *Lent) AccountUpdateNotifySettings(ctx context.Context, r *tg.AccountUpdateNotifySettingsRequest) (bool, error) {
	return lent(ctx, l, func(ctx context.Context, a *tg.Client) (bool, error) {
		return a.AccountUpdateNotifySettings(ctx, r)
	})
}
