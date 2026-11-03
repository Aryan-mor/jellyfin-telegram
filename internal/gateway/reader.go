package gateway

import (
	"context"
	"errors"
	"io"

	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
)

// sequentialBlocks is how many consecutive blocks a reader must take before read-ahead
// starts. It keeps short probes and seek bursts from pulling extra data.
const sequentialBlocks = 2

// reader is an io.ReadSeeker over a channel file, for http.ServeContent.
type reader struct {
	ctx       context.Context
	engine    *Engine
	file      media.File
	readahead int64
	pacer     *pacer
	// background marks work nobody is waiting for, which steps aside for everything else.
	background bool

	pos      int64
	curBlock int64
	cur      []byte

	lastBlock int64
	streak    int
	aheadTo   int64
}

func newReader(ctx context.Context, e *Engine, f media.File, readahead int) *reader {
	return &reader{ctx: ctx, engine: e, file: f, readahead: int64(readahead), curBlock: -1, lastBlock: -2}
}

func (r *reader) Seek(offset int64, whence int) (int64, error) {
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = r.pos + offset
	case io.SeekEnd:
		next = r.file.Size + offset
	default:
		return 0, errors.New("invalid whence")
	}
	if next < 0 {
		return 0, errors.New("negative position")
	}
	r.pos = next
	return next, nil
}

func (r *reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.pos >= r.file.Size {
		return 0, io.EOF
	}
	block := r.pos / media.BlockSize
	if block != r.curBlock {
		if !r.background {
			r.engine.noteForeground()
		}
		// A probe stays exempt, because Jellyfin waits on it before playback can start.
		// A cached block costs Telegram nothing, so only a real fetch counts.
		if r.pacer != nil && !r.engine.isProbing(r.file.MessageID) && !r.engine.store.Has(r.file.DocID, block) {
			if err := r.pacer.wait(r.ctx, media.BlockSize); err != nil {
				return 0, err
			}
		}
		data, err := r.engine.Block(r.ctx, r.file, block)
		if err != nil {
			return 0, err
		}
		r.cur, r.curBlock = data, block
		r.noteBlock(block)
	}
	offset := r.pos - block*media.BlockSize
	if offset >= int64(len(r.cur)) {
		return 0, io.ErrUnexpectedEOF
	}
	n := copy(p, r.cur[offset:])
	r.pos += int64(n)
	return n, nil
}

func (r *reader) noteBlock(block int64) {
	if block == r.lastBlock+1 {
		r.streak++
	} else {
		r.streak = 1
		r.aheadTo = block
	}
	r.lastBlock = block

	if r.readahead == 0 || r.streak < sequentialBlocks || r.engine.isProbing(r.file.MessageID) {
		return
	}
	// Jellyfin repeats its probe on every playback request, and that probe stays inside
	// the pinned blocks. Real playback starts reading ahead once it moves past them.
	if r.engine.store.IsPinned(r.file.DocID, block) {
		return
	}
	last := (r.file.Size - 1) / media.BlockSize
	for next := max(r.aheadTo, block) + 1; next <= min(block+r.readahead, last); next++ {
		r.engine.prefetch(r.ctx, r.file, next)
		r.aheadTo = next
	}
}
