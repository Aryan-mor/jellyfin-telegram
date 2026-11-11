package tgpool

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
	"github.com/Aryan-mor/jellyfin-telegram/internal/sign"
)

const (
	startStagger = 150 * time.Millisecond
	restartDelay = time.Minute
)

var attachmentExtensions = map[string]bool{
	".srt": true, ".ass": true, ".ssa": true, ".vtt": true, ".zip": true,
}

var videoExtensions = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".webm": true,
	".ts": true, ".m2ts": true, ".wmv": true, ".flv": true, ".mpg": true, ".mpeg": true,
}

var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
	".heic": true, ".heif": true, ".tif": true, ".tiff": true, ".bmp": true,
}

var audioExtensions = map[string]bool{
	".mp3": true, ".flac": true, ".m4a": true, ".aac": true,
	".ogg": true, ".opus": true, ".wav": true, ".wma": true,
}

// Options configures a Telegram-backed pool.
type Options struct {
	APIID       int
	APIHash     string
	ChannelID   int64
	Tokens      []string
	Concurrency int
	SessionDir  string
	Log         *slog.Logger
}

// New builds a pool with one connection per bot token.
func New(o Options) (*Pool, error) {
	if err := os.MkdirAll(o.SessionDir, 0o700); err != nil {
		return nil, err
	}
	bots := make([]*bot, 0, len(o.Tokens))
	workers := make([]worker, 0, len(o.Tokens))
	for _, token := range o.Tokens {
		id, err := config.BotID(token)
		if err != nil {
			return nil, err
		}
		b := &bot{
			id:          id,
			token:       token,
			apiID:       o.APIID,
			apiHash:     o.APIHash,
			channelID:   o.ChannelID,
			sessionPath: filepath.Join(o.SessionDir, strconv.FormatInt(id, 10)+".json"),
			locations:   map[int]location{},
			settledCh:   make(chan struct{}),
			log:         o.Log.With("bot", id),
		}
		bots = append(bots, b)
		workers = append(workers, b)
	}
	p := newPool(workers, o.Concurrency, o.Log)
	p.start = func(ctx context.Context) {
		go func() {
			for _, b := range bots {
				go b.run(ctx)
				select {
				case <-time.After(startStagger):
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	return p, nil
}

type location struct {
	docID int64
	input *tg.InputDocumentFileLocation
	doc   *tg.Document
}

// bot is one MTProto connection. File references belong to the account that received
// them, so every bot looks up each message for itself.
type bot struct {
	id          int64
	token       string
	apiID       int
	apiHash     string
	channelID   int64
	sessionPath string
	log         *slog.Logger

	mu          sync.RWMutex
	api         *tg.Client
	channel     *tg.InputChannel
	st          BotStatus
	locations   map[int]location
	dests       map[int64]*tg.Channel
	extra       map[int64]*tg.InputChannel
	extraLoc    map[string]location
	settledOnce sync.Once
	settledCh   chan struct{}
}

func (b *bot) settled() <-chan struct{} { return b.settledCh }

func (b *bot) status() BotStatus {
	b.mu.RLock()
	defer b.mu.RUnlock()
	st := b.st
	st.ID = b.id
	return st
}

func (b *bot) run(ctx context.Context) {
	for ctx.Err() == nil {
		err := b.connect(ctx)
		b.mu.Lock()
		b.st.Ready = false
		if err != nil && ctx.Err() == nil {
			b.st.Err = err
		}
		b.mu.Unlock()
		b.settledOnce.Do(func() { close(b.settledCh) })
		if ctx.Err() != nil {
			return
		}
		if tgerr.Is(err, "ACCESS_TOKEN_INVALID", "ACCESS_TOKEN_EXPIRED", "API_ID_INVALID") {
			b.log.Error("bot stopped", "err", err)
			return
		}
		b.log.Warn("bot disconnected, retrying", "err", err, "delay", restartDelay)
		select {
		case <-time.After(restartDelay):
		case <-ctx.Done():
			return
		}
	}
}

func (b *bot) connect(ctx context.Context) error {
	client := telegram.NewClient(b.apiID, b.apiHash, telegram.Options{
		NoUpdates:      true,
		SessionStorage: &session.FileStorage{Path: b.sessionPath},
	})
	return client.Run(ctx, func(ctx context.Context) error {
		st, err := client.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("auth status: %w", err)
		}
		if !st.Authorized {
			if _, err := client.Auth().Bot(ctx, b.token); err != nil {
				return fmt.Errorf("bot login: %w", err)
			}
		}
		self, err := client.Self(ctx)
		if err != nil {
			return fmt.Errorf("get self: %w", err)
		}
		api := client.API()
		channel, err := resolveChannel(ctx, api, b.channelID)
		if err != nil {
			b.mu.Lock()
			b.st.Username = self.Username
			b.mu.Unlock()
			return err
		}
		_, isAdmin := channel.GetAdminRights()
		b.mu.Lock()
		b.api = api
		b.channel = channel.AsInput()
		b.st = BotStatus{
			Username:     self.Username,
			Ready:        true,
			ChannelTitle: channel.Title,
			IsAdmin:      isAdmin || channel.Creator,
		}
		b.mu.Unlock()
		b.settledOnce.Do(func() { close(b.settledCh) })
		b.log.Info("bot connected", "username", self.Username)
		<-ctx.Done()
		return ctx.Err()
	})
}

// Results of looking at one bot in one channel.
const (
	CheckOK       = "ok"
	CheckNotAdmin = "not_admin"
	CheckNoPost   = "no_post"
	CheckOffline  = "offline"
)

// checkChannel asks Telegram what this bot can do in a channel right now. Channel 0 is the main
// channel. For the main channel the answer also refreshes the admin flag the bot shows.
func (b *bot) checkChannel(ctx context.Context, id int64) (state, detail string) {
	api, _, err := b.conn()
	if err != nil {
		return CheckOffline, "the bot is not connected"
	}
	if id == 0 || id == b.channelID {
		ch, err := resolveChannel(ctx, api, b.channelID)
		if err != nil {
			return CheckNotAdmin, err.Error()
		}
		_, admin := ch.GetAdminRights()
		admin = admin || ch.Creator
		b.mu.Lock()
		b.st.IsAdmin = admin
		b.st.ChannelTitle = ch.Title
		b.mu.Unlock()
		if !admin {
			return CheckNotAdmin, "the bot is not an admin of the main channel"
		}
		return CheckOK, ""
	}
	if !b.status().IsAdmin {
		return CheckNotAdmin, "the bot is not an admin of the main channel"
	}
	ch, err := resolveChannel(ctx, api, id)
	if err != nil {
		return CheckNotAdmin, "the bot cannot see this channel"
	}
	if !canPost(ch) {
		return CheckNoPost, "the bot is in the channel but may not post"
	}
	return CheckOK, ""
}

// resolveChannel finds the channel. Bots may address a channel they belong to with an
// access hash of zero, which is why only the id is needed in the configuration.
func resolveChannel(ctx context.Context, api *tg.Client, id int64) (*tg.Channel, error) {
	res, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{&tg.InputChannel{ChannelID: id}})
	if err != nil {
		return nil, fmt.Errorf("cannot see channel %d, is the bot an admin of it? %w", id, err)
	}
	for _, c := range res.GetChats() {
		if ch, ok := c.(*tg.Channel); ok && ch.ID == id {
			return ch, nil
		}
	}
	return nil, fmt.Errorf("channel %d is not visible to this bot", id)
}

// openChannel finds a channel this bot may post in. The bot must also be an admin of the main
// channel. username is used when the id is not known yet.
func (b *bot) openChannel(ctx context.Context, id int64, username string) (*tg.Channel, error) {
	api, _, err := b.conn()
	if err != nil {
		return nil, errors.New("that bot is not connected")
	}
	st := b.status()
	if !st.IsAdmin {
		return nil, errors.New("that bot is not an admin of the main channel")
	}
	if username == "" && id != 0 {
		if ch := b.cachedDest(id); ch != nil {
			return ch, nil
		}
	}
	var ch *tg.Channel
	if username != "" {
		res, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
		if err != nil {
			return nil, fmt.Errorf("cannot see @%s: %w", username, err)
		}
		for _, raw := range res.Chats {
			if found, ok := raw.(*tg.Channel); ok {
				ch = found
				break
			}
		}
		if ch == nil {
			return nil, fmt.Errorf("@%s is not a channel", username)
		}
	} else {
		ch, err = resolveChannel(ctx, api, id)
		if err != nil {
			return nil, err
		}
	}
	if ch.ID == b.channelID {
		return nil, errors.New("that is the main channel")
	}
	if !canPost(ch) {
		name := ch.Title
		if name == "" {
			name = fmt.Sprintf("%d", ch.ID)
		}
		who := st.Username
		if who == "" {
			who = fmt.Sprintf("%d", b.id)
		}
		return nil, fmt.Errorf("%w: @%s must be an admin of %s, with permission to post, and an admin of the main channel", ErrNoMirrorAccess, strings.TrimPrefix(who, "@"), name)
	}
	if username == "" {
		b.rememberDest(ch)
	}
	return ch, nil
}

func canPost(ch *tg.Channel) bool {
	if ch.Creator {
		return true
	}
	// In a supergroup every member may post unless sending is banned for all members. The right to
	// post messages is a channel right and a supergroup admin does not carry it.
	if ch.Megagroup {
		if banned, ok := ch.GetDefaultBannedRights(); ok && banned.SendMessages {
			return false
		}
		return !ch.Left
	}
	rights, ok := ch.GetAdminRights()
	return ok && rights.PostMessages
}

func (b *bot) cachedDest(id int64) *tg.Channel {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.dests[id]
}

func (b *bot) rememberDest(ch *tg.Channel) {
	if ch == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.dests == nil {
		b.dests = map[int64]*tg.Channel{}
	}
	b.dests[ch.ID] = ch
}

func (b *bot) forgetDest(id int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.dests, id)
	delete(b.extra, id)
}

// forwardTo copies one main-channel message into dest without the author line.
func (b *bot) forwardTo(ctx context.Context, messageID int, dest *tg.Channel) (int, error) {
	api, channel, err := b.conn()
	if err != nil {
		return 0, err
	}
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, err
	}
	res, err := api.MessagesForwardMessages(ctx, &tg.MessagesForwardMessagesRequest{
		FromPeer:   &tg.InputPeerChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash},
		ToPeer:     &tg.InputPeerChannel{ChannelID: dest.ID, AccessHash: dest.AccessHash},
		ID:         []int{messageID},
		RandomID:   []int64{int64(binary.LittleEndian.Uint64(raw[:]))},
		DropAuthor: true,
	})
	if err != nil {
		return 0, err
	}
	return sentMessageID(res), nil
}

