package subtitles

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
)

const persianLine = "این یک زیرنویس آزمایشی است"

func TestDecideFollowsTheOrder(t *testing.T) {
	sub := func(code string) []Track { return []Track{{Kind: "subtitle", Language: code}} }
	audio := func(code string) []Track { return []Track{{Kind: "audio", Language: code}} }
	tests := []struct {
		name    string
		in      Input
		outcome string
		fetch   bool
	}{
		{name: "track in the language", in: Input{Language: "de", Tracks: sub("de"), Title: "Heat", Online: true}, outcome: OutcomeEmbedded},
		{name: "track in another language looks online", in: Input{Language: "de", Tracks: sub("fa"), Title: "Heat", Online: true}, fetch: true},
		{name: "same file for a persian user", in: Input{Language: "fa", Tracks: sub("fa"), Title: "Heat", Online: true}, outcome: OutcomeEmbedded},
		{name: "mis-tagged track found by sniff", in: Input{Language: "fa", Tracks: sub("en"), Sniffed: true, Title: "Heat", Online: true}, outcome: OutcomeEmbedded},
		{name: "track title names the language", in: Input{Language: "de", Tracks: []Track{{Kind: "subtitle", Title: "Deutsch"}}, Online: true}, outcome: OutcomeEmbedded},
		{name: "a subtitle track beats a hardsub word", in: Input{Language: "de", Tracks: sub("de"), FileName: "Heat HardSub", Online: true}, outcome: OutcomeEmbedded},
		{name: "hardsub in file name", in: Input{Language: "de", FileName: "Heat.2020.HardSub.mkv", Title: "Heat", Online: true}, outcome: OutcomeHardSub},
		{name: "hdcam", in: Input{Language: "en", FileName: "Heat.HDCAM.mkv", Title: "Heat", Online: true}, outcome: OutcomeHardSub},
		{name: "persian burn-in word for a persian user", in: Input{Language: "fa", Caption: "زیرنویس چسبیده", Title: "Heat", Online: true}, outcome: OutcomeHardSub},
		{name: "persian burn-in word means nothing to others", in: Input{Language: "de", Caption: "زیرنویس چسبیده", Title: "Heat", Online: true}, fetch: true},
		{name: "audio in the language", in: Input{Language: "en", Tracks: audio("en"), Title: "Heat", Online: true}, outcome: OutcomeAudio},
		{name: "audio in another language looks online", in: Input{Language: "en", Tracks: audio("fa"), Title: "Heat", Online: true}, fetch: true},
		{name: "title in the script of the language", in: Input{Language: "ru", Title: "Сталкер", Online: true}, outcome: OutcomeTitle},
		{name: "title in another script looks online", in: Input{Language: "de", Title: "Сталкер", Online: true}, fetch: true},
		{name: "plain file looks online", in: Input{Language: "fr", FileName: "Heat.1995.1080p.mkv", Title: "Heat", Online: true}, fetch: true},
		{name: "online turned off", in: Input{Language: "fr", Title: "Heat"}, outcome: OutcomeDisabled},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := Decide(tc.in)
			if d.Outcome != tc.outcome || d.Fetch != tc.fetch {
				t.Errorf("got %+v", d)
			}
		})
	}
}

