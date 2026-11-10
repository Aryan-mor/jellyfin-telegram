package autoupload

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
	"github.com/Aryan-mor/jellyfin-telegram/internal/naming"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// Sessions is the set of user accounts that can post.
type Sessions interface {
	Healthy() []tguser.View
	LargestCap() int64
	Gate(id int64, size int64, now time.Time) tguser.Gate
	Upload(ctx context.Context, userID int64, path, name, caption string, progress func(done, total int64)) (int, error)
	FindMessage(ctx context.Context, name string, size int64) (int, bool, error)
	MarkSent(id int64, size int64, now time.Time)
}

// Verifier reads a message back with a bot, which is a different account from the one that uploaded it.
type Verifier interface {
	Messages(ctx context.Context, ids []int) ([]media.Message, error)
	FetchBlock(ctx context.Context, messageID int, docID, block, size int64) ([]byte, error)
}

// Worker moves the local files a person chose in the panel (catalog.KindLocal) onto the channel.
// A file that is probed stays on disk, because only the person decides to delete it.
type Worker struct {
	Cat      *catalog.Catalog
	Sessions Sessions
	Verify   Verifier
	Log      *slog.Logger
	Now      func() time.Time
	// Ctx is what an upload runs under. It outlives one pass, because a file takes longer than the
	// pass that started it. It ends when the switch is turned off or the program stops. When nil,
	// the context of the pass is used.
	Ctx      context.Context
	inflight sync.Map
}

