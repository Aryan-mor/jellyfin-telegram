package sign

import (
	"context"
	"crypto/ed25519"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
)

func sample() Meta {
	return Meta{
		Kind: "m", TMDB: 603, IMDb: "tt0133093", Quality: "1080p",
		Soft: []Track{{Lang: "fa", Codec: "srt"}, {Lang: "en", Codec: "ass"}},
		Hard: "none", Subs: "no", DocID: 42, Size: 1000,
	}
}

func TestSignRoundTrip(t *testing.T) {
	dir := t.TempDir()
	key, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if key.KeyID() != again.KeyID() {
		t.Fatal("the key changed on the second load")
	}
	line := key.Sign(sample())
	if len(line) > 1024 {
		t.Fatalf("line is %d characters", len(line))
	}
	got, err := ParseLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if !Verify(key.Public(), got, got.Sig) {
		t.Fatal("round trip did not verify")
	}
	caption := "The Matrix (1999)\n" + line
	fromCaption, ok := ParseCaption(caption)
	if !ok || fromCaption.TMDB != 603 || fromCaption.DocID != 42 {
		t.Fatalf("caption parse: %+v %v", fromCaption, ok)
	}
}

func TestChangedFieldFails(t *testing.T) {
	key, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	signed := key.Sign(sample())
	got, err := ParseLine(signed)
	if err != nil {
		t.Fatal(err)
	}
	got.Quality = "720p"
	if Verify(key.Public(), got, got.Sig) {
		t.Fatal("a changed field verified")
	}
	got, _ = ParseLine(signed)
	got.DocID = 99
	if Verify(key.Public(), got, got.Sig) {
		t.Fatal("a swapped document id verified")
	}
	_, other, _ := ed25519.GenerateKey(nil)
	got, _ = ParseLine(signed)
	if Verify(other.Public().(ed25519.PublicKey), got, got.Sig) {
		t.Fatal("the wrong key verified")
	}
}

func TestAboutKeepsOtherText(t *testing.T) {
	key, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	line := AboutLine(key.Public())
	merged := MergeAbout("Hello from the bot\n"+line, AboutLine(key.Public()))
	if merged != "Hello from the bot\n"+line {
		t.Fatalf("merged = %q", merged)
	}
	pub, ok := PublicFromAbout(merged)
	if !ok || KeyID(pub) != key.KeyID() {
		t.Fatal("the about text did not yield the key")
	}
	if _, ok := PublicFromAbout("no key here"); ok {
		t.Fatal("plain text looked like a key")
	}
}

func TestClaimRoundTrip(t *testing.T) {
	key, err := LoadOrCreate(filepath.Join(t.TempDir(), "k"))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := ParseClaim(key.SignClaim("review", 15, 7200))
	if !ok || got.Target != 15 || got.Op != "review" || got.TTL != 7200 || got.KeyID() != key.KeyID() {
		t.Fatalf("parse %+v %v", got, ok)
	}
	if !got.Verify() {
		t.Fatal("claim did not verify")
	}
	edited := time.Unix(1_700_000_000, 0)
	if !got.Live("review", 15, edited, edited.Add(time.Hour)) {
		t.Fatal("a fresh claim is not live")
	}
	if got.Live("review", 15, edited, edited.Add(3*time.Hour)) {
		t.Fatal("a lapsed claim is live")
	}
	if got.Live("subs", 15, edited, edited.Add(time.Hour)) || got.Live("review", 16, edited, edited.Add(time.Hour)) {
		t.Fatal("a claim counted for other work")
	}
	got.TTL = 100000
	if got.Verify() {
		t.Fatal("a changed ttl verified")
	}
	other, err := LoadOrCreate(filepath.Join(t.TempDir(), "o"))
	if err != nil {
		t.Fatal(err)
	}
	forged, _ := ParseClaim(key.SignClaim("review", 15, 7200))
	forged.PubKey = other.Public()
	if forged.Verify() {
		t.Fatal("a claim verified under another key")
	}
}

