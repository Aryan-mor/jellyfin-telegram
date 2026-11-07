package fileid

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math/rand"
	"testing"

	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
)

func TestUniqueIDRoundTrip(t *testing.T) {
	for _, id := range []int64{1, 255, 256, 5_000_000_000, 6_255_000_000_000_123_456, 0x0100000000000000, -42} {
		u := UniqueID(id)
		got, ok := DocID(u)
		if !ok || got != id {
			t.Errorf("%d -> %q -> %d %v", id, u, got, ok)
		}
	}
	if UniqueID(1) == UniqueID(2) {
		t.Error("two documents share a unique id")
	}
	if _, ok := DocID("not a unique id!"); ok {
		t.Error("garbage was accepted")
	}
	// A photo has another type (1) and a longer body, so it is not a document.
	photo := make([]byte, 16)
	binary.LittleEndian.PutUint32(photo[0:4], 1)
	binary.LittleEndian.PutUint64(photo[4:12], 77)
	binary.LittleEndian.PutUint32(photo[12:16], 5)
	if _, ok := DocID(base64.RawURLEncoding.EncodeToString(rleEncode(photo))); ok {
		t.Error("a non-document id was accepted")
	}
	// The same body with the document type is rejected when it is the wrong length.
	long := append([]byte{2, 0, 0, 0}, photo[4:]...)
	if _, ok := DocID(base64.RawURLEncoding.EncodeToString(rleEncode(long))); ok {
		t.Error("an id of the wrong length was accepted")
	}
}

// fakeFile serves a byte slice in blocks and counts how many different blocks were asked for.
type fakeFile struct {
	data  []byte
	asked map[int64]bool
}

func (f *fakeFile) fetch(_ context.Context, block int64) ([]byte, error) {
	if f.asked == nil {
		f.asked = map[int64]bool{}
	}
	f.asked[block] = true
	from := block * media.BlockSize
	to := min(int64(len(f.data)), from+media.BlockSize)
	if from >= to {
		return nil, fmt.Errorf("block %d is past the end", block)
	}
	return f.data[from:to], nil
}

func random(n int, seed int64) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	return b
}

func TestReadTakesAtMostThreeBlocksAndMatchesTheBytes(t *testing.T) {
	m := int(media.BlockSize)
	for _, size := range []int{1, 1000, 64 << 10, 100 << 10, m, m + 1, m + 100, 3*m + m/2, 40*m + 7, 40 * m} {
		file := &fakeFile{data: random(size, int64(size))}
		s, err := Read(context.Background(), int64(size), file.fetch)
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		wantHead := file.data[:min(size, m)]
		wantTail := file.data[size-min(size, TailBytes):]
		if !bytes.Equal(s.Head, wantHead) || !bytes.Equal(s.Tail, wantTail) {
			t.Fatalf("size %d: head or tail differs", size)
		}
		if len(file.asked) > 3 {
			t.Fatalf("size %d: %d blocks were read", size, len(file.asked))
		}
	}
}

func TestHashFollowsTheContentOnly(t *testing.T) {
	size := 5*int(media.BlockSize) + 12345
	data := random(size, 7)
	hash := func(b []byte) string {
		s, err := Read(context.Background(), int64(len(b)), (&fakeFile{data: b}).fetch)
		if err != nil {
			t.Fatal(err)
		}
		return s.Hash()
	}
	base := hash(data)
	if hash(append([]byte(nil), data...)) != base {
		t.Fatal("the same bytes gave two hashes")
	}
	if len(base) != 22 {
		t.Fatalf("hash %q is %d characters, want 22", base, len(base))
	}
	for name, at := range map[string]int{"first byte": 0, "last byte": size - 1, "tail": size - 5000} {
		changed := append([]byte(nil), data...)
		changed[at] ^= 0xff
		if hash(changed) == base {
			t.Errorf("a changed %s did not change the hash", name)
		}
	}
	// A byte in the middle is not sampled. That is the price of reading three blocks.
	middle := append([]byte(nil), data...)
	middle[size/2] ^= 0xff
	if hash(middle) != base {
		t.Error("a byte outside the sample changed the hash")
	}
	if hash(data[:size-1]) == base {
		t.Error("a shorter file gave the same hash")
	}
}

func TestOSDbMatchesTheReferenceDefinition(t *testing.T) {
	size := 3<<20 + 99
	data := random(size, 11)
	s, err := Read(context.Background(), int64(size), (&fakeFile{data: data}).fetch)
	if err != nil {
		t.Fatal(err)
	}
	// The reference reads the whole file, as the original description of the hash does.
	want := uint64(size)
	for _, off := range []int{0, size - 65536} {
		for i := 0; i < 8192; i++ {
			want += binary.LittleEndian.Uint64(data[off+i*8:])
		}
	}
	if got := s.OSDb(); got != fmt.Sprintf("%016x", want) {
		t.Fatalf("osdb = %s, want %016x", got, want)
	}
	small, err := Read(context.Background(), 1000, (&fakeFile{data: random(1000, 1)}).fetch)
	if err != nil {
		t.Fatal(err)
	}
	if small.OSDb() != "" {
		t.Error("a file under 128 KiB got an OSDb hash")
	}
}

// A value taken from the real Bot API: sendDocument answered with this file_unique_id, and the
// bots read the same message over MTProto with this document id.
func TestUniqueIDMatchesTelegram(t *testing.T) {
	const doc, unique = int64(6014791683013616491), "AgADayMAAivVeFM"
	if got := UniqueID(doc); got != unique {
		t.Fatalf("UniqueID = %q, Telegram said %q", got, unique)
	}
	if got, ok := DocID(unique); !ok || got != doc {
		t.Fatalf("DocID = %d %v", got, ok)
	}
}
