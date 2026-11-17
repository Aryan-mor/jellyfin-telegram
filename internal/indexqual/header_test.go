package indexqual

import (
	"encoding/binary"
	"testing"
)

func TestPictureSizeReadsContainers(t *testing.T) {
	mp4 := sampleMP4(1920, 800)
	w, h, ok := PictureSize(append(make([]byte, 20), mp4...))
	if !ok || w != 1920 || h != 800 {
		t.Fatalf("mp4 %d %d %v", w, h, ok)
	}
	mkv := sampleMKV(1280, 720)
	w, h, ok = PictureSize(mkv)
	if !ok || w != 1280 || h != 720 {
		t.Fatalf("mkv %d %d %v", w, h, ok)
	}
	avi := sampleAVI(854, 480)
	w, h, ok = PictureSize(avi)
	if !ok || w != 854 || h != 480 {
		t.Fatalf("avi %d %d %v", w, h, ok)
	}
	if _, _, ok := PictureSize([]byte("not a video")); ok {
		t.Fatal("plain text looked like a picture")
	}
}

func sampleMP4(width, height uint32) []byte {
	tkhd := make([]byte, 84)
	binary.BigEndian.PutUint32(tkhd[76:], width<<16)
	binary.BigEndian.PutUint32(tkhd[80:], height<<16)
	hdlr := make([]byte, 12)
	copy(hdlr[8:], "vide")
	trak := box("trak", append(box("tkhd", tkhd), box("mdia", box("hdlr", hdlr))...))
	return append(box("ftyp", []byte("isom")), box("moov", trak)...)
}

func box(typ string, body []byte) []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, uint32(8+len(body)))
	out = append(out, typ...)
	return append(out, body...)
}

func sampleMKV(width, height int) []byte {
	video := append(mkvUint(0xB0, width), mkvUint(0xBA, height)...)
	entry := mkvBytes([]byte{0xAE}, append(mkvUint(0x83, 1), mkvBytes([]byte{0xE0}, video)...))
	tracks := mkvBytes([]byte{0x16, 0x54, 0xAE, 0x6B}, entry)
	seg := mkvBytes([]byte{0x18, 0x53, 0x80, 0x67}, tracks)
	ebml := mkvBytes([]byte{0x1A, 0x45, 0xDF, 0xA3}, []byte{0x42, 0x86, 0x81, 0x01})
	return append(ebml, seg...)
}

func mkvBytes(id, body []byte) []byte {
	out := append([]byte{}, id...)
	out = append(out, 0x80|byte(len(body)))
	return append(out, body...)
}

func mkvUint(id byte, v int) []byte {
	var body []byte
	if v < 256 {
		body = []byte{byte(v)}
	} else {
		body = []byte{byte(v >> 8), byte(v)}
	}
	return mkvBytes([]byte{id}, body)
}

func sampleAVI(width, height uint32) []byte {
	b := make([]byte, 64)
	copy(b, "RIFF")
	copy(b[8:], "AVI ")
	copy(b[12:], "avih")
	binary.LittleEndian.PutUint32(b[16:], 56)
	binary.LittleEndian.PutUint32(b[12+8+32:], width)
	binary.LittleEndian.PutUint32(b[12+8+36:], height)
	return b
}
