package tgsource

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/gotd/td/tgerr"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

type fakeTG struct {
	posts  []tguser.Post // every message of the source, any order
	copied [][]int
	flood  int // number of copies to refuse with a flood wait first
	fail   error
	free   bool
}

func (f *fakeTG) Lookup(context.Context, string) (tguser.Channel, error) {
	return tguser.Channel{ID: 500, Username: "movies", Title: "Movies"}, nil
}

func (f *fakeTG) HasFreeSession() bool { return f.free }
func (f *fakeTG) HasAccount() bool     { return true }

func (f *fakeTG) History(_ context.Context, _ tguser.SourceRef, offset, limit int) (tguser.Page, error) {
	var older []tguser.Post
	for _, p := range f.posts {
		if offset == 0 || p.ID < offset {
			older = append(older, p)
		}
	}
	sort.Slice(older, func(i, j int) bool { return older[i].ID > older[j].ID })
	more := len(older) > limit
	if more {
		older = older[:limit]
	}
	pg := tguser.Page{Posts: older, More: more}
	for _, p := range older {
		pg.Highest = max(pg.Highest, p.ID)
		if pg.Lowest == 0 || p.ID < pg.Lowest {
			pg.Lowest = p.ID
		}
		if pg.Oldest.IsZero() || p.Date.Before(pg.Oldest) {
			pg.Oldest = p.Date
		}
	}
	return pg, nil
}

func (f *fakeTG) Copy(_ context.Context, _ tguser.SourceRef, ids []int) error {
	if f.flood > 0 {
		f.flood--
		return tgerr.New(420, "FLOOD_WAIT_3")
	}
	if f.fail != nil {
		return f.fail
	}
	f.copied = append(f.copied, append([]int(nil), ids...))
	return nil
}

func (f *fakeTG) all() []int {
	var out []int
	for _, b := range f.copied {
		out = append(out, b...)
	}
	sort.Ints(out)
	return out
}

// drain runs passes until older history is finished.
func drain(t *testing.T, s *Service) {
	t.Helper()
	for i := 0; i < 40; i++ {
		if !s.Pass(context.Background()) {
			return
		}
	}
	t.Fatal("history did not finish")
}

func movie(id int, date time.Time) tguser.Post {
	return tguser.Post{ID: id, Date: date, DocID: int64(1000 + id), Size: 900 << 20, MIME: "video/x-matroska",
		Name: fmt.Sprintf("Movie.%d.2020.mkv", id), Duration: 100 * time.Minute}
}

func newService(t *testing.T, tg *fakeTG, now time.Time) (*Service, catalog.Source) {
	t.Helper()
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cat.Close() })
	s := New()
	s.Cat, s.TG, s.LibraryChannel = cat, tg, 1
	s.Now = func() time.Time { return now }
	s.Sleep = func(context.Context, time.Duration) error { return nil }
	src, err := s.Add(context.Background(), "@movies")
	if err != nil {
		t.Fatal(err)
	}
	return s, src
}

func enable(t *testing.T, s *Service, mod func(*Settings)) {
	t.Helper()
	set := Defaults()
	set.Enabled = true
	if mod != nil {
		mod(&set)
	}
	if err := s.Save(context.Background(), set); err != nil {
		t.Fatal(err)
	}
}

func TestWanted(t *testing.T) {
	set := Defaults()
	good := movie(1, time.Now())
	cases := []struct {
		name string
		mod  func(*tguser.Post)
		want bool
	}{
		{"a film", func(*tguser.Post) {}, true},
		{"a trailer by name", func(p *tguser.Post) { p.Name = "Movie.Trailer.mkv" }, false},
		{"a trailer by caption", func(p *tguser.Post) { p.Caption = "official TRAILER" }, false},
		{"too short", func(p *tguser.Post) { p.Duration = 3 * time.Minute }, false},
		{"unknown length is kept", func(p *tguser.Post) { p.Duration = 0 }, true},
		{"too small", func(p *tguser.Post) { p.Size = 5 << 20 }, false},
		{"a gif", func(p *tguser.Post) { p.Animated = true }, false},
		{"a zip", func(p *tguser.Post) { p.MIME = "application/zip"; p.Name = "a.zip" }, false},
		{"an mkv without a video mime", func(p *tguser.Post) { p.MIME = "application/octet-stream" }, true},
	}
	for _, c := range cases {
		p := good
		c.mod(&p)
		if got, why := set.Wanted(p); got != c.want {
			t.Errorf("%s: got %v (%s), want %v", c.name, got, why, c.want)
		}
	}
	set.NeedTitle = true
	p := good
	p.Name = "random clip.mkv"
	if ok, _ := set.Wanted(p); ok {
		t.Error("a name without year or episode should be refused when a title is needed")
	}
	set.OnlyWords = "dubbed"
	p = good
	if ok, _ := set.Wanted(p); ok {
		t.Error("required word missing")
	}
	p.Caption = "Dubbed"
	if ok, _ := set.Wanted(p); !ok {
		t.Error("required word present")
	}
}

