package indexer

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/library"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
)

type fakeSource struct {
	msgs     map[int]media.Message
	thumb    []byte
	thumbErr error
	lookups  int
	deleted  []int
}

func (s *fakeSource) Messages(_ context.Context, ids []int) ([]media.Message, error) {
	s.lookups++
	out := make([]media.Message, 0, len(ids))
	for _, id := range ids {
		if m, ok := s.msgs[id]; ok {
			out = append(out, m)
		} else {
			out = append(out, media.Message{ID: id})
		}
	}
	return out, nil
}

func (s *fakeSource) Thumbnail(context.Context, int) ([]byte, error) { return s.thumb, s.thumbErr }

func (s *fakeSource) DeleteMessages(_ context.Context, ids []int) error {
	s.deleted = append(s.deleted, ids...)
	for _, id := range ids {
		delete(s.msgs, id)
	}
	return nil
}

func (s *fakeSource) video(id int, name, caption string) {
	s.msgs[id] = media.Message{ID: id, Exists: true, File: &media.File{
		MessageID: id, ChannelID: 5, DocID: int64(id) * 100, Size: 1000,
		FileName: name, Caption: caption, Date: time.Unix(1700000000, 0),
	}}
}

type fakeBlocks struct{ removed []int64 }

func (b *fakeBlocks) RemoveDoc(doc int64) error {
	b.removed = append(b.removed, doc)
	return nil
}

type rig struct {
	src    *fakeSource
	cat    *catalog.Catalog
	blocks *fakeBlocks
	idx    *Indexer
	root   string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	cat, err := catalog.Open(filepath.Join(dir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cat.Close() })
	root := filepath.Join(dir, "library")
	lib := library.New(root, "http://gw:8097", "tok", 5)
	if err := lib.Prepare(); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{msgs: map[int]media.Message{}}
	blocks := &fakeBlocks{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	idx := New(src, cat, lib, blocks, log)
	idx.pace = 0
	return &rig{src: src, cat: cat, blocks: blocks, root: root, idx: idx}
}

func (r *rig) read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (r *rig) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(r.root, filepath.FromSlash(rel)))
	return err == nil
}

func TestSyncWritesStrmFiles(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.src.video(1, "Heat.1995.1080p.mkv", "")
	r.src.video(2, "Lost.S01E02.mkv", "")
	r.src.msgs[3] = media.Message{ID: 3, Exists: true} // text message
	r.src.video(4, "Dune.2021.mkv", "tmdb 438631")

	added, err := r.idx.Sync(ctx, FullWindow)
	if err != nil {
		t.Fatal(err)
	}
	if added != 3 {
		t.Fatalf("added = %d", added)
	}
	if got := r.read(t, "Movies/Heat (1995)/Heat (1995) - 1080p.strm"); got != "http://gw:8097/s/tok/5/1\n" {
		t.Errorf("strm content = %q", got)
	}
	if !r.exists("Shows/Lost/Season 01/Lost S01E02.strm") {
		t.Error("episode missing")
	}
	if !r.exists("Movies/Dune (2021) [tmdbid-438631]/Dune (2021) [tmdbid-438631].strm") {
		t.Error("tmdb movie missing")
	}
	last, err := r.cat.LastMessageID(ctx)
	if err != nil || last != 4 {
		t.Errorf("last = %d, %v", last, err)
	}
}

func TestSyncSkipsDeletedRunsAndResumes(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.src.video(1, "One.2001.mkv", "")
	r.src.video(1500, "Far.2002.mkv", "")

	added, err := r.idx.Sync(ctx, FullWindow)
	if err != nil {
		t.Fatal(err)
	}
	if added != 2 {
		t.Fatalf("a long gap stopped the walk: added = %d", added)
	}

	before := r.src.lookups
	r.src.video(1501, "New.2003.mkv", "")
	added, err = r.idx.Sync(ctx, IncrementalWindow)
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 {
		t.Fatalf("added = %d", added)
	}
	if got := r.src.lookups - before; got > 2 {
		t.Errorf("incremental sync made %d lookups", got)
	}
	added, err = r.idx.Sync(ctx, IncrementalWindow)
	if err != nil || added != 0 {
		t.Fatalf("re-sync added %d, %v", added, err)
	}
}

