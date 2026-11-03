package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
	"github.com/Aryan-mor/jellyfin-telegram/internal/subtitles"
)

const (
	testRel = "Movies/Heat (1995)/Heat (1995).strm"
	testURL = "http://gw:8097/s/tok/5/1"
)

// fakeServer plays Jellyfin. onProbe runs when an item's PlaybackInfo is requested.
type fakeServer struct {
	items    []Item
	refresh  atomic.Int64
	onProbe  func(itemID string)
	streams  int
	probedID atomic.Value

	onUpload   func(itemID string)
	uploadLang atomic.Value
	uploads    atomic.Int64
	seriesIMDb string
}

func (f *fakeServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /Users", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"Id":"plain"},{"Id":"admin","Policy":{"IsAdministrator":true}}]`))
	})
	mux.HandleFunc("POST /Library/Refresh", func(w http.ResponseWriter, _ *http.Request) {
		f.refresh.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /Items", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("userId") != "admin" {
			http.Error(w, "wrong user", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Items": f.items})
	})
	mux.HandleFunc("POST /Videos/{id}/Subtitles", func(w http.ResponseWriter, r *http.Request) {
		f.uploads.Add(1)
		var body struct{ Language string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.uploadLang.Store(body.Language)
		if f.onUpload != nil {
			f.onUpload(r.PathValue("id"))
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /Items/{id}", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ProductionYear": 2018, "ProviderIds": map[string]string{"Imdb": f.seriesIMDb}})
	})
	mux.HandleFunc("GET /Items/{id}/PlaybackInfo", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		f.probedID.Store(id)
		if f.onProbe != nil {
			f.onProbe(id)
		}
		streams := make([]map[string]string, f.streams)
		_ = json.NewEncoder(w).Encode(map[string]any{"MediaSources": []map[string]any{
			{"Id": id, "RunTimeTicks": 600_000_000, "MediaStreams": streams},
		}})
	})
	return mux
}

// fakeWindow reports reads that fakeServer recorded while the window was open.
type fakeWindow struct{ reads atomic.Int64 }

func (w *fakeWindow) BeginProbe(int) func() int {
	w.reads.Store(0)
	return func() int { return int(w.reads.Load()) }
}