func TestStatusLinesAndHeader(t *testing.T) {
	for _, status := range []string{StatusReview, StatusIndexed, StatusRejected, StatusSubtitles, StatusReady} {
		got, _, ok := ParseStatus(StatusLine(status, ""))
		if !ok || got != status {
			t.Errorf("%s -> %q %v", status, StatusLine(status, ""), ok)
		}
	}
	if st, why, ok := ParseStatus(StatusLine(StatusRejected, "unidentified")); !ok || st != StatusRejected || why != "unidentified" {
		t.Errorf("rejected line: %q %q %v", st, why, ok)
	}
	if _, _, ok := ParseStatus("The Matrix (1999)"); ok {
		t.Error("a title looked like a status")
	}
	key, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	claim := key.SignClaim("review", 10, 600)
	caption := WithHeader(StatusReview, "", claim, "my own note\nsecond line")
	h, body := SplitHeader(caption)
	if h.Status != StatusReview || h.Claim == nil || !h.Claim.Verify() || body != "my own note\nsecond line" {
		t.Fatalf("header %+v body %q", h, body)
	}
	if StripHeader(caption) != body {
		t.Fatal("StripHeader and SplitHeader disagree")
	}
	if h, body := SplitHeader("just a note"); h.Status != "" || body != "just a note" {
		t.Fatalf("a caption with no header changed: %+v %q", h, body)
	}
	// A status line with no claim under it.
	if h, body := SplitHeader(WithHeader(StatusReady, "", "", "Title")); h.Status != StatusReady || h.Claim != nil || body != "Title" {
		t.Fatalf("header %+v body %q", h, body)
	}
}

func TestSignedStatusCannotBeChanged(t *testing.T) {
	key, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := sample()
	m.Status = StatusSubtitles
	got, err := ParseLine(key.Sign(m))
	if err != nil || got.Status != StatusSubtitles || !Verify(key.Public(), got, got.Sig) {
		t.Fatalf("status %q %v", got.Status, err)
	}
	got.Status = StatusReady
	if Verify(key.Public(), got, got.Sig) {
		t.Fatal("a post could be marked ready without the key")
	}
}

func TestEmptyIMDbAndQualityStayValid(t *testing.T) {
	key, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := sample()
	m.IMDb = ""
	m.Quality = ""
	line := key.Sign(m)
	if strings.Contains(line, ";i=") || strings.Contains(line, ";q=") {
		t.Fatal(line)
	}
	got, err := ParseLine(line)
	if err != nil || !Verify(key.Public(), got, got.Sig) {
		t.Fatal(err)
	}
}

func TestCaptionStaysWithinLimit(t *testing.T) {
	key, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := sample()
	for i := 0; i < 40; i++ {
		m.Soft = append(m.Soft, Track{Lang: "en", Codec: "ass"})
	}
	head := strings.Repeat("x", 600)
	line := key.Post(head, m)
	if len(head)+1+len(line) > CaptionLimit {
		t.Fatalf("caption is %d characters", len(head)+1+len(line))
	}
	body, _, _ := strings.Cut(line, ";s=")
	if !strings.Contains(body, "+") {
		t.Fatal("a shortened track list did not end with +")
	}
	got, err := ParseLine(line)
	if err != nil || !got.Cut || !Verify(key.Public(), got, got.Sig) {
		t.Fatalf("parse %v cut %v", err, got.Cut)
	}
}

func TestReplaceLine(t *testing.T) {
	key, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := sample()
	head := "The Matrix (1999)"
	caption := head + "\n" + key.Sign(m) + "\n#tm_m_603"
	m.Subs = "yes"
	next := ReplaceLine(caption, key.Sign(m))
	got, ok := ParseCaption(next)
	if !ok || got.Subs != "yes" || !Verify(key.Public(), got, got.Sig) {
		t.Fatal("replace did not keep a valid line")
	}
	if !strings.HasPrefix(next, head+"\n"+LinePrefix) {
		t.Fatalf("the head moved: %q", next)
	}
	if !strings.HasSuffix(next, "\n#tm_m_603") {
		t.Fatalf("the hashtag is not last: %q", next)
	}
	if strings.Count(next, LinePrefix) != 1 {
		t.Fatalf("the line was duplicated: %q", next)
	}
}

func TestAboutTooLongKeepsOnlyTheKey(t *testing.T) {
	key, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	line := AboutLine(key.Public())
	long := strings.Repeat("a", 100)
	if got := MergeAbout(long, line); got != line {
		t.Fatalf("merged = %q", got)
	}
	if got := MergeAbout("short", line); got != "short\n"+line {
		t.Fatalf("merged = %q", got)
	}
	if len(line) > AboutLimit {
		t.Fatalf("the key line alone is %d characters", len(line))
	}
}