func TestSettingsValidate(t *testing.T) {
	bad := Defaults()
	bad.EveryMinutes = 1
	if bad.Validate() == nil {
		t.Error("an interval under five minutes must be refused")
	}
	bad = Defaults()
	bad.MinMB = -1
	if bad.Validate() == nil {
		t.Error("negative numbers must be refused")
	}
	if Defaults().Validate() != nil {
		t.Error("the defaults must be valid")
	}
}

func TestWalkCopiesOnceAndOnlyNew(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	tg := &fakeTG{free: true}
	for i := 1; i <= 250; i++ {
		tg.posts = append(tg.posts, movie(i, now.Add(-time.Hour)))
	}
	// A short clip and a non-video must be left alone.
	tg.posts = append(tg.posts, tguser.Post{ID: 251, Date: now.Add(-time.Hour), DocID: 9, Size: 1 << 20, MIME: "video/mp4", Name: "x.mp4"})
	s, _ := newService(t, tg, now)
	enable(t, s, func(set *Settings) { set.MaxPerDay = 0 })
	drain(t, s)
	if got := len(tg.all()); got != 250 {
		t.Fatalf("history copied %d, want 250", got)
	}
	// Nothing new: a later pass copies nothing.
	before := len(tg.copied)
	if s.Pass(context.Background()) {
		t.Fatal("a caught-up channel asked to run again at once")
	}
	if len(tg.copied) != before {
		t.Fatalf("second pass copied again")
	}
	// New posts: only they are copied.
	tg.posts = append(tg.posts, movie(300, now), movie(301, now))
	s.Pass(context.Background())
	if got := tg.all(); len(got) != 252 || got[250] != 300 || got[251] != 301 {
		t.Fatalf("incremental pass copied %v", got[len(got)-3:])
	}
}

func TestLookBackStopsAtTheCutoff(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	tg := &fakeTG{free: true}
	tg.posts = append(tg.posts, movie(1, now.AddDate(0, 0, -30)), movie(2, now.AddDate(0, 0, -20)), movie(3, now.AddDate(0, 0, -2)), movie(4, now.AddDate(0, 0, -1)))
	s, _ := newService(t, tg, now)
	enable(t, s, nil) // seven days
	s.Pass(context.Background())
	if got := tg.all(); len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Fatalf("a week back copied %v", got)
	}
	// Widening the window to all history walks again and takes the rest, without repeating.
	enable(t, s, func(set *Settings) { set.LookBackDays = 0 })
	s.Pass(context.Background())
	if got := tg.all(); len(got) != 4 {
		t.Fatalf("after widening, copied %v", got)
	}
}

func TestCopiesTheSubtitleDirectlyBelow(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	tg := &fakeTG{free: true}
	film := movie(10, now)
	film.DocID = 555
	// A note sits under the film, so the archive one message further down is not taken.
	note := tguser.Post{ID: 11, Date: now, DocID: 8, Size: 20, MIME: "text/plain", Name: "note.txt"}
	late := tguser.Post{ID: 12, Date: now, DocID: 9, Size: 2000, MIME: "application/zip", Name: archiveName(film.DocID)}
	// This archive is the next message, so it is forwarded with the film.
	other := movie(20, now)
	other.DocID = 777
	sub := tguser.Post{ID: 21, Date: now, DocID: 4, Size: 2000, MIME: "application/zip", Name: archiveName(other.DocID)}
	tg.posts = []tguser.Post{film, note, late, other, sub}
	s, _ := newService(t, tg, now)
	enable(t, s, func(set *Settings) { set.MaxPerDay = 0 })
	s.Pass(context.Background())
	if got := tg.all(); len(got) != 3 || got[0] != 10 || got[1] != 20 || got[2] != 21 {
		t.Fatalf("copied %v, want the two films and only the archive under the second", got)
	}
	var paired bool
	for _, batch := range tg.copied {
		hasFilm, hasSub := false, false
		for _, id := range batch {
			if id == 20 {
				hasFilm = true
			}
			if id == 21 {
				hasSub = true
			}
		}
		paired = paired || (hasFilm && hasSub)
	}
	if !paired {
		t.Fatalf("archive was not forwarded with its film: %v", tg.copied)
	}
}

