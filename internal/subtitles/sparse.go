package subtitles

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// ErrSparseUnsupported means the container has no index that points at the subtitle bytes.
// The caller can still read the whole file.
var ErrSparseUnsupported = errors.New("sparse subtitle extract is not available for this file")

func unsupported(reason string) error {
	return fmt.Errorf("%w: %s", ErrSparseUnsupported, reason)
}

// ExtractSparse reads subtitle tracks from the container index. Matroska cues and an MP4 sample
// table name the subtitle bytes, so the video payload is never requested.
func ExtractSparse(ctx context.Context, r io.ReaderAt, size int64, tracks []ExtractTrack) (map[int][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(tracks) == 0 {
		return map[int][]byte{}, nil
	}
	if size < 16 {
		return nil, unsupported("file is too small")
	}
	head, err := readFullAt(r, 0, 12)
	if err != nil {
		return nil, err
	}
	if uint32(head[0])<<24|uint32(head[1])<<16|uint32(head[2])<<8|uint32(head[3]) == 0x1A45DFA3 {
		return extractMKV(ctx, r, size, tracks)
	}
	if printable(head[4:8]) {
		return extractMP4(ctx, r, size, tracks)
	}
	return nil, unsupported("container is not matroska or mp4")
}

func printable(b []byte) bool {
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

func readFullAt(r io.ReaderAt, off int64, n int) ([]byte, error) {
	if n < 0 {
		return nil, errors.New("negative read")
	}
	buf := make([]byte, n)
	got := 0
	for got < n {
		k, err := r.ReadAt(buf[got:], off+int64(got))
		got += k
		if got == n {
			return buf, nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) && got > 0 {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
		if k == 0 {
			return nil, io.ErrUnexpectedEOF
		}
	}
	return buf, nil
}