func (b *bot) conn() (*tg.Client, *tg.InputChannel, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.api == nil || !b.st.Ready {
		return nil, nil, errors.New("bot is not connected")
	}
	return b.api, b.channel, nil
}

func (b *bot) invalidate(messageID int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.locations, messageID)
}

func (b *bot) location(ctx context.Context, messageID int, docID int64) (location, error) {
	b.mu.RLock()
	loc, ok := b.locations[messageID]
	b.mu.RUnlock()
	if ok && (docID == 0 || loc.docID == docID) {
		return loc, nil
	}
	api, channel, err := b.conn()
	if err != nil {
		return location{}, err
	}
	res, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: channel,
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}},
	})
	if err != nil {
		return location{}, fmt.Errorf("channels.getMessages: %w", err)
	}
	var doc *tg.Document
	for _, m := range rawMessages(res) {
		if msg, ok := m.(*tg.Message); ok && msg.ID == messageID {
			doc = documentOf(msg)
		}
	}
	if doc == nil || (docID != 0 && doc.ID != docID) {
		return location{}, fmt.Errorf("%w: message %d", ErrMessageGone, messageID)
	}
	loc = location{docID: doc.ID, input: doc.AsInputDocumentFileLocation(""), doc: doc}
	b.mu.Lock()
	b.locations[messageID] = loc
	b.mu.Unlock()
	return loc, nil
}