func TestParseProbe(t *testing.T) {
	raw := `{"format":{"duration":"6120.5"},"streams":[
		{"index":0,"codec_type":"video","codec_name":"h264"},
		{"index":1,"codec_type":"audio","codec_name":"aac","tags":{"language":"eng"}},
		{"index":2,"codec_type":"subtitle","codec_name":"subrip","tags":{"LANGUAGE":"ger","TITLE":"Forced"}},
		{"index":3,"codec_type":"subtitle","codec_name":"ass","tags":{"language":"und"}}]}`
	info, err := parseProbe([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	tracks := info.Tracks
	if info.Seconds != 6120 {
		t.Errorf("seconds = %d", info.Seconds)
	}
	if len(tracks) != 3 {
		t.Fatalf("tracks = %+v", tracks)
	}
	if !HasTrack(tracks, "subtitle", "de") || !HasTrack(tracks, "audio", "en") {
		t.Error("tracks not found by normalized language")
	}
	if HasTrack(tracks, "subtitle", "en") || HasTrack(tracks, "audio", "de") {
		t.Error("kinds were mixed up")
	}
	if tracks[2].Language != "" {
		t.Errorf("und kept as %q", tracks[2].Language)
	}
}

func TestSniffCandidates(t *testing.T) {
	got := sniffCandidates([]Track{
		{Kind: "subtitle", Index: 1, Codec: "subrip", Language: "en"},
		{Kind: "subtitle", Index: 2, Codec: "subrip", Language: "de"},
		{Kind: "subtitle", Index: 3, Codec: "hdmv_pgs_subtitle", Language: ""},
		{Kind: "subtitle", Index: 4, Codec: "ass", Language: ""},
		{Kind: "audio", Index: 5, Codec: "aac", Language: ""},
	})
	if len(got) != 2 || got[0].Index != 1 || got[1].Index != 4 {
		t.Errorf("candidates = %+v", got)
	}
}

func TestUnknownLanguagesAreNeverSentToSubPlus(t *testing.T) {
	if _, ok := NewSubPlus("ko"); ok {
		t.Error("a language the site does not list was accepted")
	}
	c, ok := NewSubPlus("de")
	if !ok || c.language != "german" {
		t.Fatalf("got %+v, %v", c, ok)
	}
}

func zipOf(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, data := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractDecodesAndStripsTheAd(t *testing.T) {
	ad := "0\n00:00:01,000 --> 00:00:10,000\nساب پلاس تقدیم می‌کند\n\n"
	body := "1\n00:00:11,000 --> 00:00:12,000\n" + persianLine + "\n"
	cp1256, err := charmap.Windows1256.NewEncoder().String("1\n00:00:11,000 --> 00:00:12,000\nسلام دنيا\n")
	if err != nil {
		t.Fatal(err)
	}
	utf16, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().String(body)
	if err != nil {
		t.Fatal(err)
	}
	files, err := ExtractSubs(zipOf(t, map[string][]byte{
		"utf8.srt":    []byte("\xEF\xBB\xBF" + ad + body),
		"ansi.srt":    []byte(cp1256),
		"utf16.srt":   []byte(utf16),
		"style.ass":   []byte("[Script Info]\n"),
		"readme.txt":  []byte("ignored"),
		"../evil.nfo": []byte("ignored"),
	}), "fa")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 {
		t.Fatalf("files = %d", len(files))
	}
	for _, f := range files {
		switch f.Name {
		case "utf8.srt":
			if strings.Contains(f.Text, "ساب پلاس") || !strings.Contains(f.Text, persianLine) {
				t.Errorf("utf8: %q", f.Text)
			}
		case "ansi.srt":
			if !strings.Contains(f.Text, "سلام دنيا") {
				t.Errorf("cp1256 not decoded: %q", f.Text)
			}
		case "utf16.srt":
			if !strings.Contains(f.Text, persianLine) || !strings.Contains(f.Text, "-->") {
				t.Errorf("utf16 not decoded: %q", f.Text)
			}
		}
	}
}

func TestLegacyFileIsDecodedForTheLanguage(t *testing.T) {
	cyrillic, err := charmap.Windows1251.NewEncoder().String("1\n00:00:01,000 --> 00:00:02,000\nПривет\n")
	if err != nil {
		t.Fatal(err)
	}
	files, err := ExtractSubs(zipOf(t, map[string][]byte{"a.srt": []byte(cyrillic)}), "ru")
	if err != nil || len(files) != 1 || !strings.Contains(files[0].Text, "Привет") {
		t.Fatalf("files %+v, %v", files, err)
	}
}

func TestStripAdKeepsARealFirstCue(t *testing.T) {
	srt := "1\n00:00:01,000 --> 00:00:02,000\nHello\n\n2\n00:00:03,000 --> 00:00:04,000\nBye\n"
	if got := stripAdCue(srt); got != srt {
		t.Errorf("real cue removed: %q", got)
	}
}

func TestPickEpisode(t *testing.T) {
	files := []SubFile{
		{Name: "Show.S01E01.srt"}, {Name: "Show.S01E10.srt"}, {Name: "Show.S02E10.srt"},
	}
	if got := pickEpisode(files, 1, 10); got == nil || got.Name != "Show.S01E10.srt" {
		t.Errorf("got %+v", got)
	}
	if got := pickEpisode(files, 1, 5); got != nil {
		t.Errorf("season pack without the episode must return nil, got %+v", got)
	}
	if got := pickEpisode(files[:1], 1, 5); got == nil {
		t.Error("a single file is taken as it is")
	}
}

func TestPickMoviePrefersFullDialogueAndRelease(t *testing.T) {
	files := []SubFile{
		{Name: "Heat.1995.WEB-DL.Forced.srt"},
		{Name: "Heat.1995.BluRay.SDH.srt"},
		{Name: "Heat.1995.WEB-DL.srt"},
		{Name: "Heat.1995.BluRay.YIFY.srt"},
	}
	if got := pickMovie(files, "Heat.1995.1080p.BluRay.YIFY.mkv"); got == nil || got.Name != "Heat.1995.BluRay.YIFY.srt" {
		t.Errorf("got %+v", got)
	}
}

type fakeProvider struct {
	searches []string
	results  map[string][]Pack
	files    map[string][]SubFile
	dlErr    error
	dls      int
}

func (f *fakeProvider) Search(_ context.Context, q string) ([]Pack, error) {
	f.searches = append(f.searches, q)
	return f.results[q], nil
}

func (f *fakeProvider) Download(_ context.Context, tag string) ([]SubFile, error) {
	f.dls++
	if f.dlErr != nil {
		return nil, f.dlErr
	}
	return f.files[tag], nil
}

func TestFindUsesIMDbAndSkipsEmptyPacks(t *testing.T) {
	p := &fakeProvider{
		results: map[string][]Pack{"tt123": {
			{Tag: "empty", Title: "Heat (1995)", IMDb: "tt123", Releases: []string{"Heat.1995.1080p.BluRay"}},
			{Tag: "good", Title: "Heat (1995)", IMDb: "tt123", Releases: []string{"Heat.1995.WEB"}},
		}},
		files: map[string][]SubFile{"good": {{Name: "Heat.srt", Ext: ".srt", Text: "x"}}},
	}
	got, err := Find(context.Background(), p, Query{IMDb: "TT123", Title: "Heat", FileName: "Heat.1995.1080p.BluRay.mkv"})
	if err != nil || got == nil || got.Name != "Heat.srt" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if len(p.searches) != 1 || p.dls != 2 {
		t.Errorf("searches %v, downloads %d", p.searches, p.dls)
	}
}

// Real case: sub-plus answered a search for Street Fighter with White Fang 2 and Party.
func TestFindRejectsOtherFilmsFromAFuzzyIDSearch(t *testing.T) {
	p := &fakeProvider{
		results: map[string][]Pack{"tt0111301": {
			{Tag: "other", Title: "White Fang 2 (1994)", IMDb: "tt0111701"},
			{Tag: "other2", Title: "Party (1996)", IMDb: "tt0117301"},
		}},
		files: map[string][]SubFile{"other": {{Name: "White.Fang.srt"}}, "other2": {{Name: "Party.srt"}}},
	}
	got, err := Find(context.Background(), p, Query{IMDb: "tt0111301", Title: "Street Fighter", Year: 1994, FileName: "x.mkv"})
	if err != nil || got != nil {
		t.Fatalf("another film was accepted: %+v, %v", got, err)
	}
	if p.dls != 0 {
		t.Errorf("downloaded %d packs of other films", p.dls)
	}
}

func srtEndingAt(minutes int) string {
	return fmt.Sprintf("1\n00:00:01,000 --> 00:00:02,000\nx\n\n2\n%02d:%02d:00,000 --> %02d:%02d:02,000\ny\n",
		minutes/60, minutes%60, minutes/60, minutes%60)
}

// Real case: one English pack of a 102 minute film stopped at minute 59.
func TestFindSkipsSubtitleThatDoesNotFitTheRuntime(t *testing.T) {
	p := &fakeProvider{
		results: map[string][]Pack{"tt1": {
			{Tag: "partial", Title: "Heat (1995)", IMDb: "tt1", Releases: []string{"Heat.BluRay.x264"}},
			{Tag: "cut", Title: "Heat (1995)", IMDb: "tt1", Releases: []string{"Heat.BluRay"}},
			{Tag: "good", Title: "Heat (1995)", IMDb: "tt1"},
		}},
		files: map[string][]SubFile{
			"partial": {{Name: "a.srt", Ext: ".srt", Text: srtEndingAt(59)}},
			"cut":     {{Name: "b.srt", Ext: ".srt", Text: srtEndingAt(140)}},
			"good":    {{Name: "c.srt", Ext: ".srt", Text: srtEndingAt(101)}},
		},
	}
	got, err := Find(context.Background(), p, Query{IMDb: "tt1", FileName: "Heat.BluRay.x264.mkv", Seconds: 102 * 60})
	if err != nil || got == nil || got.Name != "c.srt" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestFitsRuntime(t *testing.T) {
	srt := SubFile{Ext: ".srt", Text: srtEndingAt(100)}
	if !fitsRuntime(srt, 0) {
		t.Error("an unknown running time should pass")
	}
	if !fitsRuntime(SubFile{Ext: ".ass", Text: "no srt timing"}, 6000) {
		t.Error("other formats should pass")
	}
	if fitsRuntime(SubFile{Ext: ".srt", Text: "no cues"}, 6000) {
		t.Error("an .srt with no cues should fail")
	}
	if !fitsRuntime(srt, 102*60) {
		t.Error("a normal end should pass")
	}
}

func TestFindByTitleRequiresExactTitleAndYear(t *testing.T) {
	p := &fakeProvider{
		results: map[string][]Pack{"Heat": {
			{Tag: "other", Title: "Heat Wave (1995)", Year: "1995"},
			{Tag: "year", Title: "Heat (1986)", Year: "1986"},
		}},
		files: map[string][]SubFile{"other": {{Name: "a.srt"}}, "year": {{Name: "b.srt"}}},
	}
	got, err := Find(context.Background(), p, Query{Title: "Heat", Year: 1995, FileName: "Heat.mkv"})
	if err != nil || got != nil {
		t.Fatalf("wrong film accepted: %+v, %v", got, err)
	}
}

func TestFindSeriesFiltersSeasonAndKind(t *testing.T) {
	p := &fakeProvider{
		results: map[string][]Pack{"tt9": {
			{Tag: "movie", Title: "Hill House (2018)", IMDb: "tt9"},
			{Tag: "s2", Title: "Hill House - Second Season (2020)", IMDb: "tt9", Series: true},
			{Tag: "s1", Title: "Hill House - First Season (2018)", IMDb: "tt9", Series: true},
		}},
		files: map[string][]SubFile{
			"s1": {{Name: "Hill.House.S01E09.srt"}, {Name: "Hill.House.S01E10.srt", Text: "right"}},
			"s2": {{Name: "Hill.House.S02E10.srt", Text: "wrong"}},
		},
	}
	got, err := Find(context.Background(), p, Query{IMDb: "tt9", Season: 1, Episode: 10, FileName: "x.mkv"})
	if err != nil || got == nil || got.Text != "right" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestFindReportsDownloadFailure(t *testing.T) {
	boom := errors.New("server down")
	p := &fakeProvider{results: map[string][]Pack{"tt1": {{Tag: "a", Title: "Heat (1995)", IMDb: "tt1"}}}, dlErr: boom}
	if _, err := Find(context.Background(), p, Query{IMDb: "tt1"}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestValidDownloadURL(t *testing.T) {
	for link, want := range map[string]bool{
		"http://sub-plus.ir/download/news-2466179.zip":                      true,
		"http://sub-plus.ir//home2/subplusi/public_html/modules/temp/-.zip": false,
		"http://sub-plus.ir/other/a.zip":                                    false,
		"http://sub-plus.ir/download/a.exe":                                 false,
		"file:///etc/passwd":                                                false,
		"":                                                                  false,
	} {
		if got := validDownloadURL(link); got != want {
			t.Errorf("%q = %v", link, got)
		}
	}
}

func TestSubPlusClient(t *testing.T) {
	zipBytes := zipOf(t, map[string][]byte{"a.srt": []byte("1\n00:00:01,000 --> 00:00:02,000\nx\n")})
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/download/a.zip":
			_, _ = w.Write(zipBytes)
		case r.FormValue("q") == "tt1" && r.FormValue("l") == "german":
			_, _ = w.Write([]byte(`{"ok":true,"count":1,"result":[{"title":"Heat (1995)","tag":"T","series":false,"info":"Rel.One\nRel.Two","imdb":"tt1","year":"1995"}]}`))
		case r.FormValue("dl") == "T":
			_, _ = w.Write([]byte(`{"ok":true,"download":"` + srv.URL + `/download/a.zip"}`))
		case r.FormValue("dl") == "bad":
			_, _ = w.Write([]byte(`{"ok":true,"download":"` + srv.URL + `//home2/x/-.zip"}`))
		default:
			_, _ = w.Write([]byte(`{"ok":false,"description":"Bad Request"}`))
		}
	}))
	defer srv.Close()
	c := &SubPlus{http: srv.Client(), api: srv.URL + "/api.php", code: "de", language: "german"}
	ctx := context.Background()

	packs, err := c.Search(ctx, "tt1")
	if err != nil || len(packs) != 1 || len(packs[0].Releases) != 2 {
		t.Fatalf("packs %+v, %v", packs, err)
	}
	files, err := c.Download(ctx, "T")
	if err != nil || len(files) != 1 {
		t.Fatalf("files %+v, %v", files, err)
	}
	if _, err := c.Download(ctx, "bad"); err == nil {
		t.Error("path-disclosure link accepted")
	}
	if _, err := c.Search(ctx, "unknown"); err == nil {
		t.Error("upstream error ignored")
	}
}
