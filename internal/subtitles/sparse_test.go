package subtitles

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"io"
	"strings"
	"sync"
	"testing"
)

func TestMatroskaCuesSkipTheVideo(t *testing.T) {
	file, videoAt, videoLen := sampleMKV(t)
	tr := &traced{b: file}
	out, err := ExtractSparse(context.Background(), tr, int64(len(file)), []ExtractTrack{{Position: 1, Codec: "subrip", Ext: "srt"}})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out[1])
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "00:00:01,500 --> 00:00:02,500") {
		t.Fatalf("srt %q", got)
	}
	for _, rd := range tr.reads {
		if overlaps(rd[0], rd[0]+rd[1], videoAt, videoAt+videoLen) {
			t.Fatalf("read %d:%d overlaps the %d byte video at %d", rd[0], rd[1], videoLen, videoAt)
		}
	}
}

func TestMP4SamplesSkipTheVideo(t *testing.T) {
	file, fillAt, fillLen := sampleMP4(t)
	tr := &traced{b: file}
	out, err := ExtractSparse(context.Background(), tr, int64(len(file)), []ExtractTrack{{Position: 0, Codec: "mov_text", Ext: "srt"}})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out[0])
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "00:00:00,000 --> 00:00:01,000") {
		t.Fatalf("srt %q", got)
	}
	for _, rd := range tr.reads {
		if overlaps(rd[0], rd[0]+rd[1], fillAt, fillAt+fillLen) {
			t.Fatalf("read %d:%d overlaps the video fill at %d", rd[0], rd[1], fillAt)
		}
	}
}

func TestPreciseSpansStayInsideABlock(t *testing.T) {
	// Covered in the tgpool package. This test only locks the subtitle clock helper.
	if srtClock(1500) != "00:00:01,500" {
		t.Fatal(srtClock(1500))
	}
}

type traced struct {
	b     []byte
	mu    sync.Mutex
	reads [][2]int64
}