func (b *bot) getBlock(ctx context.Context, messageID int, docID, offset int64, limit int) ([]byte, error) {
	loc, err := b.location(ctx, messageID, docID)
	if err != nil {
		return nil, err
	}
	api, _, err := b.conn()
	if err != nil {
		return nil, err
	}
	return readFile(ctx, api, loc.input, offset, limit)
}

func (b *bot) thumbnail(ctx context.Context, messageID int) ([]byte, error) {
	loc, err := b.location(ctx, messageID, 0)
	if err != nil {
		return nil, err
	}
	thumb := largestThumb(loc.doc)
	if thumb == "" {
		return nil, nil
	}
	api, _, err := b.conn()
	if err != nil {
		return nil, err
	}
	return readFile(ctx, api, loc.doc.AsInputDocumentFileLocation(thumb), 0, int(media.BlockSize))
}

func (b *bot) messages(ctx context.Context, ids []int) ([]media.Message, error) {
	api, channel, err := b.conn()
	if err != nil {
		return nil, err
	}
	input := make([]tg.InputMessageClass, len(ids))
	for i, id := range ids {
		input[i] = &tg.InputMessageID{ID: id}
	}
	res, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: channel, ID: input})
	if err != nil {
		return nil, fmt.Errorf("channels.getMessages: %w", err)
	}
	found := map[int]media.Message{}
	hashes := userHashes(res)
	for _, raw := range rawMessages(res) {
		switch m := raw.(type) {
		case *tg.Message:
			found[m.ID] = messageFromTG(m, b.channelID, false, hashes)
		case *tg.MessageService:
			found[m.ID] = media.Message{ID: m.ID, Exists: true}
		}
	}
	out := make([]media.Message, 0, len(ids))
	for _, id := range ids {
		if m, ok := found[id]; ok {
			out = append(out, m)
		} else {
			out = append(out, media.Message{ID: id})
		}
	}
	return out, nil
}

