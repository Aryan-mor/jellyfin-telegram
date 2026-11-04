package home

import (
	"context"

	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgpool"
)

// Reader walks the personal channel. It never deletes a message there.
type Reader struct {
	Pool    *tgpool.Pool
	Channel int64
}

// Messages reads videos and photos sent as files.
func (r Reader) Messages(ctx context.Context, ids []int) ([]media.Message, error) {
	return r.Pool.MessagesIn(ctx, r.Channel, ids)
}

// Thumbnail is unused. Photos are saved whole, and videos have no local poster.
func (r Reader) Thumbnail(context.Context, int) ([]byte, error) { return nil, nil }

// DeleteMessages does nothing. The personal channel is only changed by the person.
func (r Reader) DeleteMessages(context.Context, []int) error { return nil }

// Download reads one photo so it can be stored as an image file.
func (r Reader) Download(ctx context.Context, messageID int, docID, size int64) ([]byte, error) {
	out := make([]byte, 0, size)
	for block := int64(0); int64(len(out)) < size; block++ {
		data, err := r.Pool.FetchIn(ctx, r.Channel, messageID, docID, block, size)
		if err != nil {
			return nil, err
		}
		out = append(out, data...)
	}
	if int64(len(out)) > size {
		out = out[:size]
	}
	return out, nil
}