func (t *traced) ReadAt(p []byte, off int64) (int, error) {
	t.mu.Lock()
	t.reads = append(t.reads, [2]int64{off, int64(len(p))})
	t.mu.Unlock()
	if off < 0 {
		return 0, io.ErrUnexpectedEOF
	}
	if off >= int64(len(t.b)) {
		return 0, io.EOF
	}
	n := copy(p, t.b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func overlaps(a0, a1, b0, b1 int64) bool {
	return a0 < b1 && b0 < a1
}

func sampleMKV(t *testing.T) (file []byte, videoAt, videoLen int64) {
	return buildMKV(t, false)
}

func sampleMKVSeek(t *testing.T) (file []byte, videoAt, videoLen int64) {
	return buildMKV(t, true)
}

func buildMKV(t *testing.T, withSeek bool) (file []byte, videoAt, videoLen int64) {
	t.Helper()
	ebml := mkEl("1A45DFA3", concat(
		mkEl("4286", []byte{1}),
		mkEl("42F7", []byte{1}),
		mkEl("42F2", []byte{4}),
		mkEl("42F3", []byte{8}),
		mkEl("4282", []byte("matroska")),
		mkEl("4287", []byte{4}),
		mkEl("4285", []byte{2}),
	))
	info := mkEl("1549A966", mkEl("2AD7B1", []byte{0x0F, 0x42, 0x40}))
	video := mkEl("AE", concat(
		mkEl("D7", []byte{1}),
		mkEl("83", []byte{1}),
		mkEl("86", []byte("V_UNCOMPRESSED")),
	))
	sub := mkEl("AE", concat(
		mkEl("D7", []byte{2}),
		mkEl("83", []byte{0x11}),
		mkEl("86", []byte("S_TEXT/UTF8")),
	))
	tracks := mkEl("1654AE6B", concat(video, sub))
	payload := bytes.Repeat([]byte{'V'}, 200<<10)
	videoBlock := mkEl("A3", append([]byte{0x81, 0, 0, 0}, payload...))
	block := mkEl("A1", append([]byte{0x82, 0x05, 0xDC, 0x00}, []byte("Hello")...))
	group := mkEl("A0", concat(block, mkEl("9B", []byte{0x03, 0xE8})))
	tc := mkEl("E7", []byte{0})
	clusterBody := concat(tc, videoBlock, group)
	cluster := mkEl("1F43B675", clusterBody)
	var seek []byte
	if withSeek {
		seek = mkEl("114D9B74", mkEl("4DBB", concat(
			mkEl("53AB", []byte{0x1C, 0x53, 0xBB, 0x6B}),
			fixed4("53AC", 0),
		)))
	}
	clusterPos := len(seek) + len(info) + len(tracks)
	groupAt := len(tc) + len(videoBlock)
	point := mkEl("BB", concat(
		mkUint("B3", 1500),
		mkEl("B7", concat(
			mkUint("F7", 2),
			mkUint("F1", uint64(clusterPos)),
			mkUint("F0", uint64(groupAt)),
			mkUint("B2", 1000),
		)),
	))
	cues := mkEl("1C53BB6B", point)
	if withSeek {
		seek = mkEl("114D9B74", mkEl("4DBB", concat(
			mkEl("53AB", []byte{0x1C, 0x53, 0xBB, 0x6B}),
			fixed4("53AC", uint64(clusterPos+len(cluster))),
		)))
	}
	seg := mkEl("18538067", concat(seek, info, tracks, cluster, cues))
	file = concat(ebml, seg)
	_, data, sz, _, err := pullHead(bytes.NewReader(file), 0, int64(len(file)))
	if err != nil {
		t.Fatal(err)
	}
	_, segDataOff, _, _, err := pullHead(bytes.NewReader(file), data+sz, int64(len(file)))
	if err != nil {
		t.Fatal(err)
	}
	clusterFile := segDataOff + int64(clusterPos)
	_, clusterData, _, _, err := pullHead(bytes.NewReader(file), clusterFile, int64(len(file)))
	if err != nil {
		t.Fatal(err)
	}
	blockFile := clusterData + int64(len(tc))
	_, blockData, _, _, err := pullHead(bytes.NewReader(file), blockFile, int64(len(file)))
	if err != nil {
		t.Fatal(err)
	}
	videoAt = blockData + 4 // track, timestamp, flags
	videoLen = int64(len(payload))
	if !bytes.Equal(file[videoAt:videoAt+videoLen], payload) {
		t.Fatalf("video payload not at %d", videoAt)
	}
	return file, videoAt, videoLen
}

func TestMatroskaSeekHeadJumpsPastTheCluster(t *testing.T) {
	file, videoAt, videoLen := sampleMKVSeek(t)
	tr := &traced{b: file}
	out, err := ExtractSparse(context.Background(), tr, int64(len(file)), []ExtractTrack{{Position: 1, Codec: "subrip", Ext: "srt"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out[1]), "Hello") {
		t.Fatalf("srt %q", out[1])
	}
	for _, rd := range tr.reads {
		if overlaps(rd[0], rd[0]+rd[1], videoAt+32, videoAt+videoLen) {
			t.Fatalf("read %d:%d overlaps the video", rd[0], rd[1])
		}
	}
}

func sampleMP4(t *testing.T) (file []byte, fillAt, fillLen int64) {
	t.Helper()
	ftyp := mp4Atom("ftyp", append([]byte("isom"), 0, 0, 0, 0, 'i', 's', 'o', 'm'))
	fill := bytes.Repeat([]byte{0x11}, 200<<10)
	sample := []byte{0x00, 0x05, 'H', 'e', 'l', 'l', 'o'}
	mdat := mp4Atom("mdat", append(fill, sample...))
	sampleOff := len(ftyp) + 8 + len(fill)
	mdhd := make([]byte, 24)
	binary.BigEndian.PutUint32(mdhd[12:], 1000)
	binary.BigEndian.PutUint32(mdhd[16:], 1000)
	hdlr := make([]byte, 25)
	copy(hdlr[8:], "sbtl")
	entry := make([]byte, 16)
	binary.BigEndian.PutUint32(entry[:4], 16)
	copy(entry[4:], "tx3g")
	stsd := make([]byte, 8, 24)
	binary.BigEndian.PutUint32(stsd[4:], 1)
	stsd = append(stsd, entry...)
	stts := make([]byte, 16)
	binary.BigEndian.PutUint32(stts[4:], 1)
	binary.BigEndian.PutUint32(stts[8:], 1)
	binary.BigEndian.PutUint32(stts[12:], 1000)
	stsc := make([]byte, 20)
	binary.BigEndian.PutUint32(stsc[4:], 1)
	binary.BigEndian.PutUint32(stsc[8:], 1)
	binary.BigEndian.PutUint32(stsc[12:], 1)
	binary.BigEndian.PutUint32(stsc[16:], 1)
	stsz := make([]byte, 20)
	binary.BigEndian.PutUint32(stsz[8:], 1)
	binary.BigEndian.PutUint32(stsz[12:], uint32(len(sample)))
	stco := make([]byte, 12)
	binary.BigEndian.PutUint32(stco[4:], 1)
	binary.BigEndian.PutUint32(stco[8:], uint32(sampleOff))
	stbl := mp4Atom("stbl", concat(
		mp4Atom("stsd", stsd), mp4Atom("stts", stts), mp4Atom("stsc", stsc),
		mp4Atom("stsz", stsz), mp4Atom("stco", stco),
	))
	mdia := mp4Atom("mdia", concat(mp4Atom("mdhd", mdhd), mp4Atom("hdlr", hdlr), mp4Atom("minf", stbl)))
	moov := mp4Atom("moov", mp4Atom("trak", mdia))
	file = concat(ftyp, mdat, moov)
	return file, int64(len(ftyp) + 8), int64(len(fill))
}

func mkEl(id string, body []byte) []byte {
	raw, err := hex.DecodeString(id)
	if err != nil {
		panic(err)
	}
	return append(append(raw, sizeVint(uint64(len(body)))...), body...)
}

func mkUint(id string, v uint64) []byte {
	if v == 0 {
		return mkEl(id, []byte{0})
	}
	var b []byte
	for x := v; x > 0; x >>= 8 {
		b = append([]byte{byte(x)}, b...)
	}
	return mkEl(id, b)
}

func fixed4(id string, v uint64) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(v))
	return mkEl(id, b[:])
}

func sizeVint(n uint64) []byte {
	for w := 1; w <= 8; w++ {
		max := (uint64(1) << (7 * w)) - 1
		if n < max {
			out := make([]byte, w)
			v := n
			for i := w - 1; i >= 0; i-- {
				out[i] = byte(v)
				v >>= 8
			}
			out[0] |= 0x80 >> (w - 1)
			return out
		}
	}
	panic("ebml size")
}

func mp4Atom(typ string, body []byte) []byte {
	h := make([]byte, 8)
	binary.BigEndian.PutUint32(h[:4], uint32(8+len(body)))
	copy(h[4:], typ)
	return append(h, body...)
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
