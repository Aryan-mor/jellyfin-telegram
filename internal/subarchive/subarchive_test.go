package subarchive

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/jellyfin"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
	"github.com/Aryan-mor/jellyfin-telegram/internal/subtitles"
)

func TestNameRoundTrip(t *testing.T) {
	for _, doc := range []int64{5123279364263249911, -55} {
		name := Name(42, doc)
		msg, got, ok := ParseName(name)
		if !ok || msg != 42 || got != doc {
			t.Errorf("%s gave %d, %d, %v", name, msg, got, ok)
		}
		if !IsArchiveName(name) {
			t.Errorf("%s is not recognised", name)
		}
	}
	for _, name := range []string{"Movie.srt", "jftg-subs-.zip", "jftg-subs-1.zip", "x-jftg-subs-1-2.zip"} {
		if _, _, ok := ParseName(name); ok {
			t.Errorf("%q parsed", name)
		}
	}
}

func TestArchiveRoundTrip(t *testing.T) {
	entries := []Entry{
		{Position: 2, Ext: "srt", Language: "fa", Data: []byte("first")},
		{Position: 7, Ext: "ass", Data: []byte("second")},
		{Position: 9, Ext: "sup", Data: []byte{0, 1, 2}},
	}
	data, err := Build(entries)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("entries = %+v", got)
	}
	for i := range entries {
		if got[i].Position != entries[i].Position || got[i].Ext != entries[i].Ext ||
			got[i].Language != entries[i].Language || !bytes.Equal(got[i].Data, entries[i].Data) {
			t.Errorf("entry %d = %+v", i, got[i])
		}
	}
}

