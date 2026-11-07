// Package fileid names a file in ways that every bot can work out for itself.
//
// Three names, from the weakest to the strongest:
//
//   - UniqueID is Telegram's file_unique_id. It is made from the document id, so a forward or a
//     copy keeps it, and a bot that speaks the Bot API can read it without MTProto. A new upload
//     of the same movie gets another one.
//   - Hash is a content hash. It does not change when the movie is uploaded again.
//   - OSDb is the OpenSubtitles "moviehash", so a hash can find subtitles outside Telegram too.
//
// The two hashes need only the first and the last bytes of the file, which is two or three blocks
// from Telegram, never the whole file.
package fileid

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
)

const (
	// HeadBytes is how much of the start goes into Hash. It is the first block, which the prober
	// has already read and pinned.
	HeadBytes = media.BlockSize
	// TailBytes is how much of the end goes into Hash and OSDb.
	TailBytes = 64 << 10

	// typeDocument is the "document" type of Telegram's unique ids.
	typeDocument = 2
	hashDomain   = "jftg-h1\x00"
)

// UniqueID returns file_unique_id of a document, as the Bot API shows it. It is the document id
// with a type, run-length coded for zero bytes and written in unpadded url-safe base64.
func UniqueID(docID int64) string {
	var raw [12]byte
	binary.LittleEndian.PutUint32(raw[0:4], typeDocument)
	binary.LittleEndian.PutUint64(raw[4:12], uint64(docID))
	return base64.RawURLEncoding.EncodeToString(rleEncode(raw[:]))
}

// DocID reads the document id out of a file_unique_id. ok is false for any other kind of file,
// such as a photo, and for text that is not a unique id.
func DocID(unique string) (int64, bool) {
	b, err := base64.RawURLEncoding.DecodeString(unique)
	if err != nil {
		return 0, false
	}
	raw, ok := rleDecode(b)
	if !ok || len(raw) != 12 || binary.LittleEndian.Uint32(raw[0:4]) != typeDocument {
		return 0, false
	}
	return int64(binary.LittleEndian.Uint64(raw[4:12])), true
}

func rleEncode(s []byte) []byte {
	var out []byte
	zeros := 0
	for _, b := range s {
		if b == 0 {
			zeros++
			continue
		}
		if zeros > 0 {
			out = append(out, 0, byte(zeros))
			zeros = 0
		}
		out = append(out, b)
	}
	if zeros > 0 {
		out = append(out, 0, byte(zeros))
	}
	return out
}

func rleDecode(s []byte) ([]byte, bool) {
	var out []byte
	zero := false
	for _, b := range s {
		switch {
		case b == 0 && !zero:
			zero = true
		case zero:
			for i := 0; i < int(b); i++ {
				out = append(out, 0)
			}
			zero = false
		default:
			out = append(out, b)
		}
	}
	return out, !zero
}

// Sample is the part of a file the hashes are made from.
type Sample struct {
	Size int64
	Head []byte // the first min(Size, HeadBytes) bytes
	Tail []byte // the last min(Size, TailBytes) bytes
}

// Fetch returns block number n of the file. Only the last block may be short.
type Fetch func(ctx context.Context, block int64) ([]byte, error)

// Read gets the head and the tail of a file of this size. It asks for block 0 and for the blocks
// that hold the last TailBytes, so at most three blocks.
func Read(ctx context.Context, size int64, fetch Fetch) (Sample, error) {
	if size <= 0 {
		return Sample{}, errors.New("the file is empty")
	}
	first, err := fetch(ctx, 0)
	if err != nil {
		return Sample{}, fmt.Errorf("first block: %w", err)
	}
	headLen := min(size, HeadBytes)
	if int64(len(first)) < headLen {
		return Sample{}, io.ErrUnexpectedEOF
	}
	s := Sample{Size: size, Head: first[:headLen]}

	tailLen := min(size, TailBytes)
	start := size - tailLen
	for block := start / media.BlockSize; block <= (size-1)/media.BlockSize; block++ {
		var data []byte
		if block == 0 {
			data = first
		} else if data, err = fetch(ctx, block); err != nil {
			return Sample{}, fmt.Errorf("last block: %w", err)
		}
		from := max(start, block*media.BlockSize) - block*media.BlockSize
		to := min(size, (block+1)*media.BlockSize) - block*media.BlockSize
		if int64(len(data)) < to {
			return Sample{}, io.ErrUnexpectedEOF
		}
		s.Tail = append(s.Tail, data[from:to]...)
	}
	if int64(len(s.Tail)) != tailLen {
		return Sample{}, io.ErrUnexpectedEOF
	}
	return s, nil
}

// Hash is the content hash: 16 bytes of SHA-256 over the size, the head and the tail, written in
// unpadded url-safe base64. The same bytes give the same hash wherever the file is posted.
func (s Sample) Hash() string {
	h := sha256.New()
	h.Write([]byte(hashDomain))
	var size [8]byte
	binary.LittleEndian.PutUint64(size[:], uint64(s.Size))
	h.Write(size[:])
	h.Write(s.Head)
	h.Write(s.Tail)
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil)[:16])
}

// OSDb is the OpenSubtitles hash: the size plus the sum of the first and the last 64 KiB read as
// little-endian 64-bit numbers, as 16 hex digits. It is not defined below 128 KiB, and the result
// is then empty.
func (s Sample) OSDb() string {
	const chunk = 64 << 10
	if s.Size < 2*chunk || len(s.Head) < chunk || len(s.Tail) < chunk {
		return ""
	}
	sum := uint64(s.Size)
	for _, part := range [][]byte{s.Head[:chunk], s.Tail[len(s.Tail)-chunk:]} {
		for i := 0; i+8 <= len(part); i += 8 {
			sum += binary.LittleEndian.Uint64(part[i : i+8])
		}
	}
	return fmt.Sprintf("%016x", sum)
}