func setupProber(t *testing.T, srv *fakeServer) (*Prober, *catalog.Catalog) {
	t.Helper()
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cat.Close() })
	err = cat.Put(context.Background(), media.File{
		MessageID: 1, ChannelID: 5, DocID: 10, Size: 100, RelPath: testRel, Date: time.Unix(1, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	httpSrv := httptest.NewServer(srv.handler())
	t.Cleanup(httpSrv.Close)
	return &Prober{
		client: New(httpSrv.URL, "key"),
		cat:    cat,
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		// Short waits, so a file that never gets probed fails the test quickly.
		ScanWait: 150 * time.Millisecond,
		Poll:     10 * time.Millisecond,
	}, cat
}

func unprobed(t *testing.T, cat *catalog.Catalog) int {
	t.Helper()
	files, err := cat.Unprobed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(files)
}

func TestProbeWithReadsIsRecorded(t *testing.T) {
	win := &fakeWindow{}
	srv := &fakeServer{
		streams: 3,
		items:   []Item{{ID: "new", Path: "/media/" + testRel}},
		onProbe: func(string) { win.reads.Add(2) },
	}
	p, cat := setupProber(t, srv)
	p.window = win
	if err := p.Run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if unprobed(t, cat) != 0 {
		t.Error("file should be marked as probed")
	}
	if srv.refresh.Load() != 1 {
		t.Errorf("refreshed %d times", srv.refresh.Load())
	}
}

// A rebuilt catalog can meet an old Jellyfin item with the same stream URL. Jellyfin then
// answers with stored details and reads nothing, which must not count as a probe.
func TestAnswerFromOldItemIsNotAProbe(t *testing.T) {
	win := &fakeWindow{}
	srv := &fakeServer{
		streams: 3,
		items:   []Item{{ID: "old", Sources: []Source{{ID: "old", Path: testURL}}}},
	}
	p, cat := setupProber(t, srv)
	p.window = win
	if err := p.Run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if unprobed(t, cat) != 1 {
		t.Error("a probe that read nothing was recorded as done")
	}
}

func TestPathMatchBeatsOldItemWithSameURL(t *testing.T) {
	win := &fakeWindow{}
	srv := &fakeServer{
		streams: 3,
		items: []Item{
			{ID: "old", Sources: []Source{{ID: "old", Path: testURL}}},
			{ID: "new", Sources: []Source{{ID: "new", Path: "/media/" + testRel}}},
		},
		onProbe: func(id string) {
			if id == "new" {
				win.reads.Add(1)
			}
		},
	}
	p, cat := setupProber(t, srv)
	p.window = win
	if err := p.Run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if got := srv.probedID.Load(); got != "new" {
		t.Errorf("probed %v", got)
	}
	if unprobed(t, cat) != 0 {
		t.Error("file should be marked as probed")
	}
}

func TestProbedVersionIsFoundByURL(t *testing.T) {
	win := &fakeWindow{}
	srv := &fakeServer{
		streams: 2,
		items:   []Item{{ID: "main", Path: "/media/other.strm", Sources: []Source{{ID: "alt", Path: testURL}}}},
		onProbe: func(string) { win.reads.Add(1) },
	}
	p, cat := setupProber(t, srv)
	p.window = win
	if err := p.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if got := srv.probedID.Load(); got != "alt" {
		t.Errorf("probed %v", got)
	}
	if unprobed(t, cat) != 0 {
		t.Error("file should be marked as probed")
	}
}

func TestNoStreamsAndMissingItemsStayPending(t *testing.T) {
	win := &fakeWindow{}
	srv := &fakeServer{
		items:   []Item{{ID: "new", Path: "/media/" + testRel}},
		onProbe: func(string) { win.reads.Add(1) },
	}
	p, cat := setupProber(t, srv)
	p.window = win
	if err := p.Run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if unprobed(t, cat) != 1 {
		t.Error("a probe with no streams was recorded as done")
	}

	srv.items = nil
	if err := p.Run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if unprobed(t, cat) != 1 {
		t.Error("a file Jellyfin does not have was recorded as done")
	}
}

func TestWindowsPathsMatch(t *testing.T) {
	s := sourceIndex{{Source: Source{ID: "a", Path: `D:\media\` + strings.ReplaceAll(testRel, "/", `\`)}}}
	m, ok := s.find(media.File{MessageID: 1, ChannelID: 5, RelPath: testRel})
	if !ok || m.sourceID != "a" || !m.byPath {
		t.Errorf("got %+v, %v", m, ok)
	}
}

func TestProbedSourceStillMatchesThroughItsItemPath(t *testing.T) {
	s := indexSources([]Item{{
		ID: "main", Path: "/media/" + testRel,
		Sources: []Source{{ID: "main", Path: testURL}, {ID: "alt", Path: "http://gw/s/t/5/2"}},
	}})
	m, ok := s.find(media.File{MessageID: 1, ChannelID: 5, RelPath: testRel})
	if !ok || m.sourceID != "main" || !m.byPath {
		t.Errorf("got %+v, %v", m, ok)
	}
	// The other version shares the item, but the item's path belongs to the main source.
	m, ok = s.find(media.File{MessageID: 2, ChannelID: 5, RelPath: "Movies/Heat (1995)/Heat (1995) - 720p.strm"})
	if !ok || m.sourceID != "alt" || m.byPath {
		t.Errorf("got %+v, %v", m, ok)
	}
}

type fakeResolver struct {
	calls  atomic.Int64
	result subtitles.Result
	err    error
	onCall func(subtitles.Meta)
}

func (r *fakeResolver) Resolve(_ context.Context, m subtitles.Meta) (subtitles.Result, error) {
	r.calls.Add(1)
	if r.onCall != nil {
		r.onCall(m)
	}
	return r.result, r.err
}

// The whole point of the order: an upload after the first PlaybackInfo stays invisible.
func TestSubtitleIsUploadedBeforeTheFirstPlaybackInfo(t *testing.T) {
	win := &fakeWindow{}
	var events []string
	var mu sync.Mutex
	note := func(e string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	}
	srv := &fakeServer{
		streams: 3,
		items: []Item{{
			ID: "new", Path: "/media/" + testRel, Type: "Episode", SeriesID: "show", SeriesName: "Heat",
			Season: 1, Episode: 2,
		}},
		onProbe:    func(string) { note("probe"); win.reads.Add(2) },
		onUpload:   func(string) { note("upload") },
		seriesIMDb: "tt42",
	}
	res := &fakeResolver{result: subtitles.Result{Outcome: subtitles.OutcomeOnline, Text: "x", Format: "srt"}}
	var got subtitles.Meta
	res.onCall = func(m subtitles.Meta) { got = m }
	p, cat := setupProber(t, srv)
	p.window, p.Subtitles, p.SubtitleLanguage = win, res, "deu"
	p.StreamURL = func(id int) string { return "http://gw/" + strconv.Itoa(id) }
	if err := p.Run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if strings.Join(events, ",") != "upload,probe" {
		t.Errorf("events = %v", events)
	}
	if got := srv.uploadLang.Load(); got != "deu" {
		t.Errorf("uploaded as %v", got)
	}
	if got.IMDb != "tt42" || got.Season != 1 || got.Episode != 2 || got.Title != "Heat" || got.URL != "http://gw/1" {
		t.Errorf("meta = %+v", got)
	}
	f, err := cat.Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if f.SubtitleOutcome != subtitles.OutcomeOnline || f.DurationSeconds != 60 || f.ProbedAt.IsZero() {
		t.Errorf("stored %+v", f)
	}
	if p2, _ := cat.Pending(context.Background()); len(p2) != 0 {
		t.Error("work left after a full run")
	}
}

func TestSubtitleFailureDoesNotBlockTheProbeAndIsRetried(t *testing.T) {
	win := &fakeWindow{}
	srv := &fakeServer{
		streams: 3,
		items:   []Item{{ID: "new", Path: "/media/" + testRel}},
		onProbe: func(string) { win.reads.Add(1) },
	}
	res := &fakeResolver{err: errors.New("sub-plus is down")}
	p, cat := setupProber(t, srv)
	p.window, p.Subtitles = win, res
	if err := p.Run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	f, err := cat.Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if f.ProbedAt.IsZero() {
		t.Error("the video was not probed")
	}
	if f.SubtitleOutcome != "" {
		t.Errorf("outcome = %q, want empty so it is retried", f.SubtitleOutcome)
	}
	res.err, res.result = nil, subtitles.Result{Outcome: subtitles.OutcomeEmbedded}
	if err := p.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	f, _ = cat.Get(context.Background(), 1)
	if f.SubtitleOutcome != subtitles.OutcomeEmbedded {
		t.Errorf("retry outcome = %q", f.SubtitleOutcome)
	}
	if srv.uploads.Load() != 0 {
		t.Error("an embedded track must not cause an upload")
	}
}

func TestOldItemWithSameURLGetsNoSubtitle(t *testing.T) {
	win := &fakeWindow{}
	srv := &fakeServer{
		streams: 3,
		items:   []Item{{ID: "old", Sources: []Source{{ID: "old", Path: testURL}}}},
	}
	res := &fakeResolver{result: subtitles.Result{Outcome: subtitles.OutcomeOnline, Text: "x", Format: "srt"}}
	p, _ := setupProber(t, srv)
	p.window, p.Subtitles = win, res
	if err := p.Run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if res.calls.Load() != 0 || srv.uploads.Load() != 0 {
		t.Errorf("resolved %d times, uploaded %d times", res.calls.Load(), srv.uploads.Load())
	}
}

// Real case: with Persian metadata, Street Fighter is called مبارز خیابانی. Treating that as a
// Persian film meant it never got a subtitle.
func TestSubtitleLookupUsesTheChannelNameNotTheTranslatedTitle(t *testing.T) {
	p := &Prober{}
	f := media.File{MessageID: 1, FileName: "FilmPokVip.Street.Fighter.1994.720p.BluRay.mp4"}
	m := p.meta(context.Background(), f, Item{Name: "مبارز خیابانی", Year: 1990, Type: "Movie"}, "u")
	if m.Title != "FilmPokVip Street Fighter" || m.Year != 1994 {
		t.Errorf("title %q, year %d", m.Title, m.Year)
	}
	m = p.meta(context.Background(), media.File{MessageID: 2}, Item{Name: "Heat", Year: 1995, Type: "Movie"}, "u")
	if m.Title != "Heat" || m.Year != 1995 {
		t.Errorf("with no usable name: title %q, year %d", m.Title, m.Year)
	}
}