func TestSubtitleOnTheNewerPageIsKept(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	tg := &fakeTG{free: true}
	film := movie(50, now)
	film.DocID = 777
	tg.posts = append(tg.posts, film)
	tg.posts = append(tg.posts, tguser.Post{ID: 51, Date: now, DocID: 4, Size: 2000, MIME: "application/zip", Name: archiveName(film.DocID)})
	for i := 52; i <= 150; i++ {
		tg.posts = append(tg.posts, movie(i, now))
	}
	s, _ := newService(t, tg, now)
	enable(t, s, func(set *Settings) { set.MaxPerDay = 0; set.LookBackDays = 0 })
	drain(t, s)
	var paired bool
	for _, batch := range tg.copied {
		hasFilm, hasSub := false, false
		for _, id := range batch {
			if id == 50 {
				hasFilm = true
			}
			if id == 51 {
				hasSub = true
			}
		}
		paired = paired || (hasFilm && hasSub)
	}
	if !paired {
		t.Fatalf("archive on the newer page was not forwarded with message 50: %v", tg.copied)
	}
}

func TestStoredDailyCapOf500IsCleared(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	tg := &fakeTG{free: true}
	s, _ := newService(t, tg, now)
	raw := `{"enabled":true,"max_per_day":500,"every_minutes":30,"look_back_days":7,"min_minutes":15,"min_mb":100,"pause_seconds":120,"skip_words":"trailer,teaser,sample,promo","auto_add":true}`
	if err := s.Cat.SetStateValue(context.Background(), "tgsource_settings", raw); err != nil {
		t.Fatal(err)
	}
	s.loaded = false
	got, err := s.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxPerDay != 0 {
		t.Fatalf("stored cap %d, want unlimited", got.MaxPerDay)
	}
}

func TestDailyCapStopsAndResumes(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	tg := &fakeTG{free: true}
	for i := 1; i <= 25; i++ {
		tg.posts = append(tg.posts, movie(i, now.Add(-time.Hour)))
	}
	s, _ := newService(t, tg, now)
	enable(t, s, func(set *Settings) { set.MaxPerDay = 10 })
	s.Pass(context.Background())
	if got := len(tg.all()); got != 10 {
		t.Fatalf("copied %d under a cap of 10", got)
	}
	// Tomorrow the rest follows.
	s.Now = func() time.Time { return now.Add(24 * time.Hour) }
	s.Pass(context.Background())
	if got := len(tg.all()); got != 20 {
		t.Fatalf("next day copied %d in total, want 20", got)
	}
}

func TestFloodWaitIsSleptOutAndRetried(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	tg := &fakeTG{free: true, flood: 1}
	tg.posts = []tguser.Post{movie(1, now)}
	s, _ := newService(t, tg, now)
	var slept time.Duration
	s.Sleep = func(_ context.Context, d time.Duration) error { slept += d; return nil }
	enable(t, s, nil)
	s.Pass(context.Background())
	if len(tg.all()) != 1 || slept < 3*time.Second {
		t.Fatalf("copied %v after sleeping %v", tg.all(), slept)
	}
}

func TestForwardingForbiddenIsRemembered(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	tg := &fakeTG{free: true, fail: tgerr.New(400, "CHAT_FORWARDS_RESTRICTED")}
	tg.posts = []tguser.Post{movie(1, now)}
	s, src := newService(t, tg, now)
	enable(t, s, nil)
	s.Pass(context.Background())
	got, _, _ := s.Cat.Source(context.Background(), src.ID)
	if !got.NoForwards || got.Error == "" {
		t.Fatalf("source not flagged: %+v", got)
	}
	// A flagged channel is not tried again.
	tg.fail = nil
	s.Pass(context.Background())
	if len(tg.all()) != 0 {
		t.Fatal("a channel that forbids copying was tried again")
	}
}

func TestLibraryChannelIsRefused(t *testing.T) {
	now := time.Now()
	tg := &fakeTG{free: true}
	s, _ := newService(t, tg, now)
	s.LibraryChannel = 500
	if _, err := s.Add(context.Background(), "@movies"); err == nil {
		t.Fatal("the library channel must not be a source")
	}
}