func TestReadRejectsDamagedArchive(t *testing.T) {
	if _, err := Read([]byte("not a zip")); err == nil {
		t.Error("garbage accepted")
	}
	noManifest, err := buildRaw(map[string][]byte{"2.srt": []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Read(noManifest); err == nil {
		t.Error("archive without a manifest accepted")
	}
	missing, err := buildRaw(map[string][]byte{manifestName: []byte(`[{"position":2,"ext":"srt"}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Read(missing); err == nil {
		t.Error("archive missing a track accepted")
	}
}

func TestPlaceholderIsNeverEmpty(t *testing.T) {
	// Jellyfin extracts again when a cache file is empty.
	for _, ext := range []string{"srt", "ass", "sup"} {
		if len(placeholder(ext)) == 0 {
			t.Errorf("%s placeholder is empty", ext)
		}
	}
}

func TestCountCues(t *testing.T) {
	srt := Entry{Ext: "srt", Data: []byte("1\n00:00:01,000 --> 00:00:02,000\nx\n\n2\n00:00:03,000 --> 00:00:04,000\ny\n")}
	if countCues(srt) != 2 {
		t.Errorf("srt = %d", countCues(srt))
	}
	ass := Entry{Ext: "ass", Data: []byte("[Events]\nDialogue: 0,a\nDialogue: 0,b\nDialogue: 0,c\n")}
	if countCues(ass) != 3 {
		t.Errorf("ass = %d", countCues(ass))
	}
}

func TestFolderMatchesJellyfinLayout(t *testing.T) {
	w := &Worker{SubtitlesRoot: "root"}
	got, err := w.folder("cc4155578255bc8b607e5299583bdb0d")
	if err != nil {
		t.Fatal(err)
	}
	want := "root/cc/cc415557-8255-bc8b-607e-5299583bdb0d"
	if toSlash(got) != want {
		t.Errorf("got %s", got)
	}
	if _, err := w.folder("../../etc"); err == nil {
		t.Error("a path was accepted as an id")
	}
}

func TestBelongs(t *testing.T) {
	ep := &media.File{FileName: "Lost.S01E02.720p.mkv"}
	movie := &media.File{FileName: "Heat.1995.1080p.mkv"}
	tests := []struct {
		name  string
		a     media.Attachment
		video *media.File
		want  bool
	}{
		{"reply to this video", media.Attachment{FileName: "anything.srt", ReplyTo: 5}, ep, true},
		{"reply to another video", media.Attachment{FileName: "Lost.S01E02.srt", ReplyTo: 9}, ep, false},
		{"same episode by name", media.Attachment{FileName: "Lost.S01E02.WEB.srt"}, ep, true},
		{"other episode by name", media.Attachment{FileName: "Lost.S01E03.srt"}, ep, false},
		{"other show by name", media.Attachment{FileName: "Dark.S01E02.srt"}, ep, false},
		{"same movie by name", media.Attachment{FileName: "Heat.1995.srt"}, movie, true},
		{"unknown video", media.Attachment{FileName: "Heat.1995.srt"}, nil, false},
	}
	for _, tc := range tests {
		if got := belongs(tc.a, 5, tc.video); got != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
}

type fakeLocal struct {
	file  *subtitles.SubFile
	calls int
}

func (f *fakeLocal) Lookup(context.Context, subtitles.Meta) (*subtitles.SubFile, error) {
	f.calls++
	return f.file, nil
}

func hollowRig(t *testing.T, local *fakeLocal) (*Worker, *catalog.Catalog, media.File) {
	t.Helper()
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cat.Close() })
	f := media.File{MessageID: 1, DocID: 2, DurationSeconds: 100 * 60, RelPath: "a", Date: time.Unix(1, 0)}
	if err := cat.Put(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &Worker{
		Catalog: cat, Log: log,
		Prober:   jellyfin.NewProber(nil, cat, nil, log),
		Resolver: &subtitles.Resolver{Language: "de", Local: local},
	}, cat, f
}

func cueText(n int) []byte {
	return []byte(strings.Repeat("1\n00:00:01,000 --> 00:00:02,000\nx\n\n", n))
}

// Real case: a Persian-tagged track with two cues, in a film of over an hour.
func TestHollowTrackIsReplaced(t *testing.T) {
	replacement := &subtitles.SubFile{Name: "r.srt", Ext: ".srt", Text: "1\n01:38:00,000 --> 01:38:02,000\nreal\n"}
	local := &fakeLocal{file: replacement}
	w, cat, f := hollowRig(t, local)
	entries := []Entry{
		{Position: 2, Ext: "srt", Language: "en", Data: cueText(900)},
		{Position: 3, Ext: "srt", Language: "de", Data: cueText(2)},
	}
	w.replaceHollow(context.Background(), f, jellyfin.Located{}, "u", jellyfin.SubtitleInfo{}, entries)
	if string(entries[1].Data) != replacement.Text {
		t.Errorf("hollow track kept: %q", entries[1].Data)
	}
	if string(entries[0].Data) == replacement.Text {
		t.Error("a track in another language was replaced")
	}
	got, err := cat.Get(context.Background(), 1)
	if err != nil || got.SubtitleOutcome != subtitles.OutcomeHollow {
		t.Errorf("outcome %q, %v", got.SubtitleOutcome, err)
	}
}

func TestRealTrackIsKept(t *testing.T) {
	local := &fakeLocal{file: &subtitles.SubFile{Ext: ".srt", Text: "other"}}
	w, _, f := hollowRig(t, local)
	entries := []Entry{{Position: 3, Ext: "srt", Language: "de", Data: cueText(900)}}
	w.replaceHollow(context.Background(), f, jellyfin.Located{}, "u", jellyfin.SubtitleInfo{}, entries)
	if string(entries[0].Data) == "other" || local.calls != 0 {
		t.Errorf("a full track was touched, lookups %d", local.calls)
	}
}

func TestOneRealTrackIsEnoughAmongHollowOnes(t *testing.T) {
	local := &fakeLocal{file: &subtitles.SubFile{Ext: ".srt", Text: "other"}}
	w, _, f := hollowRig(t, local)
	entries := []Entry{
		{Position: 2, Ext: "srt", Language: "de", Data: cueText(1)},
		{Position: 3, Ext: "srt", Language: "de", Data: cueText(900)},
	}
	w.replaceHollow(context.Background(), f, jellyfin.Located{}, "u", jellyfin.SubtitleInfo{}, entries)
	if local.calls != 0 {
		t.Error("looked for a replacement although a real track exists")
	}
}

func TestArchiveIsFoundByTheDocumentIdOfItsVideo(t *testing.T) {
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cat.Close() })
	ctx := context.Background()
	for _, a := range []media.Attachment{
		{MessageID: 5, DocID: 50, Size: 1, FileName: Name(900, 777)},  // made for another message id
		{MessageID: 6, DocID: 0, Size: 1, FileName: Name(12, 888)},    // just sent, not synced yet
		{MessageID: 7, DocID: 70, Size: 1, FileName: Name(12, 999)},   // another video
		{MessageID: 8, DocID: 80, Size: 1, FileName: "notes-777.zip"}, // not an archive of ours
	} {
		if err := cat.PutAttachment(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	w := &Worker{Catalog: cat}
	// The same video posted again, with another message id, still finds its archive.
	got, ok, err := w.findArchive(ctx, media.File{MessageID: 41, DocID: 777})
	if err != nil || !ok || got.MessageID != 5 {
		t.Fatalf("by document id: %+v %v %v", got, ok, err)
	}
	// An exact name wins over a match by document id.
	if err := cat.PutAttachment(ctx, media.Attachment{MessageID: 9, DocID: 90, Size: 1, FileName: Name(41, 777)}); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := w.findArchive(ctx, media.File{MessageID: 41, DocID: 777}); !ok || got.MessageID != 9 {
		t.Fatalf("exact name: %+v %v", got, ok)
	}
	if _, ok, _ := w.findArchive(ctx, media.File{MessageID: 12, DocID: 888}); ok {
		t.Fatal("an archive that is not synced yet was found")
	}
	if _, ok, _ := w.findArchive(ctx, media.File{MessageID: 1, DocID: 4242}); ok {
		t.Fatal("an archive of another video was found")
	}
}