func (b *bot) sendDocument(ctx context.Context, name, caption string, data []byte, replyTo int) (int, error) {
	api, channel, err := b.conn()
	if err != nil {
		return 0, err
	}
	file, err := uploader.NewUploader(api).FromBytes(ctx, name, data)
	if err != nil {
		return 0, fmt.Errorf("upload: %w", err)
	}
	var randomID [8]byte
	if _, err := rand.Read(randomID[:]); err != nil {
		return 0, err
	}
	req := &tg.MessagesSendMediaRequest{
		Peer: &tg.InputPeerChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash},
		Media: &tg.InputMediaUploadedDocument{
			File:       file,
			MimeType:   "application/zip",
			Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: name}},
		},
		Message:  sign.Fit(caption),
		RandomID: int64(binary.LittleEndian.Uint64(randomID[:])),
	}
	if replyTo > 0 {
		req.SetReplyTo(&tg.InputReplyToMessage{ReplyToMsgID: replyTo})
	}
	res, err := api.MessagesSendMedia(ctx, req)
	if err != nil {
		return 0, fmt.Errorf("messages.sendMedia: %w", err)
	}
	if id := sentMessageID(res); id != 0 {
		return id, nil
	}
	return 0, errors.New("telegram did not report the new message id")
}

func sentMessageID(res tg.UpdatesClass) int {
	updates, ok := res.(*tg.Updates)
	if !ok {
		return 0
	}
	for _, u := range updates.Updates {
		switch v := u.(type) {
		case *tg.UpdateNewChannelMessage:
			if m, ok := v.Message.(*tg.Message); ok {
				return m.ID
			}
		case *tg.UpdateMessageID:
			return v.ID
		}
	}
	return 0
}

