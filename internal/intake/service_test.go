package intake

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/fileid"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
	"github.com/Aryan-mor/jellyfin-telegram/internal/sign"
)

var epoch = time.Unix(1_700_000_000, 0)

// world is one channel that every installation reads and writes.
type world struct {
	mu    sync.Mutex
	next  int
	msgs  map[int]media.Message
	clock time.Time
	// failRepost makes Repost fail for these source ids.
	failRepost map[int]bool
	// author is the bot that posted a message. A post a person made has none, and any bot may edit it.
	author map[int]int64
	edits  int
}

func newWorld() *world {
	return &world{next: 50, msgs: map[int]media.Message{}, clock: epoch, author: map[int]int64{}}
}

func (w *world) put(m media.Message) {
	w.mu.Lock()
	w.msgs[m.ID] = m
	w.mu.Unlock()
}

// advance moves the clock of the channel and of every installation that shares it.
func (w *world) advance(d time.Duration) {
	w.mu.Lock()
	w.clock = w.clock.Add(d)
	w.mu.Unlock()
}

func (w *world) now() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.clock
}

type client struct {
	w   *world
	bot int64
}

func (c *client) ReadyBot() (int64, error) { return c.bot, nil }

func (c *client) Messages(_ context.Context, ids []int) ([]media.Message, error) {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	out := make([]media.Message, 0, len(ids))
	for _, id := range ids {
		if m, ok := c.w.msgs[id]; ok {
			// A copy, so a caller cannot change the channel by accident.
			if m.File != nil {
				f := *m.File
				m.File = &f
			}
			out = append(out, m)
			continue
		}
		out = append(out, media.Message{ID: id})
	}
	return out, nil
}

func (c *client) Repost(_ context.Context, botID int64, sourceID int, caption string) (int, error) {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	src, ok := c.w.msgs[sourceID]
	if !ok || src.File == nil || c.w.failRepost[sourceID] {
		return 0, errMissing
	}
	c.w.next++
	id := c.w.next
	file := *src.File
	file.MessageID = id
	file.Caption = caption
	file.Date = c.w.clock
	file.Edited = time.Time{}
	c.w.msgs[id] = media.Message{ID: id, Exists: true, Text: caption, Date: c.w.clock, File: &file}
	c.w.author[id] = botID
	return id, nil
}

func (c *client) EditCaption(_ context.Context, botID int64, messageID int, caption string) error {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	m, ok := c.w.msgs[messageID]
	if !ok {
		return errMissing
	}
	if by, mine := c.w.author[messageID]; mine && by != botID {
		return errString("MESSAGE_AUTHOR_REQUIRED")
	}
	m.Text = caption
	if m.File != nil {
		m.File.Caption = caption
		m.File.Edited = c.w.clock
	}
	m.Edited = c.w.clock
	c.w.msgs[messageID] = m
	c.w.edits++
	return nil
}

func (c *client) DeleteMessages(_ context.Context, ids []int) error {
	c.w.mu.Lock()
	for _, id := range ids {
		delete(c.w.msgs, id)
	}
	c.w.mu.Unlock()
	return nil
}

func (c *client) About(_ context.Context, _, _ int64) (string, error) { return "", nil }

var errMissing = errString("missing")

type errString string

func (e errString) Error() string { return string(e) }

func newInstall(t *testing.T, w *world, bot int64, key *sign.Key) *Service {
	t.Helper()
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cat.Close() })
	trust := &sign.Trust{State: cat, Own: key.Public()}
	tg := &client{w: w, bot: bot}
	svc := &Service{
		Cat: cat, TG: tg, Key: key, Trust: trust,
		Bots: []int64{bot}, Window: 100, Now: w.now,
		Identify: func(context.Context, string, bool) (int, string, bool, error) {
			return 603, "tt0133093", true, nil
		},
		Inspect: func(context.Context, int) (Probe, error) {
			return Probe{Tracks: []sign.Track{{Lang: "en", Codec: "srt", Index: 3, HasIndex: true}}, Seconds: 8160}, nil
		},
		Fingerprint: func(_ context.Context, f media.File) (fileid.Sample, error) { return sampleOf(f), nil },
	}
	svc.Check = &sign.Checker{
		Key: key, Trust: trust, Now: w.now, Rand: func() float64 { return 0.99 },
		// The quick check of the tests: the content has to give the hash that the caption names.
		Verify: func(_ context.Context, f media.File, m sign.Meta) error {
			if sampleOf(f).Hash() != m.Hash {
				return sign.ErrMismatch
			}
			return nil
		},
	}
	return svc
}

