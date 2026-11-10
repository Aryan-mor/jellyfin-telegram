package tguser

import (
	"context"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// floodWait sleeps out FLOOD_WAIT on the upload save methods and tells the caller,
// which then lengthens this session's rest until the end of the day.
func floodWait(onWait func(time.Duration)) telegram.Middleware {
	return telegram.MiddlewareFunc(func(next tg.Invoker) telegram.InvokeFunc {
		return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
			for {
				err := next.Invoke(ctx, input, output)
				if err == nil {
					return nil
				}
				wait, ok := tgerr.AsFloodWait(err)
				if !ok || !uploadSave(input) {
					return err
				}
				if onWait != nil {
					onWait(wait)
				}
				if wait <= 0 {
					continue
				}
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}
		}
	})
}

func uploadSave(input bin.Encoder) bool {
	switch input.(type) {
	case *tg.UploadSaveFilePartRequest, *tg.UploadSaveBigFilePartRequest:
		return true
	default:
		return false
	}
}