func (b *bot) deleteMessages(ctx context.Context, ids []int) error {
	api, channel, err := b.conn()
	if err != nil {
		return err
	}
	_, err = api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{Channel: channel, ID: ids})
	return err
}

func readFile(ctx context.Context, api *tg.Client, loc tg.InputFileLocationClass, offset int64, limit int) ([]byte, error) {
	req := &tg.UploadGetFileRequest{Location: loc, Offset: offset, Limit: limit}
	// Precise allows any 1 KB aligned read inside a 1 MiB chunk. CDN support is left
	// off, so Telegram serves bytes directly and never answers with a redirect.
	req.SetPrecise(true)
	res, err := api.UploadGetFile(ctx, req)
	if err != nil {
		return nil, err
	}
	file, ok := res.(*tg.UploadFile)
	if !ok {
		return nil, fmt.Errorf("unexpected upload.getFile answer %T", res)
	}
	return file.Bytes, nil
}

func userHashes(res tg.MessagesMessagesClass) map[int64]int64 {
	var users []tg.UserClass
	switch m := res.(type) {
	case *tg.MessagesChannelMessages:
		users = m.Users
	case *tg.MessagesMessages:
		users = m.Users
	case *tg.MessagesMessagesSlice:
		users = m.Users
	}
	out := map[int64]int64{}
	for _, raw := range users {
		user, ok := raw.(*tg.User)
		if ok {
			out[user.ID] = user.AccessHash
		}
	}
	return out
}

func rawMessages(res tg.MessagesMessagesClass) []tg.MessageClass {
	switch m := res.(type) {
	case *tg.MessagesChannelMessages:
		return m.Messages
	case *tg.MessagesMessages:
		return m.Messages
	case *tg.MessagesMessagesSlice:
		return m.Messages
	}
	return nil
}

func documentOf(m *tg.Message) *tg.Document {
	if m == nil || m.Media == nil {
		return nil
	}
	md, ok := m.Media.(*tg.MessageMediaDocument)
	if !ok || md.Document == nil {
		return nil
	}
	doc, ok := md.Document.(*tg.Document)
	if !ok {
		return nil
	}
	return doc
}

func (b *bot) messagesIn(ctx context.Context, channelID int64, ids []int) ([]media.Message, error) {
	if channelID == 0 || channelID == b.channelID {
		return b.messages(ctx, ids)
	}
	api, _, err := b.conn()
	if err != nil {
		return nil, err
	}
	channel, err := b.inputFor(ctx, api, channelID)
	if err != nil {
		return nil, err
	}
	input := make([]tg.InputMessageClass, len(ids))
	for i, id := range ids {
		input[i] = &tg.InputMessageID{ID: id}
	}
	res, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: channel, ID: input})
	if err != nil {
		return nil, fmt.Errorf("channels.getMessages: %w", err)
	}
	found := map[int]media.Message{}
	hashes := userHashes(res)
	for _, raw := range rawMessages(res) {
		switch m := raw.(type) {
		case *tg.Message:
			found[m.ID] = messageFromTG(m, channelID, true, hashes)
		case *tg.MessageService:
			found[m.ID] = media.Message{ID: m.ID, Exists: true}
		}
	}
	out := make([]media.Message, 0, len(ids))
	for _, id := range ids {
		if m, ok := found[id]; ok {
			out = append(out, m)
		} else {
			out = append(out, media.Message{ID: id})
		}
	}
	return out, nil
}

func (b *bot) inputFor(ctx context.Context, api *tg.Client, channelID int64) (*tg.InputChannel, error) {
	b.mu.RLock()
	if in, ok := b.extra[channelID]; ok {
		b.mu.RUnlock()
		return in, nil
	}
	b.mu.RUnlock()
	ch, err := resolveChannel(ctx, api, channelID)
	if err != nil {
		return nil, err
	}
	in := ch.AsInput()
	b.mu.Lock()
	if b.extra == nil {
		b.extra = map[int64]*tg.InputChannel{}
	}
	b.extra[channelID] = in
	b.mu.Unlock()
	return in, nil
}

