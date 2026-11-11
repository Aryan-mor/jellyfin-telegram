package subtitles

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
	"strings"
)

func extractMP4(ctx context.Context, r io.ReaderAt, size int64, want []ExtractTrack) (map[int][]byte, error) {
	var moov []byte
	off := int64(0)
	for off < size {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		typ, data, end, err := pullAtom(r, off, size)
		if err != nil {
			return nil, err
		}
		if end <= off {
			return nil, unsupported("mp4 atom does not advance")
		}
		if typ == "moov" {
			n := end - data
			if n < 0 || n > 32<<20 {
				return nil, unsupported("moov is too large")
			}
			moov, err = readFullAt(r, data, int(n))
			if err != nil {
				return nil, err
			}
		}
		off = end
	}
	if moov == nil {
		return nil, unsupported("mp4 has no moov")
	}
	traks, err := parseMoov(moov)
	if err != nil {
		return nil, err
	}
	wantPos := map[int]ExtractTrack{}
	for _, t := range want {
		wantPos[t.Position] = t
	}
	out := map[int][]byte{}
	for pos, t := range wantPos {
		if pos < 0 || pos >= len(traks) || !traks[pos].subtitle || t.Ext != "srt" {
			return nil, unsupported("mp4 track is not a text subtitle")
		}
		file, err := readMP4Track(r, size, traks[pos])
		if err != nil {
			return nil, err
		}
		out[pos] = file
	}
	return out, nil
}

type mp4Track struct {
	subtitle bool
	scale    uint32
	samples  []mp4Sample
}

type mp4Sample struct {
	off   int64
	n     int
	start int64
	dur   int64
}

func parseMoov(body []byte) ([]mp4Track, error) {
	atoms, err := iterAtoms(body)
	if err != nil {
		return nil, err
	}
	var out []mp4Track
	for _, a := range atoms {
		if a.typ != "trak" {
			continue
		}
		tr, err := parseTrak(a.body)
		if err != nil {
			return nil, err
		}
		out = append(out, tr)
	}
	return out, nil
}

func parseTrak(body []byte) (mp4Track, error) {
	var tr mp4Track
	mdia, ok := findAtom(body, "mdia")
	if !ok {
		return tr, nil
	}
	if hdlr, ok := findAtom(mdia, "hdlr"); ok && len(hdlr) >= 12 {
		kind := string(hdlr[8:12])
		tr.subtitle = kind == "sbtl" || kind == "subt" || kind == "text"
	}
	if mdhd, ok := findAtom(mdia, "mdhd"); ok {
		tr.scale = mdhdScale(mdhd)
	}
	minf, ok := findAtom(mdia, "minf")
	if !ok {
		return tr, nil
	}
	stbl, ok := findAtom(minf, "stbl")
	if !ok {
		return tr, nil
	}
	if stsd, ok := findAtom(stbl, "stsd"); ok && len(stsd) >= 16 {
		codec := string(stsd[12:16])
		if codec == "tx3g" || codec == "text" {
			tr.subtitle = true
		}
	}
	if !tr.subtitle {
		return tr, nil
	}
	samples, err := samplesOf(stbl, tr.scale)
	if err != nil {
		return tr, err
	}
	tr.samples = samples
	return tr, nil
}

func mdhdScale(body []byte) uint32 {
	if len(body) < 4 {
		return 0
	}
	if body[0] == 1 {
		if len(body) < 24 {
			return 0
		}
		return binary.BigEndian.Uint32(body[20:24])
	}
	if len(body) < 16 {
		return 0
	}
	return binary.BigEndian.Uint32(body[12:16])
}

