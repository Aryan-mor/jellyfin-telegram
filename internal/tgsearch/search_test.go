package tgsearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tgerr"

	"github.com/Aryan-mor/jellyfin-telegram/internal/autofilter"
	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/sign"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

const mb = 1 << 20

func openCat(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func newKey(t *testing.T) *sign.Key {
	t.Helper()
	k, err := sign.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func signedCaption(k *sign.Key, tmdb int, size int64) string {
	m := sign.Meta{Kind: "m", TMDB: tmdb, Quality: "1080p", Hard: "none", Subs: "no", DocID: 42, Size: size}
	return "Film #tm_m_" + strconv.Itoa(tmdb) + "\n" + k.Sign(m)
}

func TestVerifyThresholds(t *testing.T) {
	q := Query{TMDB: 603, Movie: true, Runtime: 120}
	th := DefaultThresholds()
	base := tguser.SearchHit{ChannelID: 4, MessageID: 1, Caption: "x #tm_m_603", Video: true, Size: 900 * mb, Duration: 120 * 60}
	cases := []struct {
		name   string
		edit   func(*tguser.SearchHit)
		wantOK bool
	}{
		{"good", func(*tguser.SearchHit) {}, true},
		{"sample by size", func(h *tguser.SearchHit) { h.Size = 5 * mb }, false},
		{"trailer by duration", func(h *tguser.SearchHit) { h.Duration = 150 }, false},
		{"runtime too long", func(h *tguser.SearchHit) { h.Duration = 200 * 60 }, false},
		{"runtime within tolerance", func(h *tguser.SearchHit) { h.Duration = 135 * 60 }, true},
		{"unknown duration passes", func(h *tguser.SearchHit) { h.Duration = 0 }, true},
		{"other title", func(h *tguser.SearchHit) { h.Caption = "x #tm_m_6030" }, false},
	}
	for _, c := range cases {
		h := base
		c.edit(&h)
		if v := Verify(q, h, "#tm_m_603", th); v.OK != c.wantOK {
			t.Errorf("%s: ok=%v reason=%q", c.name, v.OK, v.Reason)
		}
	}
	// Runtime is not enforced for an episode, or when TMDB gave none.
	ep := Query{TMDB: 1396}
	h := base
	h.Caption, h.Duration = "e #tm_tv_1396_1_2", 45*60
	if v := Verify(ep, h, "#tm_tv_1396_1_2", th); !v.OK {
		t.Error(v.Reason)
	}
	if v := Verify(Query{TMDB: 603, Movie: true}, base, "#tm_m_603", th); !v.OK {
		t.Error(v.Reason)
	}
}

func TestVerifyLanguageSuffixUsesTheBaseTag(t *testing.T) {
	th := Thresholds{}
	hit := tguser.SearchHit{ChannelID: 4, MessageID: 1, Caption: "ep\n#tm_tv_231751_1_6_en_fa", Video: true, Size: 900 * mb, Duration: 40 * 60}
	if v := Verify(Query{TMDB: 231751}, hit, "#tm_tv_231751_1_6_en_fa", th); !v.OK {
		t.Fatal(v.Reason)
	}
	if v := Verify(Query{TMDB: 2317}, hit, "#tm_tv_231751_1_6_en_fa", th); v.OK {
		t.Fatal("accepted a shorter series id")
	}
	bare := tguser.SearchHit{ChannelID: 4, MessageID: 2, Caption: "ep\n#tm_tv_231751_1_6", Video: true, Size: 900 * mb, Duration: 40 * 60}
	if v := Verify(Query{TMDB: 231751}, bare, "#tm_tv_231751_1_6", th); !v.OK {
		t.Fatal(v.Reason)
	}

	k := newKey(t)
	meta := sign.Meta{Kind: "e", TMDB: 231751, Season: 1, Episode: 6, Hard: "none", Subs: "no", DocID: 42, Size: 900 * mb}
	signed := tguser.SearchHit{ChannelID: 4, MessageID: 3, Caption: "ep\n" + k.Sign(meta) + "\n#tm_tv_231751_1_6_en_fa", Size: 900 * mb, Video: true, Duration: 40 * 60}
	if v := Verify(Query{TMDB: 231751}, signed, "#tm_tv_231751_1_6_en_fa", th); !v.OK {
		t.Fatal(v.Reason)
	}
	other := sign.Meta{Kind: "e", TMDB: 231751, Season: 1, Episode: 7, Hard: "none", Subs: "no", DocID: 42, Size: 900 * mb}
	signed.Caption = "ep\n" + k.Sign(other) + "\n#tm_tv_231751_1_6_en_fa"
	if v := Verify(Query{TMDB: 231751}, signed, "#tm_tv_231751_1_6_en_fa", th); v.OK || !v.BlameKey {
		t.Fatalf("different episode: %+v", v)
	}
}

func TestVerifySignature(t *testing.T) {
	k := newKey(t)
	q := Query{TMDB: 603, Movie: true}
	th := DefaultThresholds()
	good := tguser.SearchHit{ChannelID: 4, MessageID: 1, Caption: signedCaption(k, 603, 900*mb), Size: 900 * mb, Video: true, Duration: 7000}
	v := Verify(q, good, "#tm_m_603", th)
	if !v.OK || !v.Signed || v.KeyID != k.KeyID() {
		t.Fatalf("%+v", v)
	}

	// A line edited after signing fails, and does not blame the key it names.
	forged := good
	forged.Caption = strings.Replace(good.Caption, "q=1080p", "q=2160p", 1)
	if v := Verify(q, forged, "#tm_m_603", th); v.OK || v.BlameKey || v.KeyID != "" {
		t.Fatalf("forged: %+v", v)
	}
	// A genuine signature for another title blames the signer.
	other := good
	other.Caption = "Film #tm_m_603\n" + k.Sign(sign.Meta{Kind: "m", TMDB: 604, Hard: "none", Subs: "no", DocID: 42, Size: 900 * mb})
	if v := Verify(q, other, "#tm_m_603", th); v.OK || !v.BlameKey || v.KeyID != k.KeyID() {
		t.Fatalf("other: %+v", v)
	}
	// A genuine signature over a different size blames the signer.
	size := good
	size.Size = 800 * mb
	if v := Verify(q, size, "#tm_m_603", th); v.OK || !v.BlameKey {
		t.Fatalf("size: %+v", v)
	}
	// A jftg line that cannot be read is refused.
	junk := good
	junk.Caption = "Film #tm_m_603\njftg:2;broken"
	if v := Verify(q, junk, "#tm_m_603", th); v.OK {
		t.Fatal("junk accepted")
	}
	// The signed running time stands in for a missing one.
	short := tguser.SearchHit{ChannelID: 4, MessageID: 1, Size: 900 * mb, Video: true}
	m := sign.Meta{Kind: "m", TMDB: 603, Hard: "none", Subs: "no", DocID: 42, Size: 900 * mb, Duration: 90}
	short.Caption = "Film #tm_m_603\n" + k.Sign(m)
	if v := Verify(q, short, "#tm_m_603", th); v.OK {
		t.Fatal("signed short duration accepted")
	}
}

func TestChoosePrefersTrustedChannel(t *testing.T) {
	s := &Service{trust: map[string]catalog.Trust{"channel/7": {Points: 5}}}
	hits := []tguser.SearchHit{
		{ChannelID: 4, MessageID: 1, Caption: "#tm_m_603", Video: true, Size: 900},
		{ChannelID: 7, MessageID: 2, Caption: "#tm_m_603", Video: true, Size: 100},
	}
	hit, _, ok := s.choose(context.Background(), Query{TMDB: 603, Movie: true}, hits, nil, nil)
	if !ok || hit.ChannelID != 7 {
		t.Fatalf("%+v", hit)
	}
	s.trust["channel/7"] = catalog.Trust{Points: 5, Denied: true}
	hit, _, _ = s.choose(context.Background(), Query{TMDB: 603, Movie: true}, hits, nil, nil)
	if hit.ChannelID != 4 {
		t.Fatalf("denied channel chosen: %+v", hit)
	}
}

type recTG struct {
	hits   []tguser.SearchHit
	copied []int
	search int
	err    error
}

func (r *recTG) SearchHashtag(context.Context, string) ([]tguser.SearchHit, error) {
	r.search++
	return r.hits, r.err
}
func (r *recTG) InReady(context.Context, string) (bool, error) { return false, nil }
func (r *recTG) Copy(_ context.Context, _ tguser.SourceRef, ids []int) error {
	r.copied = append(r.copied, ids...)
	return nil
}

type recPromoter struct{ got []int64 }

func (p *recPromoter) AddDiscovered(_ context.Context, id int64, _ string) (bool, error) {
	p.got = append(p.got, id)
	return true, nil
}

type listWant []Query

func (l listWant) Queries(context.Context) ([]Query, error) { return l, nil }

func TestPointsPromotionAndPersistence(t *testing.T) {
	cat := openCat(t)
	k := newKey(t)
	ctx := context.Background()
	prom := &recPromoter{}
	tg := &recTG{}
	mk := func(want ...Query) *Service {
		return &Service{
			TG: tg, Want: listWant(want), Store: cat, Promote: prom, Verify: DefaultThresholds(),
			Limits: Limits{PromoteAt: 3, MaxDiscovered: 5, QueryCooldown: -1},
		}
	}
	hitFor := func(tmdb int) tguser.SearchHit {
		return tguser.SearchHit{ChannelID: 9, AccessHash: 1, MessageID: tmdb, Caption: signedCaption(k, tmdb, 900*mb), Size: 900 * mb, Video: true, Duration: 6000}
	}
	// Each pass sees one new file from channel 9, signed by key k.
	for i, tmdb := range []int{1, 2, 3} {
		tg.hits = []tguser.SearchHit{hitFor(tmdb)}
		mk(Query{TMDB: tmdb, Movie: true}).pass(ctx)
		tr, _ := cat.GetTrust(ctx, catalog.TrustChannel, "9")
		if tr.Points != i+1 {
			t.Fatalf("after %d: channel points %d", i+1, tr.Points)
		}
	}
	if kt, _ := cat.GetTrust(ctx, catalog.TrustKey, k.KeyID()); kt.Points != 3 {
		t.Fatalf("key points %d", kt.Points)
	}
	if len(prom.got) != 1 || prom.got[0] != 9 {
		t.Fatalf("promoted %v", prom.got)
	}

	// A restart does not copy the same title again.
	before := len(tg.copied)
	tg.hits = []tguser.SearchHit{hitFor(1)}
	mk(Query{TMDB: 1, Movie: true}).pass(ctx)
	if len(tg.copied) != before {
		t.Fatal("copied again after restart")
	}

	// The search cooldown survives a restart too.
	n := tg.search
	tg.hits = nil
	for i := 0; i < 2; i++ {
		s := mk(Query{TMDB: 50, Movie: true})
		s.Limits.QueryCooldown = time.Hour
		s.pass(ctx)
	}
	if tg.search != n+1 {
		t.Fatalf("searched %d times", tg.search-n)
	}
}

func TestPromotionStopsAtTheCap(t *testing.T) {
	cat := openCat(t)
	ctx := context.Background()
	if _, added, err := cat.AddDiscoveredSource(ctx, catalog.Source{ChannelID: 77, Title: "x"}, "test"); err != nil || !added {
		t.Fatal(added, err)
	}
	prom := &recPromoter{}
	tg := &recTG{hits: []tguser.SearchHit{{ChannelID: 9, MessageID: 1, Caption: "#tm_m_5", Video: true, Size: 900 * mb, Duration: 6000}}}
	s := &Service{TG: tg, Want: listWant{{TMDB: 5, Movie: true}}, Store: cat, Promote: prom, Verify: DefaultThresholds(),
		Limits: Limits{PromoteAt: 1, MaxDiscovered: 1}}
	s.pass(ctx)
	if len(tg.copied) != 1 || len(prom.got) != 0 {
		t.Fatalf("copied %v promoted %v", tg.copied, prom.got)
	}
	src, ok, err := cat.SourceByChannel(ctx, 77)
	if err != nil || !ok || src.Origin != catalog.OriginDiscovered || src.OriginNote != "test" {
		t.Fatalf("%+v %v %v", src, ok, err)
	}
}

func TestRepeatedFailuresDenylistAChannel(t *testing.T) {
	cat := openCat(t)
	ctx := context.Background()
	tg := &recTG{}
	for i := 1; i <= 4; i++ {
		// A tiny file each time: a sample.
		tg.hits = []tguser.SearchHit{{ChannelID: 9, MessageID: i, Caption: "#tm_m_7", Video: true, Size: mb, Duration: 6000}}
		s := &Service{TG: tg, Want: listWant{{TMDB: 7, Movie: true}}, Store: cat, Verify: DefaultThresholds(), Limits: Limits{DenyAfter: 3, QueryCooldown: -1}}
		s.pass(ctx)
	}
	tr, _ := cat.GetTrust(ctx, catalog.TrustChannel, "9")
	if !tr.Denied || tr.Fails != 3 || tr.Points != 0 {
		t.Fatalf("%+v", tr)
	}
	if len(tg.copied) != 0 {
		t.Fatal("copied a sample")
	}
}

func TestForgedSignatureDoesNotBlameTheKey(t *testing.T) {
	cat := openCat(t)
	ctx := context.Background()
	k := newKey(t)
	capt := strings.Replace(signedCaption(k, 8, 900*mb), "q=1080p", "q=720p", 1)
	tg := &recTG{hits: []tguser.SearchHit{{ChannelID: 9, MessageID: 1, Caption: capt, Size: 900 * mb, Video: true, Duration: 6000}}}
	s := &Service{TG: tg, Want: listWant{{TMDB: 8, Movie: true}}, Store: cat, Verify: DefaultThresholds()}
	s.pass(ctx)
	if kt, _ := cat.GetTrust(ctx, catalog.TrustKey, k.KeyID()); kt.Fails != 0 {
		t.Fatal("forged line blamed the key")
	}
	if ct, _ := cat.GetTrust(ctx, catalog.TrustChannel, "9"); ct.Fails != 1 {
		t.Fatalf("%+v", ct)
	}
}

func TestFloodWaitBacksOff(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tg := &recTG{err: tgerr.New(420, "FLOOD_WAIT_300")}
	s := &Service{TG: tg, Want: listWant{{TMDB: 1, Movie: true}, {TMDB: 2, Movie: true}}, Now: func() time.Time { return now }}
	s.pass(context.Background())
	if tg.search != 1 {
		t.Fatalf("searched %d times after a flood wait", tg.search)
	}
	now = now.Add(2 * time.Minute)
	s.pass(context.Background())
	if tg.search != 1 {
		t.Fatal("searched during the back-off")
	}
	now = now.Add(10 * time.Minute)
	s.pass(context.Background())
	if tg.search != 2 {
		t.Fatalf("did not resume: %d", tg.search)
	}
}

func TestSearchesRunOneAtATime(t *testing.T) {
	var gaps int
	tg := &recTG{}
	s := &Service{TG: tg, Want: listWant{{TMDB: 1, Movie: true}, {TMDB: 2, Movie: true}, {TMDB: 3, Movie: true}},
		Limits: Limits{QueryGap: time.Second}, Sleep: func(context.Context, time.Duration) error { gaps++; return nil }}
	s.pass(context.Background())
	if tg.search != 3 || gaps != 2 {
		t.Fatalf("searches %d pauses %d", tg.search, gaps)
	}
}

type memState map[string]string

func (m memState) StateValue(_ context.Context, k string) (string, error) { return m[k], nil }
func (m memState) SetStateValue(_ context.Context, k, v string) error     { m[k] = v; return nil }

func tmdbServer(t *testing.T, hits *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "k" {
			http.Error(w, "no", 401)
			return
		}
		enc := json.NewEncoder(w)
		switch {
		case r.URL.Path == "/trending/all/day":
			*hits++
			_ = enc.Encode(map[string]any{"results": []map[string]any{
				{"id": 1, "media_type": "movie", "release_date": "2026-01-01", "genre_ids": []int{28}},
				{"id": 2, "media_type": "movie", "release_date": "2099-01-01", "genre_ids": []int{28}},
				{"id": 3, "media_type": "movie", "release_date": "", "genre_ids": []int{28}},
				{"id": 4, "media_type": "movie", "release_date": "2026-01-01", "adult": true, "genre_ids": []int{28}},
				{"id": 5, "media_type": "tv", "first_air_date": "2025-05-05", "genre_ids": []int{10759}},
				{"id": 6, "media_type": "movie", "release_date": "2026-01-01", "genre_ids": []int{27}},
				{"id": 7, "media_type": "movie", "release_date": "2026-02-01", "genre_ids": []int{18}},
				{"id": 8, "media_type": "person"},
			}})
		case strings.HasPrefix(r.URL.Path, "/genre/"):
			_ = enc.Encode(map[string]any{"genres": []map[string]any{{"id": 28, "name": "Action"}, {"id": 27, "name": "Horror"}, {"id": 18, "name": "Drama"}, {"id": 10759, "name": "Action & Adventure"}}})
		case strings.HasPrefix(r.URL.Path, "/movie/"):
			_ = enc.Encode(map[string]any{"runtime": 101})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestTrendingQueriesOncePerDay(t *testing.T) {
	var fetches int
	srv := tmdbServer(t, &fetches)
	defer srv.Close()
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	state := memState{}
	mk := func() *Trending {
		return &Trending{Key: "k", Base: srv.URL, Store: state, Now: func() time.Time { return now },
			Filter: autofilter.Filter{Genres: autofilter.Rule{Exclude: []string{"Horror"}}}, MaxPerDay: 2}
	}
	qs, err := mk().Queries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Unreleased, undated, adult, and excluded-genre titles are out; the cap keeps two.
	if len(qs) != 2 || qs[0] != (Query{TMDB: 1, Movie: true, Runtime: 101}) || qs[1].TMDB != 5 || qs[1].Movie {
		t.Fatalf("%+v", qs)
	}
	// A restart on the same day reuses the stored list and does not fetch.
	again, err := mk().Queries(context.Background())
	if err != nil || len(again) != 2 || fetches != 1 {
		t.Fatalf("%v fetches=%d err=%v", again, fetches, err)
	}
	// The next day fetches again.
	now = now.Add(24 * time.Hour)
	if _, err := mk().Queries(context.Background()); err != nil || fetches != 2 {
		t.Fatalf("fetches=%d err=%v", fetches, err)
	}
}

func TestTrendingWithoutAKeyIsQuiet(t *testing.T) {
	qs, err := (&Trending{}).Queries(context.Background())
	if err != nil || qs != nil {
		t.Fatal(qs, err)
	}
	if !Released("2026-11-08", "2026-11-08") || Released("2026-11-09", "2026-11-08") || Released("", "2026-11-08") {
		t.Fatal("Released")
	}
}

type histTG struct {
	recTG
	page tguser.Page
}

func (h *histTG) History(context.Context, tguser.SourceRef, int, int) (tguser.Page, error) {
	return h.page, nil
}

func vidPost(id int, caption string) tguser.Post {
	return tguser.Post{ID: id, Size: 900 * mb, MIME: "video/x-matroska", Name: "film.mkv", Caption: caption, Duration: 100 * time.Minute}
}

func TestFirstFileAuditsTheChannel(t *testing.T) {
	cat := openCat(t)
	ctx := context.Background()
	prom := &recPromoter{}
	tg := &histTG{page: tguser.Page{Posts: []tguser.Post{
		vidPost(1, "#tm_m_5"),
		vidPost(2, "#tm_m_8"),
		{ID: 3, Size: mb, MIME: "video/mp4", Name: "sample.mp4", Caption: "#tm_m_9", Duration: time.Minute},
	}}}
	tg.hits = []tguser.SearchHit{{ChannelID: 9, AccessHash: 1, MessageID: 1, Caption: "#tm_m_5", Video: true, Size: 900 * mb, Duration: 6000}}
	s := &Service{TG: tg, Want: listWant{{TMDB: 5, Movie: true}}, Store: cat, Promote: prom, Verify: DefaultThresholds(),
		Limits: Limits{PromoteAt: 2, MaxDiscovered: 5, QueryCooldown: -1}}
	s.pass(ctx)
	if len(tg.copied) != 1 || len(prom.got) != 1 || prom.got[0] != 9 {
		t.Fatalf("copied %v promoted %v", tg.copied, prom.got)
	}
}

func TestAShortChannelIsNotAdded(t *testing.T) {
	cat := openCat(t)
	ctx := context.Background()
	prom := &recPromoter{}
	tg := &histTG{page: tguser.Page{Posts: []tguser.Post{
		{ID: 3, Size: mb, MIME: "video/mp4", Name: "sample.mp4", Duration: time.Minute},
	}}}
	tg.hits = []tguser.SearchHit{{ChannelID: 9, MessageID: 1, Caption: "#tm_m_5", Video: true, Size: 900 * mb, Duration: 6000}}
	s := &Service{TG: tg, Want: listWant{{TMDB: 5, Movie: true}}, Store: cat, Promote: prom, Verify: DefaultThresholds(),
		Limits: Limits{PromoteAt: 2, MaxDiscovered: 5, QueryCooldown: -1}}
	s.pass(ctx)
	if len(tg.copied) != 1 || len(prom.got) != 0 {
		t.Fatalf("copied %v promoted %v", tg.copied, prom.got)
	}
}

type roomPromoter struct {
	recPromoter
	freed bool
}

func (p *roomPromoter) FreeSlot(context.Context) (bool, error) {
	p.freed = true
	return true, nil
}

func TestFullListDropsADeadChannel(t *testing.T) {
	cat := openCat(t)
	ctx := context.Background()
	if _, added, err := cat.AddDiscoveredSource(ctx, catalog.Source{ChannelID: 77, Title: "old"}, "test"); err != nil || !added {
		t.Fatal(added, err)
	}
	prom := &roomPromoter{}
	tg := &recTG{hits: []tguser.SearchHit{{ChannelID: 9, MessageID: 1, Caption: "#tm_m_5", Video: true, Size: 900 * mb, Duration: 6000}}}
	s := &Service{TG: tg, Want: listWant{{TMDB: 5, Movie: true}}, Store: cat, Promote: prom, Verify: DefaultThresholds(),
		Limits: Limits{PromoteAt: 1, MaxDiscovered: 1, QueryCooldown: -1}}
	s.pass(ctx)
	if !prom.freed || len(prom.got) != 1 || prom.got[0] != 9 {
		t.Fatalf("freed %v promoted %v", prom.freed, prom.got)
	}
}

func TestMultiJoinsAndDedupes(t *testing.T) {
	m := Multi{listWant{{TMDB: 1, Movie: true}}, listWant{{TMDB: 1, Movie: true}, {TMDB: 2}}}
	qs, _ := m.Queries(context.Background())
	if len(qs) != 2 {
		t.Fatalf("%+v", qs)
	}
}
