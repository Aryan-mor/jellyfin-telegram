package subtitles

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
)

const (
	idSegment       = 0x18538067
	idSeekHead      = 0x114D9B74
	idSeek          = 0x4DBB
	idSeekID        = 0x53AB
	idSeekPosition  = 0x53AC
	idInfo          = 0x1549A966
	idTimecodeScale = 0x2AD7B1
	idTracks        = 0x1654AE6B
	idTrackEntry    = 0xAE
	idTrackNumber   = 0xD7
	idTrackType     = 0x83
	idCodecID       = 0x86
	idCodecPrivate  = 0x63A2
	idCluster       = 0x1F43B675
	idCues          = 0x1C53BB6B
	idCuePoint      = 0xBB
	idCueTime       = 0xB3
	idCueTrackPos   = 0xB7
	idCueTrack      = 0xF7
	idCueClusterPos = 0xF1
	idCueRelative   = 0xF0
	idCueDuration   = 0xB2
	idSimpleBlock   = 0xA3
	idBlockGroup    = 0xA0
	idBlock         = 0xA1

	trackSubtitle = 0x11

	maxMeta  = 8 << 20
	maxCues  = 16 << 20
	maxBlock = 4 << 20
)

type mkElem struct {
	id   uint64
	body []byte
}

type mkTrack struct {
	number   uint64
	codec    string
	private  []byte
	subtitle bool
}

type cueRef struct {
	track   uint64
	start   int64
	dur     int64
	hasDur  bool
	cluster int64
	rel     int64
	hasRel  bool
}

func extractMKV(ctx context.Context, r io.ReaderAt, size int64, want []ExtractTrack) (map[int][]byte, error) {
	id, segData, segSize, unknown, err := pullHead(r, 0, size)
	if err != nil {
		return nil, err
	}
	if id != 0x1A45DFA3 {
		return nil, unsupported("missing ebml header")
	}
	if unknown {
		return nil, unsupported("ebml header has no size")
	}
	segAt := segData + segSize
	id, segData, segSize, unknown, err = pullHead(r, segAt, size)
	if err != nil {
		return nil, err
	}
	if id != idSegment {
		return nil, unsupported("missing segment")
	}
	segEnd := size
	if !unknown {
		segEnd = segData + segSize
		if segEnd > size {
			segEnd = size
		}
	}

	var (
		scale     int64 = 1_000_000
		tracks    []mkTrack
		haveTr    bool
		cuesPos   int64
		haveCues  bool
		trPos     int64
		haveTrPos bool
	)
	off := segData
	steps := 0
	for off < segEnd {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		steps++
		if steps > 100000 {
			return nil, unsupported("container header does not end")
		}
		id, data, sz, unk, err := pullHead(r, off, segEnd)
		if err != nil {
			return nil, err
		}
		if unk {
			return nil, unsupported("element size is unknown")
		}
		next := data + sz
		if next <= off {
			return nil, unsupported("element does not advance")
		}
		switch id {
		case idSeekHead:
			body, err := readBody(r, data, sz, maxMeta)
			if err != nil {
				return nil, err
			}
			if pos, ok := seekPos(body, []byte{0x1C, 0x53, 0xBB, 0x6B}); ok {
				cuesPos, haveCues = pos, true
			}
			if pos, ok := seekPos(body, []byte{0x16, 0x54, 0xAE, 0x6B}); ok {
				trPos, haveTrPos = pos, true
			}
		case idInfo:
			body, err := readBody(r, data, sz, maxMeta)
			if err != nil {
				return nil, err
			}
			if s, ok := childUint(body, idTimecodeScale); ok && s > 0 {
				scale = int64(s)
			}
		case idTracks:
			body, err := readBody(r, data, sz, maxMeta)
			if err != nil {
				return nil, err
			}
			tracks, err = parseTracks(body)
			if err != nil {
				return nil, err
			}
			haveTr = true
		case idCues:
			// Handled after the walk so a seek can jump here first. Falling through
			// would read it twice; record the position and stop.
			cuesPos = off - segData
			haveCues = true
		case idCluster:
			if haveCues && (haveTr || haveTrPos) {
				off = segEnd
				continue
			}
		}
		if id == idCues && haveCues && (haveTr || haveTrPos) {
			break
		}
		off = next
	}
	if !haveTr && haveTrPos {
		body, err := elementBody(r, segData+trPos, segEnd, idTracks, maxMeta)
		if err != nil {
			return nil, err
		}
		tracks, err = parseTracks(body)
		if err != nil {
			return nil, err
		}
		haveTr = true
	}
	if !haveTr {
		return nil, unsupported("no tracks")
	}
	if !haveCues {
		return nil, unsupported("no cue index")
	}
	cueBody, err := elementBody(r, segData+cuesPos, size, idCues, maxCues)
	if err != nil {
		return nil, err
	}
	refs, err := parseCues(cueBody)
	if err != nil {
		return nil, err
	}
	return cuesToFiles(ctx, r, size, segData, scale, tracks, refs, want)
}

