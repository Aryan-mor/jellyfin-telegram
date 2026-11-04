package tgpool

import (
	"testing"

	"github.com/gotd/td/tg"
)

func message(doc *tg.Document, caption string) *tg.Message {
	return &tg.Message{ID: 7, Date: 1700000000, Message: caption, Media: &tg.MessageMediaDocument{Document: doc}}
}

func TestFileFromMessage(t *testing.T) {
	doc := &tg.Document{
		ID: 9, Size: 5000, MimeType: "video/x-matroska",
		Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "Heat.1995.mkv"}},
		Thumbs:     []tg.PhotoSizeClass{&tg.PhotoSize{Type: "m", W: 90, H: 90}},
	}
	f := fileFromMessage(message(doc, "a caption"), 55, false)
	if f == nil {
		t.Fatal("video not recognised")
	}
	if f.MessageID != 7 || f.ChannelID != 55 || f.DocID != 9 || f.Size != 5000 {
		t.Errorf("ids and size wrong: %+v", f)
	}
	if f.FileName != "Heat.1995.mkv" || f.Caption != "a caption" || !f.HasThumb {
		t.Errorf("details wrong: %+v", f)
	}
	if f.Date.Unix() != 1700000000 {
		t.Errorf("date = %v", f.Date)
	}
}

func TestFileFromMessageRecognisesVideos(t *testing.T) {
	name := func(n string) tg.DocumentAttributeClass { return &tg.DocumentAttributeFilename{FileName: n} }
	tests := []struct {
		label string
		doc   *tg.Document
		want  bool
	}{
		{"video mime", &tg.Document{MimeType: "video/mp4"}, true},
		{"video attribute", &tg.Document{MimeType: "application/octet-stream",
			Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeVideo{}}}, true},
		{"extension only", &tg.Document{MimeType: "application/octet-stream",
			Attributes: []tg.DocumentAttributeClass{name("Movie.MKV")}}, true},
		{"subtitle", &tg.Document{MimeType: "text/plain",
			Attributes: []tg.DocumentAttributeClass{name("Movie.srt")}}, false},
		{"archive", &tg.Document{MimeType: "application/zip",
			Attributes: []tg.DocumentAttributeClass{name("Movie.zip")}}, false},
	}
	for _, tc := range tests {
		if got := fileFromMessage(message(tc.doc, ""), 1, false) != nil; got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.label, got, tc.want)
		}
	}
}

func TestFileFromMessageIgnoresOtherMessages(t *testing.T) {
	if fileFromMessage(&tg.Message{ID: 1, Message: "just text"}, 1, false) != nil {
		t.Error("text message treated as a file")
	}
	if fileFromMessage(&tg.Message{ID: 1, Media: &tg.MessageMediaPhoto{}}, 1, false) != nil {
		t.Error("photo treated as a file")
	}
	if fileFromMessage(&tg.Message{ID: 1, Media: &tg.MessageMediaDocument{Document: &tg.DocumentEmpty{}}}, 1, false) != nil {
		t.Error("empty document treated as a file")
	}
}

func TestAttachmentFromMessage(t *testing.T) {
	named := func(n string) *tg.Document {
		return &tg.Document{ID: 3, Size: 9, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: n}}}
	}
	m := message(named("Heat.fa.SRT"), "")
	m.ReplyTo = &tg.MessageReplyHeader{ReplyToMsgID: 5}
	a := attachmentFromMessage(m)
	if a == nil || a.DocID != 3 || a.ReplyTo != 5 || a.FileName != "Heat.fa.SRT" {
		t.Fatalf("got %+v", a)
	}
	if attachmentFromMessage(message(named("subs.zip"), "")) == nil {
		t.Error("zip not recognised")
	}
	if attachmentFromMessage(message(named("Heat.mkv"), "")) != nil {
		t.Error("video counted as a subtitle")
	}
	if attachmentFromMessage(&tg.Message{ID: 1, Message: "text"}) != nil {
		t.Error("text counted as a subtitle")
	}
}

func TestLargestThumb(t *testing.T) {
	doc := &tg.Document{Thumbs: []tg.PhotoSizeClass{
		&tg.PhotoStrippedSize{Type: "i"},
		&tg.PhotoSize{Type: "s", W: 90, H: 50},
		&tg.PhotoSizeProgressive{Type: "y", W: 800, H: 450},
		&tg.PhotoSize{Type: "m", W: 320, H: 180},
	}}
	if got := largestThumb(doc); got != "y" {
		t.Errorf("got %q", got)
	}
	if got := largestThumb(&tg.Document{}); got != "" {
		t.Errorf("no thumbs gave %q", got)
	}
}
