package tguser

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path"
	"strings"
	"time"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"

	"github.com/Aryan-mor/jellyfin-telegram/internal/naming"
)

// ErrBusy means the account is already sending another file. The caller tries again later.
var ErrBusy = errors.New("this account is already uploading")

// Upload sends path to the channel as a document and returns the new message id.
// Parts stay on this one account. Extra sessions speed up the queue, not this file.
func (m *Manager) Upload(ctx context.Context, userID int64, filePath, name, caption string, onProgress func(done, total int64)) (int, error) {
	lv, def, err := m.get(userID)
	if err != nil {
		return 0, err
	}
	if !lv.busy.CompareAndSwap(false, true) {
		return 0, ErrBusy
	}
	defer lv.busy.Store(false)
	info, err := os.Stat(filePath)
	if err != nil {
		return 0, err
	}
	lv.total.Store(info.Size())
	lv.done.Store(0)
	defer func() {
		lv.done.Store(0)
		lv.total.Store(0)
	}()
	bps := int64(lv.meta.pace(def).MBPerSec) * 1000 * 1000
	val, err := m.call(ctx, lv, func(ctx context.Context, api *tg.Client) (any, error) {
		return sendFile(ctx, api, m.channelID, lv.meta.AccessHash, filePath, name, caption, info.Size(), bps, func(done, total int64) {
			lv.done.Store(done)
			lv.total.Store(total)
			if onProgress != nil {
				onProgress(done, total)
			}
		})
	})
	if err != nil {
		if fatalAuth(err) {
			lv.errText.Store(err.Error())
			lv.meta.Error = err.Error()
			_ = lv.store.writeMeta(lv.meta)
			lv.connected.Store(false)
		}
		return 0, err
	}
	id, _ := val.(int)
	if id == 0 {
		return 0, errors.New("telegram did not report the new message id")
	}
	return id, nil
}

// UploadTo sends path into channelID. The main channel uses Upload. The account must be allowed to post there.
func (m *Manager) UploadTo(ctx context.Context, userID, channelID int64, filePath, name, caption string, onProgress func(done, total int64)) (int, error) {
	if channelID == 0 || channelID == m.channelID {
		return m.Upload(ctx, userID, filePath, name, caption, onProgress)
	}
	lv, def, err := m.get(userID)
	if err != nil {
		return 0, err
	}
	if !lv.busy.CompareAndSwap(false, true) {
		return 0, ErrBusy
	}
	defer lv.busy.Store(false)
	info, err := os.Stat(filePath)
	if err != nil {
		return 0, err
	}
	lv.total.Store(info.Size())
	lv.done.Store(0)
	defer func() {
		lv.done.Store(0)
		lv.total.Store(0)
	}()
	bps := int64(lv.meta.pace(def).MBPerSec) * 1000 * 1000
	val, err := m.call(ctx, lv, func(ctx context.Context, api *tg.Client) (any, error) {
		hash, err := channelAccess(ctx, api, channelID)
		if err != nil {
			return nil, fmt.Errorf("cannot post in that channel: %w", err)
		}
		return sendFile(ctx, api, channelID, hash, filePath, name, caption, info.Size(), bps, func(done, total int64) {
			lv.done.Store(done)
			lv.total.Store(total)
			if onProgress != nil {
				onProgress(done, total)
			}
		})
	})
	if err != nil {
		if fatalAuth(err) {
			lv.errText.Store(err.Error())
			lv.meta.Error = err.Error()
			_ = lv.store.writeMeta(lv.meta)
			lv.connected.Store(false)
		}
		return 0, err
	}
	id, _ := val.(int)
	if id == 0 {
		return 0, errors.New("telegram did not report the new message id")
	}
	return id, nil
}

// FindMessage looks through healthy sessions for a document already posted with this name and size.
// A crash after send and before the row is saved must not post the file again.
func (m *Manager) FindMessage(ctx context.Context, name string, size int64) (int, bool, error) {
	searched := false
	var last error
	for _, view := range m.Healthy() {
		lv, _, err := m.get(view.UserID)
		if err != nil || lv.busy.Load() {
			continue
		}
		val, err := m.call(ctx, lv, func(ctx context.Context, api *tg.Client) (any, error) {
			return searchFile(ctx, api, m.channelID, lv.meta.AccessHash, name, size)
		})
		if err != nil {
			if fatalAuth(err) {
				lv.errText.Store(err.Error())
			}
			last = err
			continue
		}
		searched = true
		if id, _ := val.(int); id != 0 {
			return id, true, nil
		}
	}
	if !searched {
		// A negative answer must mean the channel was searched. Otherwise the file could be posted twice.
		if last == nil {
			last = errors.New("no session was free to search the channel")
		}
		return 0, false, last
	}
	return 0, false, nil
}

// InReady reports that the channel already has this title. A language suffix does not make a
// different title: the bare tag and every language version of it all count. An unsuffixed tag
// that the channel search returns is present, the same as before.
func (m *Manager) InReady(ctx context.Context, tag string) (bool, error) {
	tag = strings.TrimSpace(tag)
	if m == nil || tag == "" {
		return false, nil
	}
	query := tag
	if base := naming.BaseTag(tag); base != "" {
		query = base
	}
	n, texts, err := m.searchReady(ctx, query, 5)
	if err != nil {
		return false, err
	}
	if query == tag && n > 0 {
		return true, nil
	}
	if captionsHold(query, texts) {
		return true, nil
	}
	if naming.BaseTag(query) == "" {
		return false, nil
	}
	// The bare hashtag was not in the channel. A copy with a language suffix is a different
	// hashtag, so ask for the base plus the mark that starts that suffix.
	_, more, err := m.searchReady(ctx, strings.TrimPrefix(query, "#")+"_", 20)
	if err != nil {
		return false, err
	}
	return captionsHold(query, more), nil
}