func elementBody(r io.ReaderAt, off, limit int64, want uint64, max int64) ([]byte, error) {
	id, data, sz, unk, err := pullHead(r, off, limit)
	if err != nil {
		return nil, err
	}
	if unk || id != want {
		return nil, unsupported("index element is missing")
	}
	return readBody(r, data, sz, max)
}

func readBody(r io.ReaderAt, data, sz, max int64) ([]byte, error) {
	if sz < 0 || sz > max {
		return nil, unsupported("index element is too large")
	}
	return readFullAt(r, data, int(sz))
}

func seekPos(body, id []byte) (int64, bool) {
	els, err := iterBuf(body)
	if err != nil {
		return 0, false
	}
	for _, e := range els {
		if e.id != idSeek {
			continue
		}
		kids, err := iterBuf(e.body)
		if err != nil {
			continue
		}
		var got []byte
		var pos uint64
		var ok bool
		for _, k := range kids {
			switch k.id {
			case idSeekID:
				got = k.body
			case idSeekPosition:
				pos = uintBE(k.body)
				ok = true
			}
		}
		if ok && bytes.Equal(got, id) {
			return int64(pos), true
		}
	}
	return 0, false
}

func childUint(body []byte, id uint64) (uint64, bool) {
	els, err := iterBuf(body)
	if err != nil {
		return 0, false
	}
	for _, e := range els {
		if e.id == id {
			return uintBE(e.body), true
		}
	}
	return 0, false
}

func parseTracks(body []byte) ([]mkTrack, error) {
	els, err := iterBuf(body)
	if err != nil {
		return nil, err
	}
	var out []mkTrack
	for _, e := range els {
		if e.id != idTrackEntry {
			continue
		}
		kids, err := iterBuf(e.body)
		if err != nil {
			return nil, err
		}
		var t mkTrack
		for _, k := range kids {
			switch k.id {
			case idTrackNumber:
				t.number = uintBE(k.body)
			case idTrackType:
				t.subtitle = uintBE(k.body) == trackSubtitle
			case idCodecID:
				t.codec = string(k.body)
			case idCodecPrivate:
				t.private = append([]byte(nil), k.body...)
			}
		}
		out = append(out, t)
	}
	return out, nil
}

func parseCues(body []byte) ([]cueRef, error) {
	els, err := iterBuf(body)
	if err != nil {
		return nil, err
	}
	var out []cueRef
	for _, e := range els {
		if e.id != idCuePoint {
			continue
		}
		kids, err := iterBuf(e.body)
		if err != nil {
			return nil, err
		}
		var start int64
		var hasTime bool
		for _, k := range kids {
			if k.id == idCueTime {
				start = int64(uintBE(k.body))
				hasTime = true
			}
		}
		if !hasTime {
			continue
		}
		for _, k := range kids {
			if k.id != idCueTrackPos {
				continue
			}
			ref := cueRef{start: start}
			pos, err := iterBuf(k.body)
			if err != nil {
				return nil, err
			}
			for _, p := range pos {
				switch p.id {
				case idCueTrack:
					ref.track = uintBE(p.body)
				case idCueClusterPos:
					ref.cluster = int64(uintBE(p.body))
				case idCueRelative:
					ref.rel = int64(uintBE(p.body))
					ref.hasRel = true
				case idCueDuration:
					ref.dur = int64(uintBE(p.body))
					ref.hasDur = true
				}
			}
			if ref.hasRel {
				out = append(out, ref)
			}
		}
	}
	return out, nil
}

type blockHit struct {
	start int64
	dur   int64
	has   bool
	text  string
	raw   []byte
}