// Run polls once a minute until ctx ends.
func (w *Worker) Run(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		if err := w.RunOnce(ctx); err != nil && ctx.Err() == nil && w.Log != nil {
			w.Log.Warn("uploader", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (w *Worker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *Worker) warn(msg string, err error) {
	if w.Log != nil {
		w.Log.Warn(msg, "err", err)
	}
}

// RunOnce does a single pass. Uploads that are already running are left alone.
func (w *Worker) RunOnce(ctx context.Context) error {
	rows, err := w.Cat.ListUploads(ctx)
	if err != nil {
		return err
	}
	var ready []catalog.Upload
	for _, row := range rows {
		if row.Kind != catalog.KindLocal {
			continue
		}
		if err := w.advance(ctx, row); err != nil {
			w.warn("upload "+row.FileName, err)
		}
		if row.State == catalog.UploadQueued || row.State == catalog.UploadUploading || row.State == catalog.UploadFailed {
			ready = append(ready, row)
		}
	}
	w.dispatch(ctx, ready)
	return nil
}

func (w *Worker) advance(ctx context.Context, row catalog.Upload) error {
	switch row.State {
	case catalog.UploadTooBig:
		if w.Sessions != nil && row.Size <= w.Sessions.LargestCap() && w.Sessions.LargestCap() > 0 {
			return w.Cat.SetUpload(ctx, row.ID, catalog.UploadQueued, 0, 0, "")
		}
	case catalog.UploadSent:
		return w.verify(ctx, row)
	case catalog.UploadVerified:
		return w.markIndexed(ctx, row)
	case catalog.UploadIndexed:
		return w.markProbed(ctx, row)
	}
	return nil
}

func (w *Worker) dispatch(ctx context.Context, rows []catalog.Upload) {
	if w.Sessions == nil {
		return
	}
	used := map[int64]bool{}
	for _, row := range rows {
		if _, busy := w.inflight.Load(row.ID); busy {
			continue
		}
		// A channel file with this name and size may already exist. It still has to pass the byte
		// check before the local copy can go, so it only moves the row to "sent".
		if f, ok, err := w.Cat.FindFile(ctx, row.FileName, row.Size); err == nil && ok {
			_ = w.Cat.SetUpload(ctx, row.ID, catalog.UploadSent, f.MessageID, 0, "")
			continue
		}
		if search, ok := w.Sessions.(interface {
			InReady(context.Context, string) (bool, error)
		}); ok {
			if tag := naming.BaseTag(naming.LookupTag(naming.Parse(row.FileName, row.Caption))); tag != "" {
				yes, err := search.InReady(ctx, tag)
				if err != nil {
					w.warn("cannot search the library", err)
				} else if yes {
					_ = w.Cat.SetUpload(ctx, row.ID, catalog.UploadPresent, 0, 0, "already in Ready")
					continue
				}
			}
		}
		var pick int64
		var best time.Duration
		found := false
		var why []string
		healthy := w.Sessions.Healthy()
		for _, view := range healthy {
			name := "@" + view.Username
			if view.Username == "" {
				name = fmt.Sprintf("user %d", view.UserID)
			}
			if used[view.UserID] || view.Busy {
				why = append(why, name+" is sending another file")
				continue
			}
			gate := w.Sessions.Gate(view.UserID, row.Size, w.now())
			if gate.Reason != "" && !gate.Ready {
				why = append(why, name+": "+gate.Reason)
				continue
			}
			if !gate.Ready {
				why = append(why, fmt.Sprintf("%s rests for %s more", name, gate.Wait.Round(time.Second)))
				continue
			}
			if !found || gate.Wait < best {
				pick = view.UserID
				best = gate.Wait
				found = true
			}
		}
		if !found {
			// Say what the file waits for, so a quiet queue is not a mystery.
			note := "waiting: no signed-in account is ready"
			if len(why) > 0 {
				note = "waiting: " + strings.Join(why, "; ")
			}
			_ = w.Cat.SetUpload(ctx, row.ID, row.State, row.MessageID, 0, note)
			continue
		}
		used[pick] = true
		row := row
		session := pick
		w.inflight.Store(row.ID, struct{}{})
		upload := ctx
		if w.Ctx != nil {
			upload = w.Ctx
		}
		go func() {
			defer w.inflight.Delete(row.ID)
			w.uploadOne(upload, row, session)
		}()
	}
}

func (w *Worker) uploadOne(ctx context.Context, row catalog.Upload, session int64) {
	if err := w.Cat.SetUpload(ctx, row.ID, catalog.UploadUploading, row.MessageID, session, ""); err != nil {
		return
	}
	// A fresh row was never sent. Any other row may have been posted just before a crash or a
	// failed answer, so the channel is searched once, here and not for every queued file.
	if row.State != catalog.UploadQueued {
		id, ok, err := w.Sessions.FindMessage(ctx, row.FileName, row.Size)
		if err != nil {
			_ = w.Cat.SetUpload(ctx, row.ID, catalog.UploadFailed, 0, session, "cannot check the channel: "+err.Error())
			return
		}
		if ok {
			_ = w.Cat.SetUpload(ctx, row.ID, catalog.UploadSent, id, session, "")
			return
		}
	}
	id, err := w.Sessions.Upload(ctx, session, row.Path, row.FileName, row.Caption, nil)
	if errors.Is(err, tguser.ErrBusy) {
		// The other worker took this account a moment ago. The file is not at fault, so it waits.
		_ = w.Cat.SetUpload(ctx, row.ID, row.State, row.MessageID, 0, "waiting: the account is sending another file")
		return
	}
	if err != nil {
		_ = w.Cat.SetUpload(ctx, row.ID, catalog.UploadFailed, 0, session, err.Error())
		return
	}
	w.Sessions.MarkSent(session, row.Size, w.now())
	_ = w.Cat.SetUpload(ctx, row.ID, catalog.UploadSent, id, session, "")
}

func (w *Worker) verify(ctx context.Context, row catalog.Upload) error {
	if w.Verify == nil || row.MessageID == 0 {
		return nil
	}
	msgs, err := w.Verify.Messages(ctx, []int{row.MessageID})
	if err != nil {
		return err
	}
	if len(msgs) == 0 || !msgs[0].Exists || msgs[0].File == nil {
		return w.Cat.SetUpload(ctx, row.ID, catalog.UploadQueued, 0, 0, "message was not in the channel")
	}
	file := msgs[0].File
	if file.Size != row.Size {
		return w.Cat.SetUpload(ctx, row.ID, catalog.UploadMismatch, row.MessageID, 0, "the channel file is a different size")
	}
	if err := sameBlocks(ctx, w.Verify, row.Path, row.MessageID, file.DocID, row.Size); err != nil {
		var bad *mismatch
		if errors.As(err, &bad) {
			return w.Cat.SetUpload(ctx, row.ID, catalog.UploadMismatch, row.MessageID, 0, err.Error())
		}
		// A network error says nothing about the file. Try again on the next pass.
		return err
	}
	return w.Cat.SetUpload(ctx, row.ID, catalog.UploadVerified, row.MessageID, 0, "")
}

// mismatch means the channel really holds other bytes than the local file.
type mismatch struct{ msg string }

func (m *mismatch) Error() string { return m.msg }

// sameBlocks compares the first and the last block of the local file with the channel. size is the
// size of the whole file, which FetchBlock needs to know where the last block ends.
func sameBlocks(ctx context.Context, v Verifier, path string, messageID int, docID, size int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil {
		return err
	} else if info.Size() != size {
		return &mismatch{"the local file changed size after it was uploaded"}
	}
	indexes := []int64{0}
	if size > media.BlockSize {
		indexes = append(indexes, (size-1)/media.BlockSize)
	}
	for _, block := range indexes {
		off := block * media.BlockSize
		wantN := min(media.BlockSize, size-off)
		buf := make([]byte, wantN)
		if _, err := f.ReadAt(buf, off); err != nil {
			return err
		}
		got, err := v.FetchBlock(ctx, messageID, docID, block, size)
		if err != nil {
			return err
		}
		if int64(len(got)) < wantN {
			return fmt.Errorf("short read from the channel")
		}
		for i := int64(0); i < wantN; i++ {
			if got[i] != buf[i] {
				return &mismatch{"channel bytes do not match the local file"}
			}
		}
	}
	return nil
}

// markIndexed waits for the indexer to see the message that was verified. It looks up that exact
// message, not any file with the same name.
func (w *Worker) markIndexed(ctx context.Context, row catalog.Upload) error {
	if _, err := w.Cat.Get(ctx, row.MessageID); err != nil {
		if errors.Is(err, catalog.ErrNotFound) {
			return nil
		}
		return err
	}
	return w.Cat.SetUpload(ctx, row.ID, catalog.UploadIndexed, row.MessageID, 0, "")
}

func (w *Worker) markProbed(ctx context.Context, row catalog.Upload) error {
	f, err := w.Cat.Get(ctx, row.MessageID)
	if err != nil {
		if errors.Is(err, catalog.ErrNotFound) {
			return nil
		}
		return err
	}
	if f.ProbedAt.IsZero() {
		return nil
	}
	return w.Cat.SetUpload(ctx, row.ID, catalog.UploadProbed, row.MessageID, 0, "")
}