func TestParseChannelRef(t *testing.T) {
	cases := []struct {
		in   string
		id   int64
		name string
		bad  bool
	}{
		{"@some_channel", 0, "some_channel", false},
		{"some_channel", 0, "some_channel", false},
		{"https://t.me/some_channel", 0, "some_channel", false},
		{"https://t.me/some_channel/1234", 0, "some_channel", false},
		{"t.me/s/some_channel", 0, "some_channel", false},
		{"-1001234567890", 1234567890, "", false},
		{"1234567890", 1234567890, "", false},
		{"https://t.me/c/1234567890/55", 1234567890, "", false},
		{"https://t.me/+AbCdEf", 0, "", true},
		{"", 0, "", true},
		{"hi", 0, "", true},
	}
	for _, c := range cases {
		id, name, err := tguser.ParseChannelRef(c.in)
		if (err != nil) != c.bad || id != c.id || name != c.name {
			t.Errorf("%q: got %d %q %v", c.in, id, name, err)
		}
	}
}

func TestSameFileFromTwoChannelsIsCopiedOnce(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	a := movie(1, now)
	b := movie(2, now) // a different document id, same name and size
	b.Name = a.Name
	tg := &fakeTG{free: true, posts: []tguser.Post{a, b}}
	s, _ := newService(t, tg, now)
	enable(t, s, nil)
	s.Pass(context.Background())
	if got := len(tg.all()); got != 1 {
		t.Fatalf("copied %d, want the same file once", got)
	}
}

func TestStrictChannelNeedsATitle(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	odd := movie(1, now)
	odd.Name = "random clip.mkv"
	tg := &fakeTG{free: true, posts: []tguser.Post{odd}}
	s, src := newService(t, tg, now)
	if err := s.Strict(context.Background(), src.ID, true); err != nil {
		t.Fatal(err)
	}
	enable(t, s, nil)
	s.Pass(context.Background())
	if len(tg.all()) != 0 {
		t.Fatal("a strict channel copied a file with no title in its name")
	}
}

func TestBannedDiscoveredChannelIsRemoved(t *testing.T) {
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cat.Close() })
	ctx := context.Background()
	found, added, err := cat.AddDiscoveredSource(ctx, catalog.Source{ChannelID: 9, Title: "old"}, "test")
	if err != nil || !added {
		t.Fatal(added, err)
	}
	manual, err := cat.AddSource(ctx, catalog.Source{ChannelID: 10, Title: "mine"})
	if err != nil {
		t.Fatal(err)
	}
	s := New()
	s.Cat = cat
	if stop := s.record(ctx, found, tgerr.New(400, "CHANNEL_PRIVATE")); stop {
		t.Fatal("a banned channel stopped the pass")
	}
	if _, ok, err := cat.SourceByChannel(ctx, 9); err != nil || ok {
		t.Fatalf("discovered channel still listed: %v %v", ok, err)
	}
	if stop := s.record(ctx, manual, tgerr.New(400, "CHANNEL_PRIVATE")); stop {
		t.Fatal("stop")
	}
	if _, ok, err := cat.SourceByChannel(ctx, 10); err != nil || !ok {
		t.Fatalf("a channel a person added was removed: %v %v", ok, err)
	}
}

func TestIdleDiscoveredChannelFreesASlot(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	old := catalog.Source{Origin: catalog.OriginDiscovered, Added: now.Add(-30 * 24 * time.Hour).Unix()}
	if !sourceDroppable(old, 0, now) {
		t.Fatal("a quiet discovered channel should free its place")
	}
	fresh := catalog.Source{Origin: catalog.OriginDiscovered, Added: now.Add(-24 * time.Hour).Unix()}
	if sourceDroppable(fresh, 0, now) {
		t.Fatal("a new channel was treated as idle")
	}
	if sourceDroppable(catalog.Source{Origin: catalog.OriginManual, Added: 1}, 0, now) {
		t.Fatal("a channel a person added was droppable")
	}
	banned := catalog.Source{Origin: catalog.OriginDiscovered, Added: now.Unix(), Error: "This account cannot read the channel any more."}
	if !sourceDroppable(banned, now.Unix(), now) {
		t.Fatal("a banned channel was kept")
	}
}

func TestChannelLookBackOverridesTheRule(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	tg := &fakeTG{free: true, posts: []tguser.Post{movie(2, now), movie(1, now.AddDate(0, 0, -3))}}
	s, src := newService(t, tg, now)
	enable(t, s, func(set *Settings) { set.LookBackDays = 7 })
	if err := s.SetLookBack(context.Background(), src.ID, 1); err != nil {
		t.Fatal(err)
	}
	s.Pass(context.Background())
	if got := tg.all(); len(got) != 1 || got[0] != 2 {
		t.Fatalf("channel window copied %v, want only the post from today", got)
	}
}