func samplesOf(stbl []byte, scale uint32) ([]mp4Sample, error) {
	if scale == 0 {
		scale = 1000
	}
	stts, _ := findAtom(stbl, "stts")
	stsc, _ := findAtom(stbl, "stsc")
	stsz, _ := findAtom(stbl, "stsz")
	offs, err := chunkOffsets(stbl)
	if err != nil {
		return nil, err
	}
	deltas := sttsDeltas(stts)
	sizes, err := stszSizes(stsz)
	if err != nil {
		return nil, err
	}
	perChunk := stscCounts(stsc, len(offs))
	var out []mp4Sample
	var tick int64
	si := 0
	for ci, base := range offs {
		n := 1
		if ci < len(perChunk) && perChunk[ci] > 0 {
			n = perChunk[ci]
		}
		pos := base
		for k := 0; k < n && si < len(sizes); k++ {
			dur := int64(1000)
			if si < len(deltas) {
				dur = deltas[si]
			}
			out = append(out, mp4Sample{
				off: pos, n: sizes[si],
				start: tick * 1000 / int64(scale),
				dur:   dur * 1000 / int64(scale),
			})
			pos += int64(sizes[si])
			tick += dur
			si++
			if len(out) > 200000 {
				return nil, unsupported("too many subtitle samples")
			}
		}
	}
	return out, nil
}

func sttsDeltas(body []byte) []int64 {
	if len(body) < 8 {
		return nil
	}
	count := int(binary.BigEndian.Uint32(body[4:8]))
	var out []int64
	at := 8
	for i := 0; i < count && at+8 <= len(body); i++ {
		n := int(binary.BigEndian.Uint32(body[at : at+4]))
		d := int64(binary.BigEndian.Uint32(body[at+4 : at+8]))
		at += 8
		for j := 0; j < n && len(out) < 200000; j++ {
			out = append(out, d)
		}
	}
	return out
}

func stszSizes(body []byte) ([]int, error) {
	if len(body) < 12 {
		return nil, unsupported("mp4 sample sizes are missing")
	}
	fixed := binary.BigEndian.Uint32(body[4:8])
	count := int(binary.BigEndian.Uint32(body[8:12]))
	if count < 0 || count > 200000 {
		return nil, unsupported("too many subtitle samples")
	}
	if fixed != 0 {
		out := make([]int, count)
		for i := range out {
			out[i] = int(fixed)
		}
		return out, nil
	}
	if 12+count*4 > len(body) {
		return nil, unsupported("mp4 sample sizes are truncated")
	}
	out := make([]int, count)
	for i := range out {
		out[i] = int(binary.BigEndian.Uint32(body[12+i*4:]))
	}
	return out, nil
}

func stscCounts(body []byte, chunks int) []int {
	out := make([]int, chunks)
	if len(body) < 8 || chunks == 0 {
		return out
	}
	count := int(binary.BigEndian.Uint32(body[4:8]))
	type row struct {
		first int
		n     int
	}
	var rows []row
	at := 8
	for i := 0; i < count && at+12 <= len(body); i++ {
		rows = append(rows, row{
			first: int(binary.BigEndian.Uint32(body[at:])),
			n:     int(binary.BigEndian.Uint32(body[at+4:])),
		})
		at += 12
	}
	for i := range out {
		n := 1
		chunk := i + 1
		for _, row := range rows {
			if row.first <= chunk {
				n = row.n
			}
		}
		out[i] = n
	}
	return out
}

func chunkOffsets(stbl []byte) ([]int64, error) {
	if body, ok := findAtom(stbl, "co64"); ok {
		return co64(body), nil
	}
	body, ok := findAtom(stbl, "stco")
	if !ok {
		return nil, unsupported("mp4 chunk offsets are missing")
	}
	if len(body) < 8 {
		return nil, unsupported("mp4 chunk offsets are missing")
	}
	count := int(binary.BigEndian.Uint32(body[4:8]))
	if 8+count*4 > len(body) {
		return nil, unsupported("mp4 chunk offsets are truncated")
	}
	out := make([]int64, count)
	for i := range out {
		out[i] = int64(binary.BigEndian.Uint32(body[8+i*4:]))
	}
	return out, nil
}

func co64(body []byte) []int64 {
	if len(body) < 8 {
		return nil
	}
	count := int(binary.BigEndian.Uint32(body[4:8]))
	if 8+count*8 > len(body) {
		return nil
	}
	out := make([]int64, count)
	for i := range out {
		out[i] = int64(binary.BigEndian.Uint64(body[8+i*8:]))
	}
	return out
}

