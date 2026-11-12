package indexer

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/library"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
	"github.com/Aryan-mor/jellyfin-telegram/internal/sign"
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

func (s *fakeSource) Download(context.Context, int, int64, int64) ([]byte, error) {
	return s.thumb, s.thumbErr
}

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

func TestMusicStaysOutOfTheFilmLibrary(t *testing.T) {
	r := newRig(t)
	r.src.msgs[1] = media.Message{ID: 1, Exists: true, File: &media.File{
		MessageID: 1, ChannelID: 5, DocID: 9, Size: 10,
		MIME: "audio/mpeg", FileName: "Song.mp3", Date: time.Unix(1700000000, 0),
	}}
	added, err := r.idx.Sync(context.Background(), FullWindow)
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 {
		t.Fatalf("added %d", added)
	}
	f, err := r.cat.Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(f.RelPath, "Music/") || r.exists(f.RelPath) {
		t.Fatalf("path %s exists %v", f.RelPath, r.exists(f.RelPath))
	}
	pending, err := r.cat.Pending(context.Background())
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending %+v %v", pending, err)
	}
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

func TestSyncLeavesOutAnExcludedCountry(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	env := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(env, []byte("AUTO_FILTER_EXCLUDE_COUNTRIES=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.idx.SetCountryFilter(func(_ context.Context, tmdb int, _ bool) ([]string, error) {
		if tmdb == 7 {
			return []string{"IN"}, nil
		}
		return []string{"US"}, nil
	}, env)
	r.src.video(1, "Heat.1995.mkv", "tmdb 603")
	r.src.video(2, "Biker.2026.mkv", "tmdb 7")
	if _, err := r.idx.Sync(ctx, FullWindow); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env, []byte("AUTO_FILTER_EXCLUDE_COUNTRIES=IN\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.src.video(3, "Mask.2026.mkv", "tmdb 8")
	added, err := r.idx.Sync(ctx, FullWindow)
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 {
		t.Fatalf("added %d", added)
	}
	all, err := r.cat.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int
	for _, f := range all {
		ids = append(ids, f.MessageID)
	}
	slices.Sort(ids)
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 3 {
		t.Fatalf("kept %v", ids)
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

func TestUnidentifiedMovieIsSkipped(t *testing.T) {
	r := newRig(t)
	r.src.thumb = []byte("jpeg-bytes")
	r.src.video(1, "Vikidrama Doctor.mp4", "not a film")
	r.src.msgs[1].File.HasThumb = true
	r.src.video(2, "Heat.1995.mkv", "")
	added, err := r.idx.Sync(context.Background(), FullWindow)
	if err != nil || added != 1 {
		t.Fatalf("added %d, %v", added, err)
	}
	if r.exists("Movies/Vikidrama Doctor") || r.exists("Movies/Vikidrama Doctor/movie.nfo") {
		t.Error("unidentified file entered the library")
	}
	if _, err := r.cat.Get(context.Background(), 1); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("catalog kept the unidentified file: %v", err)
	}
	if !r.exists("Movies/Heat (1995)/Heat (1995).strm") {
		t.Error("identified film was skipped with it")
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

func TestThumbnailFailureDoesNotKeepAnUnidentifiedVideo(t *testing.T) {
	r := newRig(t)
	r.src.thumbErr = errors.New("no thumb")
	r.src.video(1, "Family Trip.mp4", "")
	r.src.msgs[1].File.HasThumb = true
	added, err := r.idx.Sync(context.Background(), FullWindow)
	if err != nil || added != 0 {
		t.Fatalf("added %d, %v", added, err)
	}
	if r.exists("Movies/Family Trip") {
		t.Error("unidentified file entered the library")
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

func TestPersonalChannelKeepsVideoAndPhoto(t *testing.T) {
	r := newRig(t)
	r.idx.SetPersonal()
	r.src.thumb = []byte("jpeg-bytes")
	r.src.video(1, "Birthday.mp4", "home")
	r.src.msgs[2] = media.Message{ID: 2, Exists: true, File: &media.File{
		MessageID: 2, ChannelID: 5, DocID: 200, Size: 10, MIME: "image/jpeg", FileName: "Picnic.jpg", Date: time.Unix(2, 0),
	}}
	added, err := r.idx.Sync(context.Background(), FullWindow)
	if err != nil || added != 2 {
		t.Fatalf("added %d, %v", added, err)
	}
	if !r.exists("Home/Birthday tg1.strm") {
		t.Error("home video missing")
	}
	if got := r.read(t, "Home/Picnic tg2.jpg"); got != "jpeg-bytes" {
		t.Errorf("photo = %q", got)
	}
	if r.exists("Movies") && r.exists("Movies/Birthday") {
		t.Error("personal file entered Movies")
	}
}

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
	rel := "Movies/Family Trip/Family Trip.strm"
	if err := r.cat.Put(ctx, media.File{MessageID: 1, ChannelID: 5, DocID: 9, Size: 10, MIME: "video/mp4", FileName: "Family Trip.mp4", Caption: "caption text", RelPath: rel, Date: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	if err := r.idx.lib.WriteUnmatchedSidecars(rel, "Family Trip", "caption text", nil); err != nil {
		t.Fatal(err)
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

func TestIdentifyRemovesAnUnknownTitle(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	rel := "Movies/Vikidrama Doctor/Vikidrama Doctor.strm"
	if err := r.cat.Put(ctx, media.File{MessageID: 1, ChannelID: 5, DocID: 9, Size: 10, MIME: "video/mp4", FileName: "Vikidrama Doctor.mp4", RelPath: rel, Date: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.idx.lib.WriteStrm(rel, 1); err != nil {
		t.Fatal(err)
	}
	r.idx.SetIdentifier(fakeIdent{})
	if changed, err := r.idx.Identify(ctx); err != nil || len(changed) != 0 {
		t.Fatalf("changed %d, %v", len(changed), err)
	}
	if r.exists(rel) {
		t.Error("unidentified file stayed in the library")
	}
	if _, err := r.cat.Get(ctx, 1); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("catalog kept it: %v", err)
	}
}

// signedCaption is a finished library post as a bot writes it: status line, head, signed line, hashtag.
func signedCaption(t *testing.T, meta sign.Meta) string {
	t.Helper()
	key, err := sign.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if meta.Status == "" {
		meta.Status = sign.StatusReady
	}
	return sign.WithHeader(meta.Status, "", "", "Title\n"+key.Sign(meta)+"\n#tm_m_603")
}

func allowCaptionsWith(marker string) Signed {
	return Signed{Allow: func(_ context.Context, f media.File) (bool, error) {
		return strings.Contains(f.Caption, marker), nil
	}}
}

// With the shared library on, only a post whose line verifies is in the library. A raw file is the
// intake's to finish, and music has no line.
func TestSignedModeIndexesOnlyVerifiedPosts(t *testing.T) {
	r := newRig(t)
	r.idx.SetSigned(Signed{Allow: func(_ context.Context, f media.File) (bool, error) {
		return strings.HasSuffix(f.Caption, "ok"), nil
	}})
	good := signedCaption(t, sign.Meta{Kind: "m", TMDB: 603, DocID: 200, Size: 1000}) + "ok"
	bad := signedCaption(t, sign.Meta{Kind: "m", TMDB: 604, DocID: 300, Size: 1000})
	r.src.video(1, "Heat.1995.mkv", "")     // raw
	r.src.video(2, "Matrix.1999.mkv", good) // signed, accepted
	r.src.video(3, "Dune.2021.mkv", bad)    // signed, refused
	r.src.msgs[4] = media.Message{ID: 4, Exists: true, File: &media.File{
		MessageID: 4, ChannelID: 5, DocID: 9, Size: 10, MIME: "audio/mpeg", FileName: "Song.mp3", Date: time.Unix(1700000000, 0),
	}}
	added, err := r.idx.Sync(context.Background(), IncrementalWindow)
	if err != nil {
		t.Fatal(err)
	}
	if added != 2 {
		t.Fatalf("added %d, want the accepted post and the song", added)
	}
	ctx := context.Background()
	for id, want := range map[int]bool{1: false, 2: true, 3: false, 4: true} {
		_, err := r.cat.Get(ctx, id)
		if (err == nil) != want {
			t.Errorf("message %d indexed = %v, want %v", id, err == nil, want)
		}
	}
}

// With the shared library off, a channel is read as it always was.
func TestPlainModeIndexesRawFiles(t *testing.T) {
	r := newRig(t)
	r.src.video(1, "Heat.1995.mkv", "")
	added, err := r.idx.Sync(context.Background(), IncrementalWindow)
	if err != nil || added != 1 {
		t.Fatalf("added %d, %v", added, err)
	}
}

// A signed episode is placed by the season and episode in its line, which the readable head lacks,
// and the status line above the caption does not confuse the title.
func TestSignedEpisodeUsesItsLine(t *testing.T) {
	r := newRig(t)
	r.idx.SetSigned(allowCaptionsWith("#tm_m_"))
	meta := sign.Meta{Kind: "e", TMDB: 1396, Season: 2, Episode: 5, Quality: "1080p", DocID: 900, Size: 1000}
	key, err := sign.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	meta.Status = sign.StatusSubtitles
	r.src.video(1, "Breaking Bad.mkv", sign.WithHeader(meta.Status, "", "", "Breaking Bad (2008)\n"+key.Sign(meta)+"\n#tm_m_1396"))
	if _, err := r.idx.Sync(context.Background(), IncrementalWindow); err != nil {
		t.Fatal(err)
	}
	got, err := r.cat.Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.RelPath, "Shows/") || !strings.Contains(got.RelPath, "S02E05") || strings.Contains(got.RelPath, "Subtitles") {
		t.Fatalf("path = %q", got.RelPath)
	}
}

// A lookup that fails says nothing about the post, so Reconcile must not remove the film.
func TestSignedReconcileKeepsAPostItCannotCheck(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	failing := 0
	r.idx.SetSigned(Signed{Allow: func(_ context.Context, f media.File) (bool, error) {
		if f.MessageID == failing {
			return false, errors.New("flood wait")
		}
		return !strings.Contains(f.Caption, "forged"), nil
	}})
	for id, name := range map[int]string{1: "Heat.1995.mkv", 2: "Dune.2021.mkv", 3: "Alien.1979.mkv"} {
		r.src.video(id, name, signedCaption(t, sign.Meta{Kind: "m", TMDB: 600 + id, DocID: int64(id) * 11, Size: 1000}))
	}
	if _, err := r.idx.Sync(ctx, IncrementalWindow); err != nil {
		t.Fatal(err)
	}
	failing = 2
	r.src.msgs[3].File.Caption += " forged" // no longer trusted
	removed, err := r.idx.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed %d, want only the forged post", removed)
	}
	if _, err := r.cat.Get(ctx, 2); err != nil {
		t.Fatalf("a post that could not be checked was removed: %v", err)
	}
	if _, err := r.cat.Get(ctx, 3); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatal("a forged post was kept")
	}
}

func TestSignedSyncStopsOnACheckErrorAndRetries(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	down := true
	r.idx.SetSigned(Signed{Allow: func(context.Context, media.File) (bool, error) {
		if down {
			return false, errors.New("flood wait")
		}
		return true, nil
	}})
	r.src.video(1, "Heat.1995.mkv", signedCaption(t, sign.Meta{Kind: "m", TMDB: 949, DocID: 100, Size: 1000}))
	if _, err := r.idx.Sync(ctx, IncrementalWindow); err == nil {
		t.Fatal("a check error was swallowed")
	}
	down = false
	added, err := r.idx.Sync(ctx, IncrementalWindow)
	if err != nil || added != 1 {
		t.Fatalf("added %d, %v", added, err)
	}
}

// The catalog learns that a post became ready, which is what lets a mirror copy it.
func TestRefreshStatusFollowsAnEditedCaption(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.idx.SetSigned(allowCaptionsWith("#tm_m_603"))
	waiting := signedCaption(t, sign.Meta{Kind: "m", TMDB: 603, DocID: 100, Size: 1000, Status: sign.StatusSubtitles})
	r.src.video(1, "Matrix.1999.mkv", waiting)
	if _, err := r.idx.Sync(ctx, IncrementalWindow); err != nil {
		t.Fatal(err)
	}
	row, err := r.cat.Get(ctx, 1)
	if err != nil || sign.IsReady(row.Caption) {
		t.Fatalf("the post should be waiting for its subtitles: %v", err)
	}
	// A bot finishes the post.
	done := signedCaption(t, sign.Meta{Kind: "m", TMDB: 603, DocID: 100, Size: 1000, Status: sign.StatusReady})
	r.src.msgs[1].File.Caption = done
	if err := r.idx.RefreshStatus(ctx); err != nil {
		t.Fatal(err)
	}
	row, _ = r.cat.Get(ctx, 1)
	if !sign.IsReady(row.Caption) {
		t.Fatalf("the catalog still has %q", row.Caption)
	}
	// An edit that does not verify changes nothing.
	r.idx.SetSigned(allowCaptionsWith("never matches"))
	r.src.msgs[1].File.Caption = waiting
	row.Caption = waiting
	if err := r.cat.SetCaption(ctx, 1, waiting); err != nil {
		t.Fatal(err)
	}
	r.src.msgs[1].File.Caption = done
	if err := r.idx.RefreshStatus(ctx); err != nil {
		t.Fatal(err)
	}
	row, _ = r.cat.Get(ctx, 1)
	if sign.IsReady(row.Caption) {
		t.Fatal("a caption that does not verify marked the post ready")
	}
}
