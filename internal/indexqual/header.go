package indexqual

import (
	"bytes"
	"encoding/binary"
)

// PictureSize reads the video width and height from the start or the end of a container.
// The buffer is a few megabytes, not the whole file. ok is false when the header is not there.
func PictureSize(b []byte) (width, height int, ok bool) {
	if w, h, ok := mp4Size(b); ok {
		return w, h, true
	}
	if w, h, ok := mkvSize(b); ok {
		return w, h, true
	}
	if w, h, ok := aviSize(b); ok {
		return w, h, true
	}
	return 0, 0, false
}

func sane(w, h int) bool { return w > 0 && h > 0 && w <= 10000 && h <= 10000 }

func mp4Size(b []byte) (int, int, bool) {
	body, ok := moovBody(b)
	if !ok {
		return 0, 0, false
	}
	bestW, bestH := 0, 0
	eachBox(body, func(typ string, payload []byte) bool {
		if typ != "trak" {
			return true
		}
		w, h, ok := trakPixels(payload)
		if ok && w*h > bestW*bestH {
			bestW, bestH = w, h
		}
		return true
	})
	if !sane(bestW, bestH) {
		return 0, 0, false
	}
	return bestW, bestH, true
}

func moovBody(b []byte) ([]byte, bool) {
	start := 0
	for start < len(b) {
		i := bytes.Index(b[start:], []byte("moov"))
		if i < 0 {
			return nil, false
		}
		at := start + i
		if at >= 4 {
			size := int(binary.BigEndian.Uint32(b[at-4 : at]))
			header := 8
			base := at - 4
			if size == 1 && at+12 <= len(b) {
				size = int(binary.BigEndian.Uint64(b[at+4 : at+12]))
				header = 16
			}
			if size >= header && base+size <= len(b) {
				return b[base+header : base+size], true
			}
		}
		start = at + 4
	}
	return nil, false
}

func trakPixels(trak []byte) (int, int, bool) {
	video := false
	var w, h int
	var have bool
	eachBox(trak, func(typ string, body []byte) bool {
		switch typ {
		case "tkhd":
			w, h, have = tkhdSize(body)
		case "mdia":
			eachBox(body, func(typ string, body []byte) bool {
				if typ == "hdlr" && hdlrVideo(body) {
					video = true
				}
				return true
			})
		}
		return true
	})
	if !video || !have || !sane(w, h) {
		return 0, 0, false
	}
	return w, h, true
}

func tkhdSize(b []byte) (int, int, bool) {
	if len(b) < 4 {
		return 0, 0, false
	}
	off := 76
	if b[0] == 1 {
		off = 88
	}
	if len(b) < off+8 {
		return 0, 0, false
	}
	w := int(binary.BigEndian.Uint32(b[off:off+4]) >> 16)
	h := int(binary.BigEndian.Uint32(b[off+4:off+8]) >> 16)
	return w, h, sane(w, h)
}

func hdlrVideo(b []byte) bool {
	return len(b) >= 12 && string(b[8:12]) == "vide"
}

func eachBox(b []byte, fn func(typ string, body []byte) bool) {
	i := 0
	for i+8 <= len(b) {
		size := int(binary.BigEndian.Uint32(b[i : i+4]))
		typ := string(b[i+4 : i+8])
		header := 8
		if size == 1 {
			if i+16 > len(b) {
				return
			}
			size = int(binary.BigEndian.Uint64(b[i+8 : i+16]))
			header = 16
		}
		if size == 0 || size < header || i+size > len(b) {
			return
		}
		if !fn(typ, b[i+header:i+size]) {
			return
		}
		i += size
	}
}

func aviSize(b []byte) (int, int, bool) {
	if len(b) < 12 || string(b[:4]) != "RIFF" {
		return 0, 0, false
	}
	i := bytes.Index(b, []byte("avih"))
	if i < 0 || i+8+40 > len(b) {
		return 0, 0, false
	}
	data := b[i+8:]
	w := int(binary.LittleEndian.Uint32(data[32:36]))
	h := int(binary.LittleEndian.Uint32(data[36:40]))
	if !sane(w, h) {
		return 0, 0, false
	}
	return w, h, true
}