func TestV2FieldsRoundTripAndAreSigned(t *testing.T) {
	key, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := sample()
	m.DocID = 6014791683013616491
	m.Hash, m.OSDb, m.Duration, m.ArchiveHash = "9f2cAAAAAAAAAAAAAAAAAA", "8e245d9679d31e12", 8160, "abcDEF123456"
	m.Soft = []Track{{Lang: "fa", Codec: "srt", Index: 3, HasIndex: true}, {Lang: "en", Codec: "ass"}}
	line := key.Sign(m)
	got, err := ParseLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if got.UniqueID != "AgADayMAAivVeFM" {
		t.Fatalf("file_unique_id = %q", got.UniqueID)
	}
	if got.Hash != m.Hash || got.OSDb != m.OSDb || got.Duration != 8160 || got.ArchiveHash != m.ArchiveHash {
		t.Fatalf("fields lost: %+v", got)
	}
	if len(got.Soft) != 2 || !got.Soft[0].HasIndex || got.Soft[0].Index != 3 || got.Soft[1].HasIndex {
		t.Fatalf("stream numbers: %+v", got.Soft)
	}
	if !got.Authentic() || got.KeyID != key.KeyID() {
		t.Fatal("the line does not verify under the key it carries")
	}
	for name, change := range map[string]func(*Meta){
		"hash":     func(x *Meta) { x.Hash = "0000000000000000000000" },
		"duration": func(x *Meta) { x.Duration = 1 },
		"archive":  func(x *Meta) { x.ArchiveHash = "other" },
		"stream":   func(x *Meta) { x.Soft[0].Index = 4 },
	} {
		tampered, _ := ParseLine(line)
		change(&tampered)
		if tampered.Authentic() {
			t.Errorf("a changed %s still verified", name)
		}
	}
	// A line that names another key's signature does not verify.
	other, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	swapped, _ := ParseLine(line)
	swapped.PubKey = other.Public()
	if swapped.Authentic() {
		t.Fatal("a swapped key verified")
	}
}

func TestUniqueIDMustAgreeWithTheDocument(t *testing.T) {
	key, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	line := key.Sign(sample())
	forged := strings.Replace(line, ";fu=", ";fu=AAAA", 1)
	if _, err := ParseLine(forged); err == nil {
		t.Fatal("a file_unique_id of another document was accepted")
	}
}

type memState map[string]string

func (m memState) StateValue(_ context.Context, k string) (string, error) { return m[k], nil }
func (m memState) SetStateValue(_ context.Context, k, v string) error     { m[k] = v; return nil }

func checkerFor(t *testing.T, verify func(context.Context, media.File, Meta) error) (*Checker, *Key, memState) {
	t.Helper()
	own, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st := memState{}
	now := time.Unix(1_700_000_000, 0)
	return &Checker{
		Key: own, Trust: &Trust{State: st, Own: own.Public()}, Verify: verify,
		Now: func() time.Time { return now }, Rand: func() float64 { return 0.99 },
	}, own, st
}

func postBy(t *testing.T, key *Key, doc int64) media.File {
	t.Helper()
	m := sample()
	m.DocID = doc
	m.Hash = "h" + strings.Repeat("x", 21)
	return media.File{MessageID: 7, DocID: doc, Size: m.Size, Caption: "Title\n" + key.Sign(m)}
}

func TestOwnKeyNeedsNoCheck(t *testing.T) {
	c, own, _ := checkerFor(t, func(context.Context, media.File, Meta) error {
		t.Fatal("a post of the installation's own key was checked")
		return nil
	})
	if ok, err := c.Allow(context.Background(), postBy(t, own, 42)); err != nil || !ok {
		t.Fatalf("own post: %v %v", ok, err)
	}
}

func TestUnknownKeyIsCheckedAgainstTheFileOnce(t *testing.T) {
	calls := 0
	c, _, _ := checkerFor(t, func(context.Context, media.File, Meta) error { calls++; return nil })
	stranger, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := postBy(t, stranger, 42)
	for i := 0; i < 3; i++ {
		if ok, err := c.Allow(context.Background(), f); err != nil || !ok {
			t.Fatalf("allow: %v %v", ok, err)
		}
	}
	if calls != 1 {
		t.Fatalf("the file was checked %d times, want once", calls)
	}
}

func TestAWrongPostIsRefusedAndBlocksTheKey(t *testing.T) {
	c, _, _ := checkerFor(t, func(context.Context, media.File, Meta) error { return ErrMismatch })
	stranger, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for doc := int64(1); doc <= blockAfter; doc++ {
		if ok, err := c.Allow(ctx, postBy(t, stranger, doc)); err != nil || ok {
			t.Fatalf("a wrong post was accepted: %v %v", ok, err)
		}
	}
	// Now the key is blocked: the check is not even made.
	c.Verify = func(context.Context, media.File, Meta) error {
		t.Fatal("a blocked key was checked")
		return nil
	}
	if ok, _ := c.Allow(ctx, postBy(t, stranger, 99)); ok {
		t.Fatal("a blocked key's post was accepted")
	}
	// A day later it gets another chance.
	later := time.Unix(1_700_000_000, 0).Add(blockFor + time.Hour)
	c.Now = func() time.Time { return later }
	calls := 0
	c.Verify = func(context.Context, media.File, Meta) error { calls++; return nil }
	if ok, err := c.Allow(ctx, postBy(t, stranger, 100)); err != nil || !ok || calls != 1 {
		t.Fatalf("after the block: %v %v calls %d", ok, err, calls)
	}
}