func (b *bot) getBlockIn(ctx context.Context, channelID int64, messageID int, docID, offset int64, limit int) ([]byte, error) {
	if channelID == 0 || channelID == b.channelID {
		return b.getBlock(ctx, messageID, docID, offset, limit)
	}
	loc, err := b.locationIn(ctx, channelID, messageID, docID)
	if err != nil {
		return nil, err
	}
	api, _, err := b.conn()
	if err != nil {
		return nil, err
	}
	return readFile(ctx, api, loc.input, offset, limit)
}

func (b *bot) locationIn(ctx context.Context, channelID int64, messageID int, docID int64) (location, error) {
	key := strconv.FormatInt(channelID, 10) + ":" + strconv.Itoa(messageID)
	b.mu.RLock()
	loc, ok := b.extraLoc[key]
	b.mu.RUnlock()
	if ok && (docID == 0 || loc.docID == docID) {
		return loc, nil
	}
	api, _, err := b.conn()
	if err != nil {
		return location{}, err
	}
	channel, err := b.inputFor(ctx, api, channelID)
	if err != nil {
		return location{}, err
	}
	res, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: channel,
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}},
	})
	if err != nil {
		return location{}, fmt.Errorf("channels.getMessages: %w", err)
	}
	var doc *tg.Document
	for _, m := range rawMessages(res) {
		if msg, ok := m.(*tg.Message); ok && msg.ID == messageID {
			doc = documentOf(msg)
		}
	}
	if doc == nil || (docID != 0 && doc.ID != docID) {
		return location{}, fmt.Errorf("%w: message %d", ErrMessageGone, messageID)
	}
	loc = location{docID: doc.ID, input: doc.AsInputDocumentFileLocation(""), doc: doc}
	b.mu.Lock()
	if b.extraLoc == nil {
		b.extraLoc = map[string]location{}
	}
	b.extraLoc[key] = loc
	b.mu.Unlock()
	return loc, nil
}

func fileFromMessage(m *tg.Message, channelID int64, photos bool) *media.File {
	doc := documentOf(m)
	if doc == nil {
		return nil
	}
	name := ""
	isVideoAttr := false
	isAudio := false
	voice := false
	for _, a := range doc.Attributes {
		switch v := a.(type) {
		case *tg.DocumentAttributeFilename:
			name = v.FileName
		case *tg.DocumentAttributeVideo:
			isVideoAttr = !v.RoundMessage
		case *tg.DocumentAttributeAudio:
			if v.Voice {
				voice = true
			} else {
				isAudio = true
			}
		}
	}
	ext := strings.ToLower(path.Ext(name))
	isVideo := isVideoAttr || strings.HasPrefix(doc.MimeType, "video/") || videoExtensions[ext]
	if !isAudio && !voice && (strings.HasPrefix(doc.MimeType, "audio/") || audioExtensions[ext]) {
		isAudio = true
	}
	isImage := photos && (strings.HasPrefix(doc.MimeType, "image/") || imageExtensions[ext])
	// A voice note is not music. A music video stays a video. The personal channel also keeps photos sent as files.
	if !isVideo && !isImage && (!isAudio || voice) {
		return nil
	}
	if photos && !isVideo && !isImage {
		return nil
	}
	from, reply := messageParts(m)
	return &media.File{
		MessageID: m.ID,
		ChannelID: channelID,
		DocID:     doc.ID,
		Size:      doc.Size,
		MIME:      doc.MimeType,
		FileName:  name,
		Caption:   m.Message,
		Date:      time.Unix(int64(m.Date), 0).UTC(),
		HasThumb:  largestThumb(doc) != "",
		FromID:    from,
		ReplyTo:   reply,
		Edited:    editedAt(m),
	}
}

