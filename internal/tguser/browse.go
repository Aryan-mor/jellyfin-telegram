package tguser

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"
)

// ErrNoFreeSession means no signed-in account that can post is free right now. A long upload
// keeps its account busy, so the caller tries again later.
var ErrNoFreeSession = errors.New("no signed-in account is free right now")

// SourceRef names a channel to read from. Username is empty for a private channel.
type SourceRef struct {
	ChannelID int64
	Username  string
	// AccessHash is set when a search already learned it. Zero means it still has to be looked up.
	AccessHash int64
}

// Channel describes a channel found for a link, username or id.
type Channel struct {
	ID         int64
	Username   string
	Title      string
	NoForwards bool
}

// StartHit is one bot deep link in a channel post. One post can carry several.
type StartHit struct {
	ID   int
	Date time.Time
	URL  string
}

// Post is one document in a source channel.
type Post struct {
	ID        int
	Date      time.Time
	DocID     int64
	Size      int64
	Name      string
	Caption   string
	MIME      string
	Duration  time.Duration
	Round     bool
	Animated  bool
	NoForward bool
}

var (
	reUsername = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{3,31}$`)
	reNumeric  = regexp.MustCompile(`^-?\d{5,}$`)
	reLinkID   = regexp.MustCompile(`t\.me/c/(\d+)`)
	reLinkName = regexp.MustCompile(`t\.me/(?:s/)?([A-Za-z][A-Za-z0-9_]{3,31})(?:[/?#]|$)`)
)

// ParseChannelRef reads what a person typed: a username, a t.me link, a link to one message, or a
// numeric id. Exactly one of id and username is set.
func ParseChannelRef(input string) (id int64, username string, err error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return 0, "", errors.New("enter a channel username, link or id")
	}
	if strings.Contains(s, "t.me/+") || strings.Contains(s, "joinchat") {
		return 0, "", errors.New("an invite link cannot be used. Join the channel with this account in Telegram, then give its numeric id (send a message from it to @id_bot) or its @username")
	}
	if m := reLinkID.FindStringSubmatch(s); m != nil {
		n, _ := strconv.ParseInt(m[1], 10, 64)
		return n, "", nil
	}
	if reNumeric.MatchString(s) {
		s = strings.TrimPrefix(strings.TrimPrefix(s, "-"), "100")
		n, perr := strconv.ParseInt(s, 10, 64)
		if perr != nil || n <= 0 {
			return 0, "", errors.New("the id must look like -1001234567890")
		}
		return n, "", nil
	}
	if m := reLinkName.FindStringSubmatch(s); m != nil {
		return 0, m[1], nil
	}
	name := strings.TrimPrefix(s, "@")
	if reUsername.MatchString(name) {
		return 0, name, nil
	}
	return 0, "", errors.New("that is not a channel username, link or id")
}

type userKey struct{}

// WithUser asks later reads and forwards on this context to use one signed-in account.
// Zero keeps the usual choice, any free admin.
func WithUser(ctx context.Context, id int64) context.Context {
	if id == 0 {
		return ctx
	}
	return context.WithValue(ctx, userKey{}, id)
}

func (m *Manager) pick(ctx context.Context) (*live, error) {
	id, _ := ctx.Value(userKey{}).(int64)
	if id == 0 {
		return m.idle()
	}
	lv, _, err := m.get(id)
	if err != nil {
		return nil, errors.New("that account is not signed in")
	}
	if !lv.connected.Load() {
		return nil, errors.New("that account is not connected")
	}
	if lv.busy.Load() {
		return nil, ErrNoFreeSession
	}
	return lv, nil
}

// idle picks a healthy session that is not uploading.
func (m *Manager) idle() (*live, error) {
	for _, v := range m.Healthy() {
		lv, _, err := m.get(v.UserID)
		if err == nil && !lv.busy.Load() {
			return lv, nil
		}
	}
	return nil, ErrNoFreeSession
}

// HasAccount reports whether any signed-in account can post in the library channel.
func (m *Manager) HasAccount() bool { return len(m.Healthy()) > 0 }

// HasFreeSession reports whether a source channel could be read right now.
func (m *Manager) HasFreeSession() bool {
	_, err := m.idle()
	return err == nil
}

// peer finds the access hash this account needs. The hash belongs to one account, so it is looked up
// on the account that makes the call. It runs inside the session's own loop.
func (lv *live) peer(ctx context.Context, api *tg.Client, ref SourceRef) (*tg.InputPeerChannel, error) {
	if lv.hashes == nil {
		lv.hashes = map[int64]int64{}
	}
	if ref.AccessHash != 0 {
		lv.hashes[ref.ChannelID] = ref.AccessHash
		return &tg.InputPeerChannel{ChannelID: ref.ChannelID, AccessHash: ref.AccessHash}, nil
	}
	if h, ok := lv.hashes[ref.ChannelID]; ok {
		return &tg.InputPeerChannel{ChannelID: ref.ChannelID, AccessHash: h}, nil
	}
	ch, err := resolveChannel(ctx, api, ref)
	if err != nil {
		return nil, err
	}
	lv.hashes[ch.ID] = ch.AccessHash
	return &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}, nil
}

func resolveChannel(ctx context.Context, api *tg.Client, ref SourceRef) (*tg.Channel, error) {
	if ref.Username != "" {
		res, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: ref.Username})
		if err != nil {
			if strings.Contains(err.Error(), "USERNAME_NOT_OCCUPIED") || strings.Contains(err.Error(), "USERNAME_INVALID") {
				return nil, fmt.Errorf("no channel is called @%s", ref.Username)
			}
			return nil, err
		}
		for _, raw := range res.Chats {
			if ch, ok := raw.(*tg.Channel); ok && (ref.ChannelID == 0 || ch.ID == ref.ChannelID) {
				return ch, nil
			}
		}
		return nil, fmt.Errorf("@%s is not a channel or group", ref.Username)
	}
	ch, err := findChannel(ctx, api, ref.ChannelID)
	if errors.Is(err, errNoDialog) {
		return nil, errors.New("this account is not a member of that channel. Join it in Telegram with the signed-in account first")
	}
	return ch, err
}

