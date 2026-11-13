package tguser

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"

	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
)

// maxSubtitleBytes is the largest subtitle file a search may download.
const maxSubtitleBytes int64 = 8 << 20

// DownloadMessage reads one small document from a channel the signed-in account can see.
// A file larger than maxBytes is refused, because this path is only for subtitles.
func (m *Manager) DownloadMessage(ctx context.Context, ref SourceRef, messageID int, maxBytes int64) (string, []byte, error) {
	if m == nil || messageID <= 0 {
		return "", nil, fmt.Errorf("telegram: missing message")
	}
	if maxBytes <= 0 || maxBytes > maxSubtitleBytes {
		maxBytes = maxSubtitleBytes
	}
	lv, err := m.pick(ctx)
	if err != nil {
		return "", nil, err
	}
	val, err := m.call(ctx, lv, func(ctx context.Context, api *tg.Client) (any, error) {
		peer, err := lv.peer(ctx, api, ref)
		if err != nil {
			return nil, err
		}
		res, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash},
			ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}},
		})
		if err != nil {
			return nil, err
		}
		var doc *tg.Document
		var name string
		for _, raw := range searchMessages(res) {
			msg, ok := raw.(*tg.Message)
			if !ok || msg.ID != messageID {
				continue
			}
			doc, name = messageDocument(msg)
			break
		}
		if doc == nil {
			return nil, fmt.Errorf("telegram: message %d has no file", messageID)
		}
		if doc.Size <= 0 || doc.Size > maxBytes {
			return nil, fmt.Errorf("telegram: file is %d bytes", doc.Size)
		}
		loc := doc.AsInputDocumentFileLocation("")
		buf := make([]byte, 0, doc.Size)
		for off := int64(0); off < doc.Size; off += media.BlockSize {
			part, err := readUserFile(ctx, api, loc, off, int(media.BlockSize))
			if err != nil {
				return nil, err
			}
			if len(part) == 0 {
				return nil, fmt.Errorf("telegram: short read")
			}
			buf = append(buf, part...)
		}
		if int64(len(buf)) > doc.Size {
			buf = buf[:doc.Size]
		}
		lv.clock.hold(historyGap)
		return downloaded{name, buf}, nil
	})
	if err != nil {
		m.did(lv, "subtitle", fmt.Sprintf("%d", messageID), err)
		return "", nil, err
	}
	got, _ := val.(downloaded)
	m.did(lv, "subtitle", fmt.Sprintf("%d · %d", messageID, len(got.data)), nil)
	return got.name, got.data, nil
}

type downloaded struct {
	name string
	data []byte
}

func messageDocument(msg *tg.Message) (*tg.Document, string) {
	mediaDoc, ok := msg.Media.(*tg.MessageMediaDocument)
	if !ok {
		return nil, ""
	}
	doc, ok := mediaDoc.Document.(*tg.Document)
	if !ok {
		return nil, ""
	}
	name := ""
	for _, attr := range doc.Attributes {
		if a, ok := attr.(*tg.DocumentAttributeFilename); ok {
			name = a.FileName
		}
	}
	return doc, name
}

func readUserFile(ctx context.Context, api *tg.Client, loc tg.InputFileLocationClass, offset int64, limit int) ([]byte, error) {
	req := &tg.UploadGetFileRequest{Location: loc, Offset: offset, Limit: limit}
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