const (
	idSegment    = 0x18538067
	idTracks     = 0x1654AE6B
	idTrackEntry = 0xAE
	idTrackType  = 0x83
	idVideo      = 0xE0
	idPixelW     = 0xB0
	idPixelH     = 0xBA
	trackVideo   = 1
)

func mkvSize(b []byte) (int, int, bool) {
	if len(b) < 4 || b[0] != 0x1A {
		return 0, 0, false
	}
	bestW, bestH := 0, 0
	walkMKV(b, func(id uint64, body []byte) {
		if id != idTrackEntry {
			return
		}
		w, h, ok := mkvTrack(body)
		if ok && w*h > bestW*bestH {
			bestW, bestH = w, h
		}
	})
	if !sane(bestW, bestH) {
		return 0, 0, false
	}
	return bestW, bestH, true
}

func walkMKV(b []byte, fn func(id uint64, body []byte)) {
	i := 0
	for i+2 <= len(b) {
		id, in, ok := ebmlID(b[i:])
		if !ok {
			return
		}
		sz, sn, unknown, ok := ebmlSize(b[i+in:])
		if !ok {
			return
		}
		start := i + in + sn
		if unknown {
			visitMKV(id, b[start:], fn)
			return
		}
		if sz > uint64(len(b)-start) {
			return
		}
		end := start + int(sz)
		visitMKV(id, b[start:end], fn)
		i = end
	}
}

func visitMKV(id uint64, body []byte, fn func(id uint64, body []byte)) {
	switch id {
	case idSegment, idTracks:
		walkMKV(body, fn)
	case idTrackEntry:
		fn(id, body)
	}
}

func mkvTrack(body []byte) (int, int, bool) {
	var typ int
	var w, h int
	var haveW, haveH bool
	i := 0
	for i+2 <= len(body) {
		id, in, ok := ebmlID(body[i:])
		if !ok {
			break
		}
		sz, sn, unknown, ok := ebmlSize(body[i+in:])
		if !ok || unknown {
			break
		}
		start := i + in + sn
		if sz > uint64(len(body)-start) {
			break
		}
		el := body[start : start+int(sz)]
		switch id {
		case idTrackType:
			typ = ebmlUint(el)
		case idVideo:
			w, h, haveW, haveH = videoBox(el)
		}
		i = start + int(sz)
	}
	if typ != trackVideo || !haveW || !haveH || !sane(w, h) {
		return 0, 0, false
	}
	return w, h, true
}

func videoBox(body []byte) (w, h int, haveW, haveH bool) {
	i := 0
	for i+2 <= len(body) {
		id, in, ok := ebmlID(body[i:])
		if !ok {
			break
		}
		sz, sn, unknown, ok := ebmlSize(body[i+in:])
		if !ok || unknown {
			break
		}
		start := i + in + sn
		if sz > uint64(len(body)-start) {
			break
		}
		el := body[start : start+int(sz)]
		switch id {
		case idPixelW:
			w, haveW = ebmlUint(el), true
		case idPixelH:
			h, haveH = ebmlUint(el), true
		}
		i = start + int(sz)
	}
	return w, h, haveW, haveH
}

func ebmlUint(b []byte) int {
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	if v > 10000 {
		return 0
	}
	return int(v)
}

func ebmlID(b []byte) (uint64, int, bool) {
	if len(b) == 0 {
		return 0, 0, false
	}
	n := vintWidth(b[0])
	if n == 0 || n > len(b) || n > 4 {
		return 0, 0, false
	}
	return uintBE(b[:n]), n, true
}

func ebmlSize(b []byte) (uint64, int, bool, bool) {
	if len(b) == 0 {
		return 0, 0, false, false
	}
	n := vintWidth(b[0])
	if n == 0 || n > len(b) || n > 8 {
		return 0, 0, false, false
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
	return val, n, unknown, true
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