func readMP4Track(r io.ReaderAt, size int64, tr mp4Track) ([]byte, error) {
	samples := append([]mp4Sample(nil), tr.samples...)
	sort.SliceStable(samples, func(i, j int) bool { return samples[i].off < samples[j].off })
	for _, s := range samples {
		if s.off < 0 || s.n < 0 || s.off+int64(s.n) > size {
			return nil, unsupported("subtitle sample is outside the file")
		}
	}
	type piece struct {
		off, end int64
		idx      []int
	}
	var groups []piece
	for i, s := range samples {
		end := s.off + int64(s.n)
		if len(groups) > 0 && s.off <= groups[len(groups)-1].end+1024 {
			if end > groups[len(groups)-1].end {
				groups[len(groups)-1].end = end
			}
			groups[len(groups)-1].idx = append(groups[len(groups)-1].idx, i)
			continue
		}
		groups = append(groups, piece{off: s.off, end: end, idx: []int{i}})
	}
	got := make([][]byte, len(samples))
	for _, g := range groups {
		buf, err := readFullAt(r, g.off, int(g.end-g.off))
		if err != nil {
			return nil, err
		}
		for _, i := range g.idx {
			s := samples[i]
			at := int(s.off - g.off)
			if at < 0 || at+s.n > len(buf) {
				return nil, unsupported("subtitle sample is outside the file")
			}
			got[i] = buf[at : at+s.n]
		}
	}
	byOff := map[int64][]byte{}
	for i, s := range samples {
		byOff[s.off] = got[i]
	}
	var b strings.Builder
	n := 0
	for _, s := range tr.samples {
		text := tx3gText(byOff[s.off])
		if strings.TrimSpace(text) == "" {
			continue
		}
		end := s.start + s.dur
		if s.dur <= 0 {
			end = s.start + 5000
		}
		n++
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", n, srtClock(s.start), srtClock(end), text)
	}
	return []byte(b.String()), nil
}

func tx3gText(sample []byte) string {
	if len(sample) >= 2 {
		n := int(binary.BigEndian.Uint16(sample[:2]))
		if n >= 0 && 2+n <= len(sample) {
			return cleanText(sample[2 : 2+n])
		}
	}
	return cleanText(sample)
}

type atom struct {
	typ  string
	body []byte
}

func iterAtoms(buf []byte) ([]atom, error) {
	var out []atom
	i := 0
	for i+8 <= len(buf) {
		sz := int64(binary.BigEndian.Uint32(buf[i:]))
		typ := string(buf[i+4 : i+8])
		data := i + 8
		var end int
		switch sz {
		case 1:
			if i+16 > len(buf) {
				return nil, unsupported("mp4 atom is truncated")
			}
			sz = int64(binary.BigEndian.Uint64(buf[i+8:]))
			data = i + 16
			end = i + int(sz)
		case 0:
			end = len(buf)
		default:
			end = i + int(sz)
		}
		if end < data || end > len(buf) {
			return nil, unsupported("mp4 atom is truncated")
		}
		out = append(out, atom{typ: typ, body: buf[data:end]})
		if end <= i {
			return nil, unsupported("mp4 atom does not advance")
		}
		i = end
	}
	return out, nil
}

func findAtom(body []byte, typ string) ([]byte, bool) {
	atoms, err := iterAtoms(body)
	if err != nil {
		return nil, false
	}
	for _, a := range atoms {
		if a.typ == typ {
			return a.body, true
		}
	}
	return nil, false
}

func pullAtom(r io.ReaderAt, off, limit int64) (typ string, data, end int64, err error) {
	if off < 0 || off+8 > limit {
		return "", 0, 0, io.ErrUnexpectedEOF
	}
	hdr, err := readFullAt(r, off, 8)
	if err != nil {
		return "", 0, 0, err
	}
	sz := binary.BigEndian.Uint32(hdr[:4])
	typ = string(hdr[4:8])
	data = off + 8
	switch sz {
	case 1:
		ext, err := readFullAt(r, off+8, 8)
		if err != nil {
			return "", 0, 0, err
		}
		data = off + 16
		end = off + int64(binary.BigEndian.Uint64(ext))
	case 0:
		end = limit
	default:
		end = off + int64(sz)
	}
	if end < data || end > limit {
		return "", 0, 0, unsupported("mp4 atom is truncated")
	}
	return typ, data, end, nil
}