// Lookup finds a channel from what the person typed.
func (m *Manager) Lookup(ctx context.Context, input string) (Channel, error) {
	id, name, err := ParseChannelRef(input)
	if err != nil {
		return Channel{}, err
	}
	lv, err := m.pick(ctx)
	if err != nil {
		return Channel{}, err
	}
	val, err := m.call(ctx, lv, func(ctx context.Context, api *tg.Client) (any, error) {
		ch, err := resolveChannel(ctx, api, SourceRef{ChannelID: id, Username: name})
		if err != nil {
			return nil, err
		}
		if lv.hashes == nil {
			lv.hashes = map[int64]int64{}
		}
		lv.hashes[ch.ID] = ch.AccessHash
		un, _ := ch.GetUsername()
		return Channel{ID: ch.ID, Username: un, Title: ch.Title, NoForwards: ch.Noforwards}, nil
	})
	if err != nil {
		return Channel{}, err
	}
	return val.(Channel), nil
}

// Page is one slice of a channel's history.
type Page struct {
	// Posts holds the documents on the page, newest first. Messages without a document are left out.
	Posts []Post
	// Links are bot start URLs found in the same page. The post itself is not a video.
	Links []StartHit
	// Highest and Lowest are the newest and oldest message ids on the page, documents or not.
	Highest, Lowest int
	// Oldest is the date of the oldest message on the page.
	Oldest time.Time
	// More is true when Telegram may have older messages.
	More bool
}

// History returns up to limit messages older than offsetID, newest first. Zero starts at the newest.
func (m *Manager) History(ctx context.Context, ref SourceRef, offsetID, limit int) (Page, error) {
	lv, err := m.pick(ctx)
	if err != nil {
		return Page{}, err
	}
	val, err := m.call(ctx, lv, func(ctx context.Context, api *tg.Client) (any, error) {
		peer, err := lv.peer(ctx, api, ref)
		if err != nil {
			return nil, err
		}
		res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: peer, OffsetID: offsetID, Limit: limit})
		if err != nil {
			delete(lv.hashes, ref.ChannelID)
			return nil, err
		}
		msgs := searchMessages(res)
		out := Page{More: len(msgs) >= limit}
		see := func(id, date int) {
			if out.Lowest == 0 || id < out.Lowest {
				out.Lowest = id
			}
			out.Highest = max(out.Highest, id)
			if date > 0 {
				if t := time.Unix(int64(date), 0).UTC(); out.Oldest.IsZero() || t.Before(out.Oldest) {
					out.Oldest = t
				}
			}
		}
		for _, raw := range msgs {
			switch msg := raw.(type) {
			case *tg.Message:
				see(msg.ID, msg.Date)
				if p, ok := postFrom(msg); ok {
					out.Posts = append(out.Posts, p)
				}
				for _, url := range StartURLs(msg.Message, append(textURL(msg), buttonURLs(msg)...)) {
					out.Links = append(out.Links, StartHit{
						ID: msg.ID, Date: time.Unix(int64(msg.Date), 0).UTC(), URL: url,
					})
				}
			case *tg.MessageService:
				see(msg.ID, msg.Date)
			case *tg.MessageEmpty:
				see(msg.ID, 0)
			}
		}
		return out, nil
	})
	if err != nil {
		return Page{}, err
	}
	return val.(Page), nil
}

func postFrom(msg *tg.Message) (Post, bool) {
	media, ok := msg.Media.(*tg.MessageMediaDocument)
	if !ok {
		return Post{}, false
	}
	doc, ok := media.Document.(*tg.Document)
	if !ok {
		return Post{}, false
	}
	p := Post{
		ID: msg.ID, Date: time.Unix(int64(msg.Date), 0).UTC(), DocID: doc.ID, Size: doc.Size,
		Caption: msg.Message, MIME: doc.MimeType, NoForward: msg.Noforwards,
	}
	for _, attr := range doc.Attributes {
		switch a := attr.(type) {
		case *tg.DocumentAttributeFilename:
			p.Name = a.FileName
		case *tg.DocumentAttributeVideo:
			p.Duration = time.Duration(a.Duration * float64(time.Second))
			p.Round = a.RoundMessage
		case *tg.DocumentAttributeAnimated:
			p.Animated = true
		}
	}
	if p.Name == "" {
		p.Name = "video" + strconv.Itoa(msg.ID) + extFor(doc.MimeType)
	}
	return p, true
}

func extFor(mime string) string {
	switch strings.ToLower(mime) {
	case "video/mp4":
		return ".mp4"
	case "video/x-matroska":
		return ".mkv"
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	}
	return ""
}

// Copy posts the listed messages of a source channel into the library channel without the author
// line. Telegram copies the file on its own servers, so nothing is downloaded or uploaded here.
func (m *Manager) Copy(ctx context.Context, ref SourceRef, ids []int) error {
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
		from, err := lv.peer(ctx, api, ref)
		if err != nil {
			return nil, err
		}
		req := &tg.MessagesForwardMessagesRequest{
			FromPeer:   from,
			ToPeer:     &tg.InputPeerChannel{ChannelID: m.channelID, AccessHash: lv.meta.AccessHash},
			ID:         ids,
			RandomID:   random,
			DropAuthor: true,
		}
		_, err = api.MessagesForwardMessages(ctx, req)
		return nil, err
	})
	return err
}