func TestACheckThatCannotBeMadeIsNotAVerdict(t *testing.T) {
	c, _, st := checkerFor(t, func(context.Context, media.File, Meta) error { return errors.New("flood wait") })
	stranger, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ok, err := c.Allow(context.Background(), postBy(t, stranger, 5))
	if ok || err == nil {
		t.Fatalf("a failed check gave %v %v", ok, err)
	}
	if st[repKey] != "" {
		t.Fatalf("a check that could not be made was recorded: %s", st[repKey])
	}
}

func TestAKeyWithAGoodRecordIsOnlySpotChecked(t *testing.T) {
	calls := 0
	c, _, _ := checkerFor(t, func(context.Context, media.File, Meta) error { calls++; return nil })
	stranger, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for doc := int64(1); doc <= reputableAfter; doc++ {
		if ok, err := c.Allow(ctx, postBy(t, stranger, doc)); err != nil || !ok {
			t.Fatalf("post %d: %v %v", doc, ok, err)
		}
	}
	if calls != reputableAfter {
		t.Fatalf("%d checks for %d first posts", calls, reputableAfter)
	}
	// The dice say "do not check", so the next posts pass without a look.
	for doc := int64(100); doc < 110; doc++ {
		if ok, err := c.Allow(ctx, postBy(t, stranger, doc)); err != nil || !ok {
			t.Fatalf("post %d: %v %v", doc, ok, err)
		}
	}
	if calls != reputableAfter {
		t.Fatalf("a reputable key was checked: %d calls", calls)
	}
	// The dice say "check this one", and a wrong post is caught.
	c.Rand = func() float64 { return 0 }
	c.Verify = func(context.Context, media.File, Meta) error { return ErrMismatch }
	if ok, _ := c.Allow(ctx, postBy(t, stranger, 200)); ok {
		t.Fatal("a wrong post of a reputable key got through a spot check")
	}
}

func TestNoVerifierMeansOnlyKnownKeys(t *testing.T) {
	c, own, _ := checkerFor(t, nil)
	stranger, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if ok, _ := c.Allow(ctx, postBy(t, stranger, 1)); ok {
		t.Fatal("a stranger got in with nothing to check it")
	}
	if ok, _ := c.Allow(ctx, postBy(t, own, 2)); !ok {
		t.Fatal("the own key was refused")
	}
	if err := c.Trust.Add(ctx, stranger.Public()); err != nil {
		t.Fatal(err)
	}
	if ok, _ := c.Allow(ctx, postBy(t, stranger, 3)); !ok {
		t.Fatal("a key added by hand was refused")
	}
	// A line that was not signed by the key it carries never gets in.
	forged := postBy(t, stranger, 4)
	forged.Caption = strings.Replace(forged.Caption, ";hs=none", ";hs=fa", 1)
	if ok, _ := c.Allow(ctx, forged); ok {
		t.Fatal("an altered line got in")
	}
}

func TestSubsLineRoundTripAndIsSigned(t *testing.T) {
	key, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := Subs{
		DocID: 6014791683013616491, VideoHash: "9f2cAAAAAAAAAAAAAAAAAA", ArchiveHash: ArchiveHash([]byte("zip")),
		Tracks: []Track{{Lang: "fa", Codec: "srt", Index: 3, HasIndex: true}, {Lang: "en", Codec: "ass", Index: 5, HasIndex: true}},
	}
	caption := "Subtitles: Title\n" + key.SignSubs(s) + "\n#tm_tv_1_1_1"
	got, ok := ParseSubsCaption(caption)
	if !ok || !got.Authentic() || got.KeyID != key.KeyID() {
		t.Fatalf("parse %+v %v", got, ok)
	}
	if got.DocID != s.DocID || got.VideoHash != s.VideoHash || got.ArchiveHash != s.ArchiveHash || len(got.Tracks) != 2 || got.Tracks[1].Index != 5 {
		t.Fatalf("fields lost: %+v", got)
	}
	for name, change := range map[string]func(*Subs){
		"video":   func(x *Subs) { x.DocID = 1 },
		"archive": func(x *Subs) { x.ArchiveHash = "other" },
		"track":   func(x *Subs) { x.Tracks[0].Lang = "de" },
	} {
		tampered, _ := ParseSubsCaption(caption)
		change(&tampered)
		if tampered.Authentic() {
			t.Errorf("a changed %s still verified", name)
		}
	}
	// A video line is not a subtitle line.
	if _, ok := ParseSubsCaption(key.Sign(sample())); ok {
		t.Fatal("a video line was read as a subtitle line")
	}
}