// sampleOf stands in for the first and last blocks of a file: the same document gives the same bytes.
func sampleOf(f media.File) fileid.Sample {
	doc := strconv.FormatInt(f.DocID, 10)
	return fileid.Sample{Size: f.Size, Head: []byte("head-" + doc), Tail: []byte("tail-" + doc)}
}

// film is a raw file somebody posted, with a note of their own in the caption.
func film(id int, doc int64, name, note string) media.Message {
	return media.Message{
		ID: id, Exists: true, Text: note, Date: epoch,
		File: &media.File{
			MessageID: id, DocID: doc, Size: 1000, Date: epoch,
			FileName: name, MIME: "video/x-matroska", Caption: note,
		},
	}
}

func caption(w *world, id int) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.msgs[id].File.Caption
}

// posts are the signed library posts in the channel.
func posts(w *world) []media.Message {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []media.Message
	for _, m := range w.msgs {
		if m.File == nil {
			continue
		}
		if _, ok := sign.ParseCaption(m.File.Caption); ok {
			out = append(out, m)
		}
	}
	return out
}

func status(t *testing.T, c string) string {
	t.Helper()
	h, _ := sign.SplitHeader(c)
	return h.Status
}

func passes(t *testing.T, n int, svcs ...*Service) {
	t.Helper()
	for i := 0; i < n; i++ {
		for _, s := range svcs {
			if err := s.Pass(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func newKey(t *testing.T) *sign.Key {
	t.Helper()
	key, err := sign.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestTwoInstallationsClaimOnceAndWriteTheStatusInTheCaption(t *testing.T) {
	w := newWorld()
	w.put(film(10, 1000, "The Matrix (1999).mkv", "my own note"))
	aKey, bKey := newKey(t), newKey(t)
	a := newInstall(t, w, 11, aKey)
	b := newInstall(t, w, 22, bKey)

	// The first pass only writes a claim. The second one finds it again, and only its owner works.
	passes(t, 1, a, b)
	if got := status(t, caption(w, 10)); got != sign.StatusReview {
		t.Fatalf("after the first pass the raw file says %q, want review", got)
	}
	passes(t, 4, a, b)

	all := posts(w)
	if len(all) != 1 {
		t.Fatalf("signed posts = %d, want 1", len(all))
	}
	post := all[0].File.Caption
	meta, ok := sign.ParseCaption(post)
	if !ok || meta.TMDB != 603 || meta.DocID != 1000 || meta.IMDb != "tt0133093" || meta.Status != sign.StatusReady {
		t.Fatalf("signed line %+v %v", meta, ok)
	}
	// The line names the content, the running time and where the subtitle track is.
	if want := sampleOf(media.File{DocID: 1000, Size: 1000}).Hash(); meta.Hash != want || meta.Duration != 8160 {
		t.Fatalf("hash %q (want %q), duration %d", meta.Hash, want, meta.Duration)
	}
	if meta.UniqueID != fileid.UniqueID(1000) {
		t.Fatalf("file_unique_id = %q", meta.UniqueID)
	}
	if len(meta.Soft) != 1 || !meta.Soft[0].HasIndex || meta.Soft[0].Index != 3 {
		t.Fatalf("tracks %+v", meta.Soft)
	}
	if !strings.HasPrefix(post, sign.StatusLine(sign.StatusReady, "")+"\n") {
		t.Fatalf("the first line is not the status: %q", post)
	}
	if !strings.HasSuffix(post, "\n#tm_m_603") {
		t.Fatalf("the caption does not end with the film hashtag: %q", post)
	}
	if len(post) > sign.CaptionLimit {
		t.Fatalf("the caption is %d characters", len(post))
	}
	if !sign.IsReady(post) {
		t.Fatal("a finished post is not ready")
	}
	// The raw file says it is indexed, and what the person wrote is still there.
	raw := caption(w, 10)
	if status(t, raw) != sign.StatusIndexed || sign.StripHeader(raw) != "my own note" {
		t.Fatalf("raw file caption = %q", raw)
	}
	if len(w.msgs) != 2 {
		t.Fatalf("the channel holds %d posts, want the raw file and its signed post", len(w.msgs))
	}
	// Later passes change nothing.
	edits := w.edits
	passes(t, 3, a, b)
	if len(posts(w)) != 1 || w.edits != edits {
		t.Fatal("something was done again")
	}
}

func TestALapsedClaimIsTakenOver(t *testing.T) {
	w := newWorld()
	w.put(film(10, 1000, "The Matrix (1999).mkv", ""))
	a := newInstall(t, w, 11, newKey(t))
	b := newInstall(t, w, 22, newKey(t))
	passes(t, 1, a) // a claims
	if h, _ := sign.SplitHeader(caption(w, 10)); h.Claim == nil || h.Claim.KeyID() != a.Key.KeyID() {
		t.Fatal("a did not claim")
	}
	// a goes quiet. Inside the claim time b keeps off.
	w.advance(time.Hour)
	passes(t, 3, b)
	if h, _ := sign.SplitHeader(caption(w, 10)); h.Claim == nil || h.Claim.KeyID() != a.Key.KeyID() || len(posts(w)) != 0 {
		t.Fatal("b worked on a file that a had claimed")
	}
	// After the claim has lapsed b takes the file.
	w.advance(2 * time.Hour)
	passes(t, 3, b)
	all := posts(w)
	if len(all) != 1 {
		t.Fatalf("signed posts = %d", len(all))
	}
	if meta, _ := sign.ParseCaption(all[0].File.Caption); meta.KeyID != b.Key.KeyID() {
		t.Fatal("the post is not signed by the installation that took over")
	}
}

func TestUnidentifiedIsRejectedWithAReasonInTheCaption(t *testing.T) {
	w := newWorld()
	w.put(film(10, 1000, "clip.mkv", "from my phone"))
	svc := newInstall(t, w, 11, newKey(t))
	svc.Identify = func(context.Context, string, bool) (int, string, bool, error) { return 0, "", false, nil }
	passes(t, 4, svc)
	raw := caption(w, 10)
	h, body := sign.SplitHeader(raw)
	if h.Status != sign.StatusRejected || h.Why != "unidentified" || h.Claim != nil || body != "from my phone" {
		t.Fatalf("raw file caption = %q", raw)
	}
	if len(posts(w)) != 0 || len(w.msgs) != 1 {
		t.Fatal("something was posted")
	}
	state, _, err := svc.Cat.InboxState(context.Background(), 10)
	if err != nil || state != "rejected" {
		t.Fatalf("state = %q %v", state, err)
	}
	// Nobody takes a rejected file up again, not even another installation.
	other := newInstall(t, w, 22, newKey(t))
	edits := w.edits
	passes(t, 3, other)
	if w.edits != edits {
		t.Fatal("a rejected file was edited again")
	}
}

func TestSameDocumentIsNotPostedTwice(t *testing.T) {
	w := newWorld()
	w.put(film(10, 1000, "The Matrix (1999).mkv", ""))
	svc := newInstall(t, w, 11, newKey(t))
	meta := sign.Meta{Kind: "m", TMDB: 603, IMDb: "tt0133093", DocID: 1000, Size: 1000, Hard: "unknown", Subs: "no", Status: sign.StatusReady}
	ready := sign.WithHeader(sign.StatusReady, "", "", "The Matrix (1999)\n"+svc.Key.Sign(meta)+"\n#tm_m_603")
	post := film(20, 1000, "The Matrix (1999).mkv", ready)
	w.put(post)
	passes(t, 3, svc)
	if len(posts(w)) != 1 {
		t.Fatalf("signed posts = %d", len(posts(w)))
	}
	if got := status(t, caption(w, 10)); got != sign.StatusIndexed {
		t.Fatalf("the raw file says %q", got)
	}
}

// A document the catalog already holds needs no work, wherever it came from.
func TestDocumentKnownToTheCatalogIsNotPostedAgain(t *testing.T) {
	w := newWorld()
	w.put(film(10, 1000, "The Matrix (1999).mkv", ""))
	svc := newInstall(t, w, 11, newKey(t))
	ctx := context.Background()
	if err := svc.Cat.Put(ctx, media.File{MessageID: 600, DocID: 1000, Size: 1000, FileName: "x.mkv", RelPath: "Movies/x/x.strm"}); err != nil {
		t.Fatal(err)
	}
	passes(t, 3, svc)
	if len(posts(w)) != 0 {
		t.Fatal("a known document was posted again")
	}
	if got := status(t, caption(w, 10)); got != sign.StatusIndexed {
		t.Fatalf("the raw file says %q", got)
	}
}

func TestFailingFileDoesNotBlockTheQueue(t *testing.T) {
	w := newWorld()
	w.put(film(10, 1000, "The Matrix (1999).mkv", ""))
	w.put(film(11, 1001, "Heat (1995).mkv", ""))
	w.failRepost = map[int]bool{10: true}
	svc := newInstall(t, w, 11, newKey(t))
	passes(t, maxTries+4, svc)
	all := posts(w)
	if len(all) != 1 || all[0].File.DocID != 1001 {
		t.Fatalf("signed posts = %d", len(all))
	}
	h, _ := sign.SplitHeader(caption(w, 10))
	if h.Status != sign.StatusRejected {
		t.Fatalf("the failing file says %q, want rejected", h.Status)
	}
	state, _, err := svc.Cat.InboxState(context.Background(), 10)
	if err != nil || state != "rejected" {
		t.Fatalf("state = %q %v", state, err)
	}
}

func TestNoIdentifierLeavesTheFileUntouched(t *testing.T) {
	w := newWorld()
	w.put(film(10, 1000, "clip.mkv", ""))
	svc := newInstall(t, w, 11, newKey(t))
	svc.Identify = nil
	passes(t, 3, svc)
	if w.edits != 0 || len(posts(w)) != 0 {
		t.Fatal("the file was touched")
	}
	state, _, err := svc.Cat.InboxState(context.Background(), 10)
	if err != nil || state != "open" {
		t.Fatalf("state = %q %v", state, err)
	}
}

func TestIdentifierErrorNeverRejects(t *testing.T) {
	w := newWorld()
	w.put(film(10, 1000, "clip.mkv", ""))
	svc := newInstall(t, w, 11, newKey(t))
	svc.Identify = func(context.Context, string, bool) (int, string, bool, error) {
		return 0, "", false, errMissing
	}
	passes(t, maxTries+3, svc)
	state, _, err := svc.Cat.InboxState(context.Background(), 10)
	if err != nil || state != "open" {
		t.Fatalf("state = %q %v", state, err)
	}
	if h, _ := sign.SplitHeader(caption(w, 10)); h.Status == sign.StatusRejected {
		t.Fatal("a failed lookup rejected the file")
	}
}

// One pass takes up a few files, so a channel full of new files is not edited all at once.
func TestOnePassTakesUpOnlyAFewFiles(t *testing.T) {
	w := newWorld()
	for id := 1; id <= 8; id++ {
		w.put(film(id, int64(id)*100, "Film (1990).mkv", ""))
	}
	svc := newInstall(t, w, 11, newKey(t))
	passes(t, 1, svc)
	claimed := 0
	for id := 1; id <= 8; id++ {
		if status(t, caption(w, id)) == sign.StatusReview {
			claimed++
		}
	}
	if claimed != maxPerPass {
		t.Fatalf("%d files were taken up in one pass, want %d", claimed, maxPerPass)
	}
	passes(t, 6, svc)
	if len(posts(w)) != 8 {
		t.Fatalf("signed posts = %d, want 8", len(posts(w)))
	}
}

// With subtitles on, a post is not ready until they are done, and only one installation does them.
func TestSubtitlesStatusAndClaim(t *testing.T) {
	w := newWorld()
	w.put(film(10, 1000, "The Matrix (1999).mkv", ""))
	a := newInstall(t, w, 11, newKey(t))
	b := newInstall(t, w, 22, newKey(t))
	a.Subtitles = true
	ctx := context.Background()
	passes(t, 4, a)
	all := posts(w)
	if len(all) != 1 {
		t.Fatalf("signed posts = %d", len(all))
	}
	id := all[0].ID
	post := all[0].File.Caption
	if status(t, post) != sign.StatusSubtitles || sign.IsReady(post) {
		t.Fatalf("a post with subtitles to do says %q", status(t, post))
	}
	if meta, _ := sign.ParseCaption(post); meta.Status != sign.StatusSubtitles {
		t.Fatalf("signed status = %q", meta.Status)
	}
	file := *all[0].File

	won, err := a.Claim(ctx, file)
	if err != nil || won {
		t.Fatalf("the first call should only write the claim: %v %v", won, err)
	}
	if h, _ := sign.SplitHeader(caption(w, id)); h.Claim == nil || h.Claim.Op != "subs" || h.Status != sign.StatusSubtitles {
		t.Fatalf("no subtitle claim under the status line: %q", caption(w, id))
	}
	if won, err := b.Claim(ctx, file); err != nil || won {
		t.Fatalf("another installation took a claimed file: %v %v", won, err)
	}
	if won, err := a.Claim(ctx, file); err != nil || !won {
		t.Fatalf("the owner lost its claim: %v %v", won, err)
	}

	if err := a.Resign(ctx, file, "ARCHIVEHASH12"); err != nil {
		t.Fatal(err)
	}
	done := caption(w, id)
	if meta, _ := sign.ParseCaption(done); meta.ArchiveHash != "ARCHIVEHASH12" || meta.Subs != "yes" {
		t.Fatalf("the archive is not named in the line: %+v", meta)
	}
	h, _ := sign.SplitHeader(done)
	if h.Status != sign.StatusReady || h.Claim != nil || !sign.IsReady(done) {
		t.Fatalf("after the subtitles the post says %q", done)
	}
	if !strings.HasSuffix(done, "\n#tm_m_603") {
		t.Fatalf("the hashtag moved: %q", done)
	}
	if ok, err := a.Check.Allow(ctx, media.File{MessageID: id, DocID: 1000, Size: 1000, Caption: done}); err != nil || !ok {
		t.Fatalf("the new signature does not verify: %v %v", ok, err)
	}
	// A post that is ready needs nobody's claim.
	if won, err := b.Claim(ctx, file); err != nil || !won {
		t.Fatalf("claim on a ready post: %v %v", won, err)
	}
}

func TestEpisodeKeepsItsSeriesImdbAndItsStatus(t *testing.T) {
	w := newWorld()
	w.put(film(10, 1000, "Show S01E02 [tmdbid-77].mkv", ""))
	svc := newInstall(t, w, 11, newKey(t))
	ctx := context.Background()
	passes(t, 4, svc)
	all := posts(w)
	if len(all) != 1 {
		t.Fatalf("signed posts = %d", len(all))
	}
	before := all[0].File.Caption
	if err := svc.FillIMDb(ctx, *all[0].File, "tt9999999"); err != nil {
		t.Fatal(err)
	}
	after := caption(w, all[0].ID)
	if after != before {
		t.Fatalf("the caption of an episode changed:\n%q\n%q", before, after)
	}
	meta, ok := sign.ParseCaption(after)
	if !ok || meta.Kind != "e" || meta.IMDb == "tt9999999" {
		t.Fatalf("meta %+v %v", meta, ok)
	}
	if !strings.HasSuffix(after, "\n#tm_tv_77_1_2") {
		t.Fatalf("the caption does not end with the episode hashtag: %q", after)
	}
}

// A channel post does not say which bot sent it, so a re-sign has to find the right one.
func TestResignTriesEveryBot(t *testing.T) {
	w := newWorld()
	w.put(film(10, 1000, "The Matrix (1999).mkv", ""))
	svc := newInstall(t, w, 11, newKey(t))
	svc.Subtitles = true
	passes(t, 4, svc)
	all := posts(w)
	if len(all) != 1 {
		t.Fatalf("signed posts = %d", len(all))
	}
	svc.Bots = []int64{22, 33, 11} // the bot that posted is the last one tried
	if err := svc.Resign(context.Background(), *all[0].File, ""); err != nil {
		t.Fatal(err)
	}
	if !sign.IsReady(caption(w, all[0].ID)) {
		t.Fatalf("the post is not ready: %q", caption(w, all[0].ID))
	}
}

// A post by another installation is compared with its file before it counts, and the key earns a
// record by being right.
func TestAForeignPostIsCheckedAgainstItsFile(t *testing.T) {
	w := newWorld()
	w.put(film(10, 1000, "The Matrix (1999).mkv", ""))
	a := newInstall(t, w, 11, newKey(t))
	b := newInstall(t, w, 22, newKey(t))
	passes(t, 4, a)
	all := posts(w)
	if len(all) != 1 {
		t.Fatalf("signed posts = %d", len(all))
	}
	ctx := context.Background()
	if ok, err := b.Check.Allow(ctx, *all[0].File); err != nil || !ok {
		t.Fatalf("a right post was refused: %v %v", ok, err)
	}
	rows, err := b.Trust.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range rows {
		if r.Fingerprint == a.Key.KeyID() && r.Verified == 1 && r.Wrong == 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("the key has no record: %+v", rows)
	}

	// The same caption on another file is caught: the signature holds, the content does not.
	other := *all[0].File
	other.MessageID = 99
	other.DocID = 5555
	meta, _ := sign.ParseCaption(other.Caption)
	meta.DocID, meta.UniqueID = 5555, ""
	forger := newKey(t)
	other.Caption = sign.WithHeader(sign.StatusReady, "", "", "Title\n"+forger.Sign(meta)+"\n#tm_m_603")
	if ok, err := b.Check.Allow(ctx, other); err != nil || ok {
		t.Fatalf("a post for the wrong content was accepted: %v %v", ok, err)
	}
	rows, _ = b.Trust.List(ctx)
	for _, r := range rows {
		if r.Fingerprint == forger.KeyID() && r.Wrong != 1 {
			t.Fatalf("the wrong post was not recorded: %+v", r)
		}
	}
	// An error from the check is not a verdict.
	b.Check.Verify = func(context.Context, media.File, sign.Meta) error { return errors.New("flood wait") }
	third := newKey(t)
	meta.Hash = sampleOf(other).Hash()
	other.Caption = sign.WithHeader(sign.StatusReady, "", "", "Title\n"+third.Sign(meta)+"\n#tm_m_603")
	if ok, err := b.Check.Allow(ctx, other); ok || err == nil {
		t.Fatalf("a failed check gave %v %v", ok, err)
	}
}

// With no way to read the file, the installation keeps to the keys it knows.
func TestWithoutAFingerprintTheLineHasNoHashes(t *testing.T) {
	w := newWorld()
	w.put(film(10, 1000, "The Matrix (1999).mkv", ""))
	svc := newInstall(t, w, 11, newKey(t))
	svc.Fingerprint = nil
	svc.Inspect = nil
	passes(t, 4, svc)
	all := posts(w)
	if len(all) != 1 {
		t.Fatalf("signed posts = %d", len(all))
	}
	meta, _ := sign.ParseCaption(all[0].File.Caption)
	if meta.Hash != "" || meta.OSDb != "" || meta.Duration != 0 || len(meta.Soft) != 0 {
		t.Fatalf("fields that could not be read were invented: %+v", meta)
	}
	if !meta.Authentic() || meta.UniqueID == "" {
		t.Fatal("the line is not signed")
	}
}

// The archive is posted with a caption that names its video and its own bytes, signed.
func TestArchiveCaptionNamesTheVideoAndTheArchive(t *testing.T) {
	w := newWorld()
	w.put(film(10, 1000, "The Matrix (1999).mkv", ""))
	svc := newInstall(t, w, 11, newKey(t))
	svc.Subtitles = true
	passes(t, 4, svc)
	all := posts(w)
	if len(all) != 1 {
		t.Fatalf("signed posts = %d", len(all))
	}
	tracks := []sign.Track{{Lang: "fa", Codec: "srt", Index: 3, HasIndex: true}}
	caption := svc.ArchiveCaption(context.Background(), *all[0].File, sign.ArchiveHash([]byte("zip")), tracks)
	subs, ok := sign.ParseSubsCaption(caption)
	if !ok || !subs.Authentic() {
		t.Fatalf("no signed line in %q", caption)
	}
	videoMeta, _ := sign.ParseCaption(all[0].File.Caption)
	if subs.DocID != 1000 || subs.VideoHash != videoMeta.Hash || subs.VideoHash == "" || subs.ArchiveHash != sign.ArchiveHash([]byte("zip")) {
		t.Fatalf("archive line %+v", subs)
	}
	if len(subs.Tracks) != 1 || subs.Tracks[0].Index != 3 {
		t.Fatalf("tracks %+v", subs.Tracks)
	}
	if !strings.HasSuffix(caption, "\n#tm_m_603") || !strings.Contains(caption, "The Matrix") {
		t.Fatalf("title or hashtag missing: %q", caption)
	}
	if len(caption) > sign.CaptionLimit {
		t.Fatalf("the caption is %d characters", len(caption))
	}
}