func cuesToFiles(ctx context.Context, r io.ReaderAt, size, segData, scale int64, tracks []mkTrack, refs []cueRef, want []ExtractTrack) (map[int][]byte, error) {
	byNum := map[uint64]int{}
	for i, t := range tracks {
		byNum[t.number] = i
	}
	wantPos := map[int]ExtractTrack{}
	for _, t := range want {
		wantPos[t.Position] = t
	}
	grouped := map[int][]cueRef{}
	seen := map[[2]int64]bool{}
	for _, ref := range refs {
		i, ok := byNum[ref.track]
		if !ok || !tracks[i].subtitle {
			continue
		}
		if _, ok := wantPos[i]; !ok {
			continue
		}
		key := [2]int64{ref.cluster, ref.rel}
		if seen[key] {
			continue
		}
		seen[key] = true
		grouped[i] = append(grouped[i], ref)
	}
	for pos := range wantPos {
		if pos < 0 || pos >= len(tracks) || !tracks[pos].subtitle {
			return nil, unsupported("track is not an embedded subtitle")
		}
		if !extForCodec(tracks[pos].codec, wantPos[pos].Ext) {
			return nil, unsupported("subtitle codec needs a full read")
		}
		if len(grouped[pos]) == 0 {
			return nil, unsupported("subtitle track has no cues")
		}
	}

	var clusterMu sync.Mutex
	clusters := map[int64]int64{}
	clusterAt := func(pos int64) (int64, error) {
		clusterMu.Lock()
		if v, ok := clusters[pos]; ok {
			clusterMu.Unlock()
			return v, nil
		}
		clusterMu.Unlock()
		id, data, _, unk, err := pullHead(r, segData+pos, size)
		if err != nil {
			return 0, err
		}
		if unk || id != idCluster {
			return 0, unsupported("cue does not point at a cluster")
		}
		clusterMu.Lock()
		clusters[pos] = data
		clusterMu.Unlock()
		return data, nil
	}

	out := map[int][]byte{}
	for pos, refs := range grouped {
		hits := make([]blockHit, len(refs))
		g, _ := errgroup.WithContext(ctx)
		g.SetLimit(8)
		for i, ref := range refs {
			g.Go(func() error {
				if err := ctx.Err(); err != nil {
					return err
				}
				base, err := clusterAt(ref.cluster)
				if err != nil {
					return err
				}
				raw, err := readBlock(r, base+ref.rel, size)
				if err != nil {
					return err
				}
				text, bin, err := blockText(raw)
				if err != nil {
					return err
				}
				hits[i] = blockHit{
					start: unitsToMs(ref.start, scale),
					dur:   unitsToMs(ref.dur, scale),
					has:   ref.hasDur,
					text:  text,
					raw:   bin,
				}
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return nil, err
		}
		file, err := renderTrack(tracks[pos], wantPos[pos].Ext, hits)
		if err != nil {
			return nil, err
		}
		out[pos] = file
	}
	return out, nil
}

func extForCodec(codec, ext string) bool {
	switch codec {
	case "S_TEXT/UTF8", "S_TEXT/ASCII", "S_TEXT/WEBVTT":
		return ext == "srt"
	case "S_TEXT/ASS":
		return ext == "ass"
	case "S_TEXT/SSA":
		return ext == "ssa" || ext == "ass"
	case "S_HDMV/PGS":
		return ext == "sup"
	default:
		return false
	}
}

func readBlock(r io.ReaderAt, off, limit int64) ([]byte, error) {
	id, data, sz, unk, err := pullHead(r, off, limit)
	if err != nil {
		return nil, err
	}
	if unk || (id != idSimpleBlock && id != idBlockGroup && id != idBlock) {
		return nil, unsupported("cue does not point at a subtitle block")
	}
	if sz > maxBlock {
		return nil, unsupported("subtitle block is too large")
	}
	body, err := readFullAt(r, data, int(sz))
	if err != nil {
		return nil, err
	}
	if id == idBlockGroup {
		kids, err := iterBuf(body)
		if err != nil {
			return nil, err
		}
		for _, k := range kids {
			if k.id == idBlock || k.id == idSimpleBlock {
				return k.body, nil
			}
		}
		return nil, unsupported("block group has no block")
	}
	return body, nil
}

func blockText(body []byte) (string, []byte, error) {
	frames, err := blockFrames(body)
	if err != nil {
		return "", nil, err
	}
	var raw []byte
	var lines []string
	for _, f := range frames {
		raw = append(raw, f...)
		lines = append(lines, cleanText(f))
	}
	return strings.Join(lines, "\n"), raw, nil
}

func cleanText(b []byte) string {
	for len(b) > 0 && (b[len(b)-1] == 0 || b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return string(b)
}

func renderTrack(t mkTrack, ext string, hits []blockHit) ([]byte, error) {
	sort.Slice(hits, func(i, j int) bool { return hits[i].start < hits[j].start })
	if ext == "sup" {
		var b bytes.Buffer
		for _, h := range hits {
			b.Write(h.raw)
		}
		return b.Bytes(), nil
	}
	if ext == "ass" || ext == "ssa" {
		var b strings.Builder
		head := string(t.private)
		if head != "" && !strings.HasSuffix(head, "\n") {
			head += "\n"
		}
		b.WriteString(head)
		for _, h := range hits {
			if h.text == "" {
				continue
			}
			b.WriteString(h.text)
			if !strings.HasSuffix(h.text, "\n") {
				b.WriteByte('\n')
			}
		}
		return []byte(b.String()), nil
	}
	var b strings.Builder
	n := 0
	for i, h := range hits {
		if strings.TrimSpace(h.text) == "" {
			continue
		}
		end := h.start + h.dur
		if !h.has || h.dur <= 0 {
			if i+1 < len(hits) && hits[i+1].start > h.start {
				end = hits[i+1].start
			} else {
				end = h.start + 5000
			}
		}
		n++
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", n, srtClock(h.start), srtClock(end), h.text)
	}
	return []byte(b.String()), nil
}

func srtClock(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	h := ms / 3_600_000
	ms %= 3_600_000
	m := ms / 60_000
	ms %= 60_000
	s := ms / 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms%1000)
}

func unitsToMs(v, scale int64) int64 {
	if scale <= 0 {
		scale = 1_000_000
	}
	if v < 0 {
		v = 0
	}
	return (v/1_000_000)*scale + (v%1_000_000)*scale/1_000_000
}

func pullHead(r io.ReaderAt, off, limit int64) (id uint64, data int64, size int64, unknown bool, err error) {
	if off < 0 || off >= limit {
		return 0, 0, 0, false, io.ErrUnexpectedEOF
	}
	n := int(min(int64(12), limit-off))
	buf, err := readFullAt(r, off, n)
	if err != nil {
		return 0, 0, 0, false, err
	}
	id, idn, err := ebmlID(buf)
	if err != nil {
		return 0, 0, 0, false, err
	}
	if idn >= len(buf) {
		return 0, 0, 0, false, io.ErrUnexpectedEOF
	}
	sz, sn, unk, err := ebmlSize(buf[idn:])
	if err != nil {
		return 0, 0, 0, false, err
	}
	return id, off + int64(idn+sn), int64(sz), unk, nil
}

func ebmlID(b []byte) (uint64, int, error) {
	if len(b) == 0 {
		return 0, 0, io.ErrUnexpectedEOF
	}
	n := vintWidth(b[0])
	if n == 0 || n > len(b) || n > 4 {
		return 0, 0, errors.New("bad ebml id")
	}
	return uintBE(b[:n]), n, nil
}

func ebmlSize(b []byte) (uint64, int, bool, error) {
	if len(b) == 0 {
		return 0, 0, false, io.ErrUnexpectedEOF
	}
	n := vintWidth(b[0])
	if n == 0 || n > len(b) || n > 8 {
		return 0, 0, false, errors.New("bad ebml size")
	}
	mask := byte(0x80) >> (n - 1)
	unknown := b[0]&(mask-1) == mask-1
	val := uint64(b[0] & (mask - 1))
	for i := 1; i < n; i++ {
		val = val<<8 | uint64(b[i])
		if b[i] != 0xFF {
			unknown = false
		}
	}
	return val, n, unknown, nil
}

func vintWidth(b byte) int {
	n := 1
	mask := byte(0x80)
	for b&mask == 0 {
		mask >>= 1
		n++
		if n > 8 {
			return 0
		}
	}
	return n
}

func uintBE(b []byte) uint64 {
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v
}

func iterBuf(buf []byte) ([]mkElem, error) {
	var out []mkElem
	i := 0
	for i+1 < len(buf) {
		id, idn, err := ebmlID(buf[i:])
		if err != nil {
			return nil, err
		}
		sz, sn, unk, err := ebmlSize(buf[i+idn:])
		if err != nil {
			return nil, err
		}
		data := i + idn + sn
		if unk {
			out = append(out, mkElem{id: id, body: buf[data:]})
			break
		}
		if sz > uint64(len(buf)-data) {
			return nil, io.ErrUnexpectedEOF
		}
		next := data + int(sz)
		if next <= i {
			return nil, io.ErrUnexpectedEOF
		}
		out = append(out, mkElem{id: id, body: buf[data:next]})
		i = next
	}
	return out, nil
}

func blockFrames(body []byte) ([][]byte, error) {
	_, n, unk, err := ebmlSize(body)
	if err != nil || unk {
		return nil, err
	}
	if n+3 > len(body) {
		return nil, io.ErrUnexpectedEOF
	}
	return unlace(body[n+2], body[n+3:])
}

func unlace(flags byte, data []byte) ([][]byte, error) {
	switch flags & 0x06 {
	case 0:
		return [][]byte{data}, nil
	case 0x02:
		return xiphLace(data)
	case 0x04:
		return fixedLace(data)
	default:
		return ebmlLace(data)
	}
}

func xiphLace(data []byte) ([][]byte, error) {
	if len(data) < 1 {
		return nil, io.ErrUnexpectedEOF
	}
	n := int(data[0]) + 1
	data = data[1:]
	sizes := make([]int, n)
	for i := 0; i < n-1; i++ {
		for {
			if len(data) == 0 {
				return nil, io.ErrUnexpectedEOF
			}
			sizes[i] += int(data[0])
			v := data[0]
			data = data[1:]
			if v < 255 {
				break
			}
		}
	}
	return cutFrames(data, sizes)
}

func fixedLace(data []byte) ([][]byte, error) {
	if len(data) < 1 {
		return nil, io.ErrUnexpectedEOF
	}
	n := int(data[0]) + 1
	data = data[1:]
	if n == 0 || len(data)%n != 0 {
		return nil, io.ErrUnexpectedEOF
	}
	sz := len(data) / n
	sizes := make([]int, n)
	for i := range sizes {
		sizes[i] = sz
	}
	return cutFrames(data, sizes)
}

func ebmlLace(data []byte) ([][]byte, error) {
	if len(data) < 1 {
		return nil, io.ErrUnexpectedEOF
	}
	n := int(data[0]) + 1
	data = data[1:]
	sizes := make([]int, n)
	first, w, unk, err := ebmlSize(data)
	if err != nil || unk {
		return nil, io.ErrUnexpectedEOF
	}
	sizes[0] = int(first)
	data = data[w:]
	for i := 1; i < n-1; i++ {
		diff, w, err := signedSize(data)
		if err != nil {
			return nil, err
		}
		sizes[i] = sizes[i-1] + diff
		if sizes[i] < 0 {
			return nil, io.ErrUnexpectedEOF
		}
		data = data[w:]
	}
	return cutFrames(data, sizes)
}

func signedSize(b []byte) (int, int, error) {
	u, n, unk, err := ebmlSize(b)
	if err != nil || unk || n <= 0 {
		return 0, 0, io.ErrUnexpectedEOF
	}
	bias := (int64(1) << (7*n - 1)) - 1
	return int(int64(u) - bias), n, nil
}

func cutFrames(data []byte, sizes []int) ([][]byte, error) {
	sum := 0
	for i := 0; i < len(sizes)-1; i++ {
		if sizes[i] < 0 {
			return nil, io.ErrUnexpectedEOF
		}
		sum += sizes[i]
	}
	if sum > len(data) {
		return nil, io.ErrUnexpectedEOF
	}
	if len(sizes) == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	sizes[len(sizes)-1] = len(data) - sum
	out := make([][]byte, len(sizes))
	at := 0
	for i, sz := range sizes {
		if sz < 0 || at+sz > len(data) {
			return nil, io.ErrUnexpectedEOF
		}
		out[i] = data[at : at+sz]
		at += sz
	}
	return out, nil
}