func messageFromTG(m *tg.Message, channelID int64, photos bool, hashes map[int64]int64) media.Message {
	from, reply := messageParts(m)
	msg := media.Message{
		ID: m.ID, Exists: true, Text: m.Message,
		FromID: from, ReplyTo: reply,
		AccessHash: hashes[from],
		Date:       time.Unix(int64(m.Date), 0).UTC(),
		Edited:     editedAt(m),
		File:       fileFromMessage(m, channelID, photos),
		Attachment: attachmentFromMessage(m),
	}
	if msg.File != nil {
		msg.File.AccessHash = msg.AccessHash
	}
	return msg
}

// editedAt is when a message was last edited, or the zero time.
func editedAt(m *tg.Message) time.Time {
	if d, ok := m.GetEditDate(); ok && d > 0 {
		return time.Unix(int64(d), 0).UTC()
	}
	return time.Time{}
}

func messageParts(m *tg.Message) (from int64, reply int) {
	if u, ok := m.FromID.(*tg.PeerUser); ok {
		from = u.UserID
	}
	if h, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok {
		reply = h.ReplyToMsgID
	}
	return from, reply
}

func attachmentFromMessage(m *tg.Message) *media.Attachment {
	doc := documentOf(m)
	if doc == nil {
		return nil
	}
	name := ""
	for _, a := range doc.Attributes {
		if v, ok := a.(*tg.DocumentAttributeFilename); ok {
			name = v.FileName
		}
	}
	if !attachmentExtensions[strings.ToLower(path.Ext(name))] {
		return nil
	}
	att := &media.Attachment{MessageID: m.ID, DocID: doc.ID, Size: doc.Size, FileName: name}
	if h, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok {
		att.ReplyTo = h.ReplyToMsgID
	}
	return att
}

func (b *bot) peer() (*tg.InputPeerChannel, *tg.InputChannel, *tg.Client, error) {
	api, channel, err := b.conn()
	if err != nil {
		return nil, nil, nil, err
	}
	return &tg.InputPeerChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash}, channel, api, nil
}

func randomID() (int64, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return 0, err
	}
	return int64(binary.LittleEndian.Uint64(buf[:])), nil
}

// setOwnAbout publishes the platform key in this bot's About text and keeps the rest.
func (b *bot) setOwnAbout(ctx context.Context, line string) error {
	api, _, err := b.conn()
	if err != nil {
		return err
	}
	full, err := api.UsersGetFullUser(ctx, &tg.InputUserSelf{})
	if err != nil {
		return fmt.Errorf("read about: %w", err)
	}
	about, _ := full.FullUser.GetAbout()
	req := &tg.BotsSetBotInfoRequest{}
	req.SetAbout(sign.MergeAbout(about, line))
	if _, err := api.BotsSetBotInfo(ctx, req); err != nil {
		return fmt.Errorf("bots.setBotInfo: %w", err)
	}
	return nil
}

// about reads another account's About text from a message it sent in this chat.
func (b *bot) about(ctx context.Context, userID, accessHash int64) (string, error) {
	api, _, err := b.conn()
	if err != nil {
		return "", err
	}
	var input tg.InputUserClass
	if userID == b.id || userID == 0 {
		input = &tg.InputUserSelf{}
	} else if accessHash != 0 {
		input = &tg.InputUser{UserID: userID, AccessHash: accessHash}
	} else {
		return "", errors.New("no access hash for that account")
	}
	full, err := api.UsersGetFullUser(ctx, input)
	if err != nil {
		return "", fmt.Errorf("users.getFullUser: %w", err)
	}
	about, _ := full.FullUser.GetAbout()
	return about, nil
}

// publicKeyOf reads the library key published in a bot's About text.
func (b *bot) publicKeyOf(ctx context.Context, username string) (ed25519.PublicKey, error) {
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	if username == "" {
		return nil, errors.New("enter a bot username")
	}
	api, _, err := b.conn()
	if err != nil {
		return nil, err
	}
	res, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", username, err)
	}
	var user *tg.User
	for _, raw := range res.Users {
		u, ok := raw.(*tg.User)
		if ok && strings.EqualFold(u.Username, username) {
			user = u
			break
		}
	}
	if user == nil {
		return nil, errors.New("telegram did not return that account")
	}
	if !user.Bot {
		return nil, errors.New("that account is not a bot")
	}
	about, err := b.about(ctx, user.ID, user.AccessHash)
	if err != nil {
		return nil, err
	}
	pub, ok := sign.PublicFromAbout(about)
	if !ok {
		return nil, errors.New("that bot has no library key in its About text")
	}
	return pub, nil
}

