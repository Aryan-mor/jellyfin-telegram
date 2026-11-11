package tgpool

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
)

// preciseStep is the alignment Telegram requires for an upload.getFile with the precise flag.
const preciseStep int64 = 1024

// span is one precise read. Offset is divisible by preciseStep, and the read stays inside one block.
type span struct {
	Offset int64
	Limit  int
}

// preciseSpans covers [off, off+n) with the reads Telegram accepts. A read is a multiple of
// 1 KiB and does not cross a 1 MiB block. Bytes past size are not asked for.
func preciseSpans(off, n, size int64) []span {
	if n <= 0 || off < 0 || size <= 0 || off >= size {
		return nil
	}
	if off+n > size {
		n = size - off
	}
	start := off - off%preciseStep
	end := off + n
	if rem := end % preciseStep; rem != 0 {
		end += preciseStep - rem
	}
	var out []span
	for start < end && start < size {
		chunkEnd := (start/media.BlockSize + 1) * media.BlockSize
		lim := min(end, chunkEnd) - start
		if lim <= 0 {
			break
		}
		out = append(out, span{Offset: start, Limit: int(lim)})
		start += lim
	}
	return out
}

// rangeFile is an io.ReaderAt over one document. It fetches only the pages a caller actually
// reads, so walking a subtitle index does not pull the video.
type rangeFile struct {
	p         *Pool
	ctx       context.Context
	messageID int
	docID     int64
	size      int64

	mu    sync.Mutex
	pages map[int64][]byte
	n     int64
}

// Open returns a reader for one document. Reads wait while something is playing.
func (p *Pool) Open(ctx context.Context, messageID int, docID, size int64) io.ReaderAt {
	return &rangeFile{
		p: p, ctx: ctx, messageID: messageID, docID: docID, size: size,
		pages: map[int64][]byte{},
	}
}

// Downloaded is how many bytes Telegram has sent for this reader.
func (f *rangeFile) Downloaded() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n
}

func (f *rangeFile) ReadAt(p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := f.p.waitQuiet(f.ctx); err != nil {
		return 0, err
	}
	if off < 0 {
		return 0, io.ErrUnexpectedEOF
	}
	if off >= f.size {
		return 0, io.EOF
	}
	if off+int64(len(p)) > f.size {
		p = p[:f.size-off]
	}
	if err := f.fill(off, int64(len(p))); err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	n := copyPages(f.pages, p, off)
	if n < len(p) {
		return n, io.ErrUnexpectedEOF
	}
	return n, nil
}

func (f *rangeFile) fill(off, n int64) error {
	for _, sp := range preciseSpans(off, n, f.size) {
		if f.cached(sp.Offset, sp.Limit) {
			continue
		}
		buf := make([]byte, sp.Limit)
		got, err := f.p.readPrecise(f.ctx, f.messageID, f.docID, sp.Offset, buf)
		if got > 0 {
			f.store(sp.Offset, buf[:got])
		}
		if err != nil {
			return err
		}
		if got < sp.Limit && sp.Offset+int64(got) < f.size && sp.Offset+int64(got) < off+n {
			// Telegram ended the read early inside the file. The caller still needs those bytes.
			return io.ErrUnexpectedEOF
		}
	}
	return nil
}

func (f *rangeFile) cached(off int64, n int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for at := off; at < off+int64(n) && at < f.size; at += preciseStep {
		if _, ok := f.pages[at]; !ok {
			return false
		}
	}
	return true
}

func (f *rangeFile) store(off int64, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := 0; i < len(data); {
		end := i + int(preciseStep)
		if end > len(data) {
			end = len(data)
		}
		key := off + int64(i)
		if _, ok := f.pages[key]; !ok {
			buf := make([]byte, end-i)
			copy(buf, data[i:end])
			f.pages[key] = buf
			f.n += int64(len(buf))
		}
		if end == len(data) {
			break
		}
		i = end
	}
}

func copyPages(pages map[int64][]byte, dst []byte, off int64) int {
	wrote := 0
	for wrote < len(dst) {
		key := (off + int64(wrote)) - (off+int64(wrote))%preciseStep
		page, ok := pages[key]
		if !ok {
			break
		}
		skip := int((off + int64(wrote)) - key)
		if skip >= len(page) {
			break
		}
		n := copy(dst[wrote:], page[skip:])
		if n == 0 {
			break
		}
		wrote += n
	}
	return wrote
}

func (p *Pool) readPrecise(ctx context.Context, messageID int, docID, offset int64, dst []byte) (int, error) {
	var data []byte
	err := p.callGap(ctx, messageID, sparseGap, func(w worker) error {
		var err error
		data, err = w.getBlock(ctx, messageID, docID, offset, len(dst))
		if err == nil {
			p.addBytes(w, len(data))
		}
		return err
	})
	if err != nil {
		return 0, err
	}
	return copy(dst, data), nil
}

// waitQuiet blocks while a person is watching, same as the background extract pacer.
func (p *Pool) waitQuiet(ctx context.Context) error {
	for {
		p.mu.Lock()
		fn := p.playing
		p.mu.Unlock()
		if fn == nil || !fn() {
			return nil
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