// readyHit is how many messages a channel search returned, and the text of the real ones.
type readyHit struct {
	n     int
	texts []string
}

func (m *Manager) searchReady(ctx context.Context, query string, limit int) (int, []string, error) {
	lv, err := m.pick(ctx)
	if err != nil {
		return 0, nil, err
	}
	val, err := m.call(ctx, lv, func(ctx context.Context, api *tg.Client) (any, error) {
		res, err := api.MessagesSearch(ctx, &tg.MessagesSearchRequest{
			Peer:   &tg.InputPeerChannel{ChannelID: m.channelID, AccessHash: lv.meta.AccessHash},
			Q:      query,
			Filter: &tg.InputMessagesFilterEmpty{},
			Limit:  limit,
		})
		if err != nil {
			return nil, err
		}
		msgs := searchMessages(res)
		hit := readyHit{n: len(msgs)}
		for _, raw := range msgs {
			msg, ok := raw.(*tg.Message)
			if ok {
				hit.texts = append(hit.texts, msg.Message)
			}
		}
		return hit, nil
	})
	if err != nil {
		return 0, nil, err
	}
	hit, _ := val.(readyHit)
	return hit.n, hit.texts, nil
}

// captionsHold reports whether one of the captions is this title in any language.
func captionsHold(tag string, captions []string) bool {
	want := naming.BaseTag(tag)
	if want == "" {
		return false
	}
	for _, caption := range captions {
		for _, got := range naming.TagsIn(caption) {
			if naming.BaseTag(got) == want {
				return true
			}
		}
	}
	return false
}

func sendFile(ctx context.Context, api *tg.Client, channelID, accessHash int64, filePath, name, caption string, size, bps int64, progress func(done, total int64)) (int, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var reader io.Reader = f
	if bps > 0 {
		reader = &steady{r: f, bps: bps}
	}
	up := uploader.NewUploader(api).WithPartSize(PartSize).WithThreads(1).WithProgress(progressFunc(progress))
	file, err := up.Upload(ctx, uploader.NewUpload(name, reader, size))
	if err != nil {
		return 0, fmt.Errorf("upload: %w", err)
	}
	var randomID [8]byte
	if _, err := rand.Read(randomID[:]); err != nil {
		return 0, err
	}
	ext := strings.ToLower(path.Ext(name))
	kind := mime.TypeByExtension(ext)
	if kind == "" {
		kind = "application/octet-stream"
	}
	req := &tg.MessagesSendMediaRequest{
		Peer: &tg.InputPeerChannel{ChannelID: channelID, AccessHash: accessHash},
		Media: &tg.InputMediaUploadedDocument{
			File:      file,
			MimeType:  kind,
			ForceFile: true,
			Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeFilename{FileName: name},
			},
		},
		Message:  caption,
		RandomID: int64(binary.LittleEndian.Uint64(randomID[:])),
	}
	res, err := api.MessagesSendMedia(ctx, req)
	if err != nil {
		return 0, fmt.Errorf("messages.sendMedia: %w", err)
	}
	if id := sentID(res); id != 0 {
		return id, nil
	}
	return 0, errors.New("telegram did not report the new message id")
}

type progressFunc func(done, total int64)

func (p progressFunc) Chunk(_ context.Context, state uploader.ProgressState) error {
	if p != nil {
		p(state.Uploaded, state.Total)
	}
	return nil
}

// steady spreads reads so the average stays near bps.
type steady struct {
	r    io.Reader
	bps  int64
	last time.Time
	sent int64
}

func (s *steady) Read(p []byte) (int, error) {
	if s.last.IsZero() {
		s.last = time.Now()
	}
	n, err := s.r.Read(p)
	s.sent += int64(n)
	if s.bps > 0 {
		want := time.Duration(s.sent) * time.Second / time.Duration(s.bps)
		if wait := want - time.Since(s.last); wait > 0 {
			time.Sleep(wait)
		}
	}
	return n, err
}

func searchFile(ctx context.Context, api *tg.Client, channelID, accessHash int64, name string, size int64) (int, error) {
	res, err := api.MessagesSearch(ctx, &tg.MessagesSearchRequest{
		Peer:   &tg.InputPeerChannel{ChannelID: channelID, AccessHash: accessHash},
		Q:      name,
		Filter: &tg.InputMessagesFilterDocument{},
		Limit:  20,
	})
	if err != nil {
		return 0, err
	}
	for _, raw := range searchMessages(res) {
		msg, ok := raw.(*tg.Message)
		if !ok {
			continue
		}
		media, ok := msg.Media.(*tg.MessageMediaDocument)
		if !ok {
			continue
		}
		doc, ok := media.Document.(*tg.Document)
		if !ok || doc.Size != size {
			continue
		}
		for _, attr := range doc.Attributes {
			file, ok := attr.(*tg.DocumentAttributeFilename)
			if ok && file.FileName == name {
				return msg.ID, nil
			}
		}
	}
	return 0, nil
}

func searchMessages(res tg.MessagesMessagesClass) []tg.MessageClass {
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

func sentID(res tg.UpdatesClass) int {
	switch v := res.(type) {
	case *tg.Updates:
		for _, u := range v.Updates {
			switch n := u.(type) {
			case *tg.UpdateNewChannelMessage:
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

func fatalAuth(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, needle := range []string{"SESSION_REVOKED", "AUTH_KEY_UNREGISTERED", "AUTH_KEY_INVALID", "USER_DEACTIVATED", "SESSION_EXPIRED"} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}