func (b *bot) sendText(ctx context.Context, text string) (int, error) {
	peer, _, api, err := b.peer()
	if err != nil {
		return 0, err
	}
	id, err := randomID()
	if err != nil {
		return 0, err
	}
	req := &tg.MessagesSendMessageRequest{Peer: peer, Message: text, RandomID: id}
	res, err := api.MessagesSendMessage(ctx, req)
	if err != nil {
		return 0, fmt.Errorf("messages.sendMessage: %w", err)
	}
	if got := sentMessageID(res); got != 0 {
		return got, nil
	}
	return 0, errors.New("telegram did not report the new message id")
}

func (b *bot) react(ctx context.Context, messageID int, emoji string) error {
	peer, _, api, err := b.peer()
	if err != nil {
		return err
	}
	req := &tg.MessagesSendReactionRequest{Peer: peer, MsgID: messageID}
	req.SetReaction([]tg.ReactionClass{&tg.ReactionEmoji{Emoticon: emoji}})
	if _, err := api.MessagesSendReaction(ctx, req); err != nil {
		return fmt.Errorf("messages.sendReaction: %w", err)
	}
	return nil
}

// repostDocument copies a document already in this channel as a new post, with a new caption.
// Telegram keeps the same document id.
func (b *bot) repostDocument(ctx context.Context, sourceID int, caption string) (int, error) {
	peer, channel, api, err := b.peer()
	if err != nil {
		return 0, err
	}
	res, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: channel,
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: sourceID}},
	})
	if err != nil {
		return 0, fmt.Errorf("channels.getMessages: %w", err)
	}
	var doc *tg.Document
	for _, raw := range rawMessages(res) {
		msg, ok := raw.(*tg.Message)
		if ok && msg.ID == sourceID {
			doc = documentOf(msg)
		}
	}
	if doc == nil {
		return 0, fmt.Errorf("%w: message %d", ErrMessageGone, sourceID)
	}
	id, err := randomID()
	if err != nil {
		return 0, err
	}
	req := &tg.MessagesSendMediaRequest{
		Peer:     peer,
		Media:    &tg.InputMediaDocument{ID: doc.AsInput()},
		Message:  sign.Fit(caption),
		RandomID: id,
	}
	sent, err := api.MessagesSendMedia(ctx, req)
	if err != nil {
		return 0, fmt.Errorf("messages.sendMedia: %w", err)
	}
	if got := sentMessageID(sent); got != 0 {
		return got, nil
	}
	return 0, errors.New("telegram did not report the new message id")
}

func (b *bot) editCaption(ctx context.Context, messageID int, caption string) error {
	peer, _, api, err := b.peer()
	if err != nil {
		return err
	}
	req := &tg.MessagesEditMessageRequest{Peer: peer, ID: messageID}
	req.SetMessage(sign.Fit(caption))
	if _, err := api.MessagesEditMessage(ctx, req); err != nil {
		// Telegram refuses an edit that changes nothing. The caption is already what we want.
		if tgerr.Is(err, "MESSAGE_NOT_MODIFIED") {
			return nil
		}
		return fmt.Errorf("messages.editMessage: %w", err)
	}
	return nil
}

// largestThumb returns the type of the biggest downloadable thumbnail.
func largestThumb(doc *tg.Document) string {
	best, bestArea := "", 0
	for _, t := range doc.Thumbs {
		switch v := t.(type) {
		case *tg.PhotoSize:
			if a := v.W * v.H; a > bestArea {
				best, bestArea = v.Type, a
			}
		case *tg.PhotoSizeProgressive:
			if a := v.W * v.H; a > bestArea {
				best, bestArea = v.Type, a
			}
		}
	}
	return best
}