func TestSameNameDifferentMessagesDoNotCollide(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.src.video(1, "Heat.1995.1080p.mkv", "")
	r.src.video(2, "Heat.1995.1080p.mkv", "")
	if _, err := r.idx.Sync(ctx, FullWindow); err != nil {
		t.Fatal(err)
	}
	if got := r.read(t, "Movies/Heat (1995)/Heat (1995) - 1080p.strm"); !strings.HasSuffix(got, "/1\n") {
		t.Errorf("first file = %q", got)
	}
	if got := r.read(t, "Movies/Heat (1995)/Heat (1995) - 1080p - tg2.strm"); !strings.HasSuffix(got, "/2\n") {
		t.Errorf("second file = %q", got)
	}
}

func TestUnmatchableMovieGetsLocalMetadata(t *testing.T) {
	r := newRig(t)
	r.src.thumb = []byte("jpeg-bytes")
	r.src.video(1, "Family Trip.mp4", "Summer <2020> & friends")
	m := r.src.msgs[1]
	m.File.HasThumb = true
	if _, err := r.idx.Sync(context.Background(), FullWindow); err != nil {
		t.Fatal(err)
	}
	nfo := r.read(t, "Movies/Family Trip/movie.nfo")
	for _, want := range []string{"<title>Family Trip</title>", "Summer &lt;2020&gt; &amp; friends", "<lockdata>true</lockdata>"} {
		if !strings.Contains(nfo, want) {
			t.Errorf("nfo misses %q:\n%s", want, nfo)
		}
	}
	if got := r.read(t, "Movies/Family Trip/poster.jpg"); got != "jpeg-bytes" {
		t.Errorf("poster = %q", got)
	}
}

func TestMatchableMovieHasNoLocalMetadata(t *testing.T) {
	r := newRig(t)
	r.src.thumb = []byte("jpeg-bytes")
	r.src.video(1, "Heat.1995.mkv", "a caption")
	r.src.msgs[1].File.HasThumb = true
	if _, err := r.idx.Sync(context.Background(), FullWindow); err != nil {
		t.Fatal(err)
	}
	if r.exists("Movies/Heat (1995)/movie.nfo") || r.exists("Movies/Heat (1995)/poster.jpg") {
		t.Error("local metadata would override online metadata")
	}
}

func TestThumbnailFailureKeepsTheVideo(t *testing.T) {
	r := newRig(t)
	r.src.thumbErr = errors.New("no thumb")
	r.src.video(1, "Family Trip.mp4", "")
	r.src.msgs[1].File.HasThumb = true
	added, err := r.idx.Sync(context.Background(), FullWindow)
	if err != nil || added != 1 {
		t.Fatalf("added %d, %v", added, err)
	}
	if !r.exists("Movies/Family Trip/Family Trip.strm") || !r.exists("Movies/Family Trip/movie.nfo") {
		t.Error("files missing")
	}
	if r.exists("Movies/Family Trip/poster.jpg") {
		t.Error("empty poster written")
	}
}

func TestReconcileRemovesDeletedAndPrunes(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.src.video(1, "Heat.1995.mkv", "")
	r.src.video(2, "Lost.S01E01.mkv", "")
	r.src.video(3, "Lost.S01E02.mkv", "")
	if _, err := r.idx.Sync(ctx, FullWindow); err != nil {
		t.Fatal(err)
	}
	delete(r.src.msgs, 1)
	delete(r.src.msgs, 2)

	removed, err := r.idx.Reconcile(ctx)
	if err != nil || removed != 2 {
		t.Fatalf("removed %d, %v", removed, err)
	}
	if r.exists("Movies/Heat (1995)") {
		t.Error("empty movie folder kept")
	}
	if !r.exists("Movies") {
		t.Error("the library root folder was removed")
	}
	if r.exists("Shows/Lost/Season 01/Lost S01E01.strm") {
		t.Error("deleted episode kept")
	}
	if !r.exists("Shows/Lost/Season 01/Lost S01E02.strm") {
		t.Error("sibling episode removed")
	}
	if _, err := r.cat.Get(ctx, 1); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("catalog still has the file: %v", err)
	}
	if len(r.blocks.removed) != 2 {
		t.Errorf("cached blocks removed for %v", r.blocks.removed)
	}
}

func TestReconcileRefusesWhenEverythingVanishes(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.src.video(1, "Heat.1995.mkv", "")
	r.src.video(2, "Dune.2021.mkv", "")
	if _, err := r.idx.Sync(ctx, FullWindow); err != nil {
		t.Fatal(err)
	}
	r.src.msgs = map[int]media.Message{}

	removed, err := r.idx.Reconcile(ctx)
	if err != nil || removed != 0 {
		t.Fatalf("removed %d, %v", removed, err)
	}
	if !r.exists("Movies/Heat (1995)/Heat (1995).strm") {
		t.Error("library was wiped")
	}
}

func TestReconcileReplacedDocumentKeepsPathAndResetsProbe(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.src.video(1, "Heat.1995.mkv", "")
	r.src.video(2, "Dune.2021.mkv", "")
	if _, err := r.idx.Sync(ctx, FullWindow); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{1, 2} {
		if err := r.cat.MarkProbed(ctx, id, time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	r.src.video(1, "Renamed.1999.mkv", "new caption")
	r.src.msgs[1].File.DocID = 999
	r.src.msgs[2].File.Caption = "only the caption changed"
	if _, err := r.idx.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}

	replaced, err := r.cat.Get(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.RelPath != "Movies/Heat (1995)/Heat (1995).strm" {
		t.Errorf("path changed to %q, which would lose Jellyfin watch state", replaced.RelPath)
	}
	if replaced.DocID != 999 || !replaced.ProbedAt.IsZero() {
		t.Errorf("doc %d, probed %v", replaced.DocID, replaced.ProbedAt)
	}
	if len(r.blocks.removed) != 1 || r.blocks.removed[0] != 100 {
		t.Errorf("removed blocks %v", r.blocks.removed)
	}
	edited, err := r.cat.Get(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if edited.ProbedAt.IsZero() {
		t.Error("a caption edit should not force a new probe")
	}
}

func TestRewriteAppliesNewURLAndRestoresFiles(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.src.video(1, "Heat.1995.mkv", "")
	if _, err := r.idx.Sync(ctx, FullWindow); err != nil {
		t.Fatal(err)
	}
	rel := "Movies/Heat (1995)/Heat (1995).strm"
	if err := os.Remove(filepath.Join(r.root, filepath.FromSlash(rel))); err != nil {
		t.Fatal(err)
	}
	r.idx.lib = library.New(r.root, "http://new:1", "tok2", 5)
	if err := r.idx.Rewrite(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.read(t, rel); got != "http://new:1/s/tok2/5/1\n" {
		t.Errorf("strm = %q", got)
	}
}

func (s *fakeSource) attachment(id int, name string, replyTo int) {
	s.msgs[id] = media.Message{ID: id, Exists: true, Attachment: &media.Attachment{
		MessageID: id, DocID: int64(id) * 7, Size: 10, FileName: name, ReplyTo: replyTo,
	}}
}

func TestAttachmentsAreIndexedAndNeverCountedAsVideos(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.src.video(1, "Heat.1995.mkv", "")
	r.src.attachment(2, "Heat.fa.srt", 1)
	added, err := r.idx.Sync(ctx, FullWindow)
	if err != nil || added != 1 {
		t.Fatalf("added %d, %v", added, err)
	}
	all, err := r.cat.Attachments(ctx)
	if err != nil || len(all) != 1 || all[0].ReplyTo != 1 || all[0].DocID != 14 {
		t.Fatalf("attachments %+v, %v", all, err)
	}
}

func TestLostArchiveMakesTheVideoAskForANewOne(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.src.video(1, "Heat.1995.mkv", "")
	r.src.video(2, "Dune.2021.mkv", "")
	r.src.attachment(3, ArchivePrefix+"1-100.zip", 1)
	r.src.attachment(4, "my own subtitle.srt", 2)
	if _, err := r.idx.Sync(ctx, FullWindow); err != nil {
		t.Fatal(err)
	}
	if err := r.cat.SetExtractState(ctx, 1, media.ExtractDone); err != nil {
		t.Fatal(err)
	}
	delete(r.src.msgs, 3)
	if _, err := r.idx.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	f, err := r.cat.Get(ctx, 1)
	if err != nil || f.ExtractState != "" {
		t.Errorf("state %q, %v", f.ExtractState, err)
	}
	all, _ := r.cat.Attachments(ctx)
	if len(all) != 1 || all[0].MessageID != 4 {
		t.Errorf("attachments %+v", all)
	}
}

func TestDeletedVideoTakesOnlyItsOwnArchiveWithIt(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.src.video(1, "Heat.1995.mkv", "")
	r.src.video(2, "Dune.2021.mkv", "")
	r.src.attachment(3, ArchivePrefix+"1-100.zip", 1)
	r.src.attachment(4, "my own subtitle.srt", 1)
	r.src.attachment(5, ArchivePrefix+"2-200.zip", 2)
	if _, err := r.idx.Sync(ctx, FullWindow); err != nil {
		t.Fatal(err)
	}
	delete(r.src.msgs, 1)
	if _, err := r.idx.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.src.deleted) != 1 || r.src.deleted[0] != 3 {
		t.Errorf("deleted %v, want only the archive of the removed video", r.src.deleted)
	}
}

type fakeIdent map[string]int

func (f fakeIdent) Movie(_ context.Context, title string) (int, bool, error) {
	id, ok := f[title]
	return id, ok, nil
}

func TestNewTitleWithoutYearGetsTMDBID(t *testing.T) {
	r := newRig(t)
	r.idx.SetIdentifier(fakeIdent{"Family Trip": 77})
	r.src.thumb = []byte("jpeg-bytes")
	r.src.video(1, "Family Trip.mp4", "caption text")
	r.src.msgs[1].File.HasThumb = true
	if _, err := r.idx.Sync(context.Background(), FullWindow); err != nil {
		t.Fatal(err)
	}
	nfo := r.read(t, "Movies/Family Trip/movie.nfo")
	if !strings.Contains(nfo, "<tmdbid>77</tmdbid>") || strings.Contains(nfo, "lockdata") || strings.Contains(nfo, "<plot>") {
		t.Errorf("nfo = %s", nfo)
	}
	if r.exists("Movies/Family Trip/poster.jpg") {
		t.Error("local poster would hide the TMDB poster")
	}
}

func TestIdentifyFixesALockedTitle(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.src.video(1, "Family Trip.mp4", "caption text")
	if _, err := r.idx.Sync(ctx, FullWindow); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.read(t, "Movies/Family Trip/movie.nfo"), "lockdata") {
		t.Fatal("expected a locked local nfo first")
	}
	r.idx.SetIdentifier(fakeIdent{"Family Trip": 77})
	changed, err := r.idx.Identify(ctx)
	if err != nil || len(changed) != 1 {
		t.Fatalf("changed %d, %v", len(changed), err)
	}
	if nfo := r.read(t, "Movies/Family Trip/movie.nfo"); !strings.Contains(nfo, "<tmdbid>77</tmdbid>") || strings.Contains(nfo, "lockdata") {
		t.Errorf("nfo = %s", nfo)
	}
	if changed, _ := r.idx.Identify(ctx); len(changed) != 0 {
		t.Errorf("second pass changed %d files", len(changed))
	}
}

func TestIdentifyLeavesAnUnknownTitleAlone(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.src.video(1, "Family Trip.mp4", "caption text")
	r.idx.SetIdentifier(fakeIdent{})
	if _, err := r.idx.Sync(ctx, FullWindow); err != nil {
		t.Fatal(err)
	}
	if changed, err := r.idx.Identify(ctx); err != nil || len(changed) != 0 {
		t.Fatalf("changed %d, %v", len(changed), err)
	}
	if !strings.Contains(r.read(t, "Movies/Family Trip/movie.nfo"), "lockdata") {
		t.Error("local metadata should stay")
	}
}
