package naming

import (
	"strings"
	"testing"

	"github.com/Aryan-mor/jellyfin-telegram/internal/sign"
)

func TestParseAndRelPath(t *testing.T) {
	tests := []struct {
		name, file, caption string
		want                string
		matchable           bool
	}{
		{"scene movie", "Inception.2010.1080p.BluRay.x264-GROUP.mkv", "",
			"Movies/Inception (2010)/Inception (2010) - 1080p.strm", true},
		{"title that is a year", "2012.2009.720p.mkv", "",
			"Movies/2012 (2009)/2012 (2009) - 720p.strm", true},
		{"year in parentheses", "1917 (2019).mkv", "",
			"Movies/1917 (2019)/1917 (2019).strm", true},
		{"lone number stays a title", "1917.mkv", "",
			"Movies/1917/1917.strm", false},
		{"episode dots", "Breaking.Bad.S01E02.720p.WEB-DL.mkv", "",
			"Shows/Breaking Bad/Season 01/Breaking Bad S01E02.strm", true},
		{"episode underscores", "Dark_Show_2017_S02E10_1080p.mp4", "",
			"Shows/Dark Show (2017)/Season 02/Dark Show S02E10.strm", true},
		{"real: handle joined to the title by underscores",
			"@active_film_The_Haunting_of_Hill_House_10_2018_season_1_720p.mkv",
			"📺 The Haunting of Hill House (2018)\nS01E10\n⏱ 1:11:02\n🔗 tmdb 72844",
			"Shows/The Haunting of Hill House (2018) [tmdbid-72844]/Season 01/The Haunting of Hill House S01E10.strm", true},
		{"real: site tag in the file name",
			"FilmPokVip.Street.Fighter.1994.720p.BluRay.x264.AAC.mp4",
			"🎬 Street Fighter (1994)\n⏱ 1:42:00\n🔗 tmdb 11667",
			"Movies/Street Fighter (1994) [tmdbid-11667]/Street Fighter (1994) [tmdbid-11667] - 720p.strm", true},
		{"handle with underscores and no caption keeps the title",
			"@my_channel_Heat_1995.mkv", "",
			"Movies/channel Heat (1995)/channel Heat (1995).strm", true},
		{"known limit: any caption shaped like Title (Year) wins",
			"Heat.1995.mkv", "I watched this in (2020)",
			"Movies/I watched this in (2020)/I watched this in (2020).strm", true},
		{"episode x form", "Lost 1x05.avi", "",
			"Shows/Lost/Season 01/Lost S01E05.strm", true},
		{"zero padded episode is season 1", "Muhtemel.Ask_020_720p(@EADizi).mkv", "",
			"Shows/Muhtemel Ask/Season 01/Muhtemel Ask S01E20.strm", true},
		{"glued episode number", "Evlilik.Guzeldir.E009.HardSub.540p.(@EADizi).mp4", "",
			"Shows/Evlilik Guzeldir/Season 01/Evlilik Guzeldir S01E09.strm", true},
		{"hyphens in the show name", "Gunesin-Dogdugu-Yer.E008.HardSub.720p.mkv", "",
			"Shows/Gunesin Dogdugu Yer/Season 01/Gunesin Dogdugu Yer S01E08.strm", true},
		{"turkish bölüm", "Halef: Köklerin Çağrısı 41. Bölüm.mp4", "",
			"Shows/Halef - Köklerin Çağrısı/Season 01/Halef - Köklerin Çağrısı S01E41.strm", true},
		{"bare zero is not an episode", "020.mkv", "",
			"Movies/020/020.strm", false},
		{"persian title", "جدایی نادر از سیمین 2011.mkv", "",
			"Movies/جدایی نادر از سیمین (2011)/جدایی نادر از سیمین (2011).strm", true},
		{"generic name falls back to caption", "video_2024.mp4", "🎬 Dune (2021)\n1080p",
			"Movies/Dune (2021)/Dune (2021) - 1080p.strm", true},
		{"tmdb id in caption", "Dune.2021.mkv", "tmdb 438631",
			"Movies/Dune (2021) [tmdbid-438631]/Dune (2021) [tmdbid-438631].strm", true},
		{"tmdb url in caption", "Dune.2021.mkv", "https://www.themoviedb.org/movie/438631-dune",
			"Movies/Dune (2021) [tmdbid-438631]/Dune (2021) [tmdbid-438631].strm", true},
		{"tmdb episode url", "Muhtemel.Ask_020_720p.mkv", "https://www.themoviedb.org/tv/322499/season/1/episode/20",
			"Shows/Muhtemel Ask [tmdbid-322499]/Season 01/Muhtemel Ask S01E20.strm", true},
		{"tmdb episode url with a slug", "Muhtemel.Ask_020_720p.mkv", "https://www.themoviedb.org/tv/322499-muhtemel-ask/season/1/episode/20",
			"Shows/Muhtemel Ask [tmdbid-322499]/Season 01/Muhtemel Ask S01E20.strm", true},
		{"episode only in caption", "Severance.mkv", "Severance\nS02E04",
			"Shows/Severance/Season 02/Severance S02E04.strm", true},
		{"handles and tags removed", "@somechannel Heat [Director Cut] 1995 BluRay.mkv", "",
			"Movies/Heat (1995)/Heat (1995).strm", true},
		{"year from a caption naming the same title", "Dune.mkv", "Dune (2021)",
			"Movies/Dune (2021)/Dune (2021).strm", true},
		{"year from an unrelated caption is ignored", "Family Trip.mp4", "Summer 2020 with friends",
			"Movies/Family Trip/Family Trip.strm", false},
		{"generic name without caption", "video.mp4", "",
			"Movies/Telegram 42/Telegram 42.strm", false},
		{"numeric name prefers the caption", "12345.mp4", "Heat (1995)",
			"Movies/Heat (1995)/Heat (1995).strm", true},
		{"no usable name", "", "",
			"Movies/Telegram 42/Telegram 42.strm", false},
		{"unsafe characters", `Who? What: Why (2001).mkv`, "",
			"Movies/Who What - Why (2001)/Who What - Why (2001).strm", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := Parse(tc.file, tc.caption)
			if got := p.RelPath(42, ""); got != tc.want {
				t.Errorf("RelPath = %q, want %q", got, tc.want)
			}
			if p.Matchable() != tc.matchable {
				t.Errorf("Matchable = %v, want %v", p.Matchable(), tc.matchable)
			}
		})
	}
}

func TestCaptionSuppliesSeasonEpisodeAndTitle(t *testing.T) {
	caption := strings.Join([]string{
		".📹سریال : «  ازدواج زیباست  »",
		"📹 Evlilik Guzeldir",
		"📂فصل اول",
		"🔸قسمت  9",
		"🟢کیفیت  540p",
		"✔️زیرنویس چسبیده فارسی  TV24",
		"🔸بدون سانسور",
		"",
		"@EvlilikGuzeldir_ir",
	}, "\n")
	p := Parse("video.mp4", caption)
	if !p.Episode || p.Season != 1 || p.Number != 9 || p.Title != "Evlilik Guzeldir" {
		t.Fatalf("parsed %+v", p)
	}
	want := "Shows/Evlilik Guzeldir/Season 01/Evlilik Guzeldir S01E09.strm"
	if got := p.RelPath(42, ""); got != want {
		t.Fatalf("RelPath = %q", got)
	}
	// A season written in the file name wins over the caption.
	p = Parse("Show.S02E04.mkv", "فصل اول\nقسمت 9\nEvlilik Guzeldir")
	if p.Season != 2 || p.Number != 4 {
		t.Fatalf("caption overrode S02E04: %+v", p)
	}
}

func TestHintReady(t *testing.T) {
	if !HintReady("https://www.themoviedb.org/tv/322499/season/1/episode/20") {
		t.Fatal("episode link")
	}
	if !HintReady("tmdb 603") {
		t.Fatal("film id")
	}
	if HintReady("https://www.themoviedb.org/tv/322499") {
		t.Fatal("a series link with no episode should wait")
	}
	if HintReady("hello") {
		t.Fatal("plain text")
	}
}

func TestRelPathSuffixSeparatesVersions(t *testing.T) {
	p := Parse("Heat.1995.1080p.mkv", "")
	want := "Movies/Heat (1995)/Heat (1995) - 1080p - tg7.strm"
	if got := p.RelPath(7, "tg7"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSanitize(t *testing.T) {
	tests := map[string]string{
		"CON":                      "_CON",
		"aux.strm":                 "_aux.strm",
		"ends with dot.":           "ends with dot",
		"a<b>c":                    "abc",
		"...":                      "_",
		"tab\there":                "tabhere",
		"  many   spaces  ":        "many spaces",
		`back\slash/forward|pipe*`: "backslashforwardpipe",
	}
	for in, want := range tests {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
	long := Sanitize(strings.Repeat("x", 300) + ".strm")
	if len([]rune(long)) != maxSegmentRunes || !strings.HasSuffix(long, ".strm") {
		t.Errorf("long name not capped with its extension kept: %d runes, %q", len([]rune(long)), long[len(long)-8:])
	}
}

func TestUploadNameRoundTrip(t *testing.T) {
	samples := []Parsed{
		{Title: "Sintel", Year: 2010, TMDBID: 45745, Label: "1080p"},
		{Title: "千と千尋の神隠し", Year: 2001, TMDBID: 129, Label: "1080p"},
		{Title: "Das Leben der Anderen", Year: 2006, TMDBID: 582, Label: "720p"},
		{Title: "جدایی نادر از سیمین", Year: 2011, TMDBID: 45269},
		{Title: "Breaking Bad", Year: 2008, TMDBID: 1396, Episode: true, Season: 1, Number: 2},
		{Title: "Dark", Year: 2017, Episode: true, Season: 2, Number: 10, Label: "1080p"},
	}
	for _, p := range samples {
		name, caption := UploadName(p, ".mkv")
		got := Parse(name, caption)
		if got.Title != p.Title || got.Year != p.Year || got.TMDBID != p.TMDBID || got.Episode != p.Episode || got.Season != p.Season || got.Number != p.Number {
			t.Errorf("Parse(%q, %q) = %+v", name, caption, got)
		}
		if p.Episode && got.Label != "" {
			t.Errorf("episode name kept a label: %q", name)
		}
		if !p.Episode && got.Label != p.Label {
			t.Errorf("label: got %q want %q from %q", got.Label, p.Label, name)
		}
		if caption != p.Title && !strings.Contains(caption, p.Title) {
			t.Errorf("caption %q", caption)
		}
	}
}

func TestParsePath(t *testing.T) {
	cases := []struct {
		rel     string
		title   string
		year    int
		tmdb    int
		episode bool
		season  int
		number  int
		label   string
	}{
		{"Sintel (2010)/Sintel (2010).mkv", "Sintel", 2010, 0, false, 0, 0, ""},
		{"Sintel.2010.1080p.BluRay.x264.mkv", "Sintel", 2010, 0, false, 0, 0, "1080p"},
		{"Action/Die Hard 1988 720p.mp4", "Die Hard", 1988, 0, false, 0, 0, "720p"},
		{"Heat (1995) [tmdbid-949]/Heat.mkv", "Heat", 1995, 949, false, 0, 0, ""},
		{"Fringe (2008)/Season 01/Fringe - S01E02 - The Same Old Story.mkv", "Fringe", 2008, 0, true, 1, 2, ""},
		{"Fringe (2008)/Season 2/05.mkv", "Fringe", 2008, 0, true, 2, 5, ""},
		{"Fringe (2008)/S03/Episode 7.mkv", "Fringe", 2008, 0, true, 3, 7, ""},
		{"Anime/Naruto [tmdbid-46260]/Season 1/Naruto.S01E10.mkv", "Naruto", 0, 46260, true, 1, 10, ""},
		{"Show Name/Specials/E01.mkv", "Show Name", 0, 0, true, 0, 1, ""},
		{"Dune.Part.Two.2024.mkv", "Dune Part Two", 2024, 0, false, 0, 0, ""},
		{"Some Show S02E03.mkv", "Some Show", 0, 0, true, 2, 3, ""},
		{"video_2024.mkv", "", 0, 0, false, 0, 0, ""},
		{`Fringe (2008)\Season 01\Fringe S01E01.mkv`, "Fringe", 2008, 0, true, 1, 1, ""},
	}
	for _, c := range cases {
		got := ParsePath(c.rel)
		if got.Title != c.title || got.Year != c.year || got.TMDBID != c.tmdb || got.Episode != c.episode || got.Season != c.season || got.Number != c.number || got.Label != c.label {
			t.Errorf("%s\n got  %+v\n want title=%q year=%d tmdb=%d episode=%v season=%d number=%d label=%q", c.rel, got, c.title, c.year, c.tmdb, c.episode, c.season, c.number, c.label)
		}
	}
}

func TestSignedLineWinsAndOldCaptionsStay(t *testing.T) {
	old := Parse("Inception.2010.1080p.mkv", "")
	if old.Title != "Inception" || old.Year != 2010 || old.TMDBID != 0 {
		t.Fatalf("old caption changed: %+v", old)
	}
	key, err := sign.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	line := key.Sign(sign.Meta{Kind: "m", TMDB: 603, IMDb: "tt0133093", Quality: "1080p",
		Soft: []sign.Track{{Lang: "fa", Codec: "srt"}, {Lang: "en", Codec: "ass"}}, Hard: "none", DocID: 1, Size: 2})
	caption := "The Matrix (1999)\n🎬 Movie · TMDB 603\n" + line
	p := Parse("video.mkv", caption)
	if p.Title != "The Matrix" || p.Year != 1999 || p.TMDBID != 603 || p.Label != "1080p" || p.Episode {
		t.Fatalf("signed movie: %+v", p)
	}
	if LookupTag(p) != "#tm_m_603" {
		t.Fatal(LookupTag(p))
	}
	ep := key.Sign(sign.Meta{Kind: "e", TMDB: 1396, IMDb: "tt0903747", Quality: "1080p", Season: 1, Episode: 2,
		Soft: []sign.Track{{Lang: "en", Codec: "srt"}}, Hard: "none", DocID: 1, Size: 2})
	got := Parse("clip.mkv", "Breaking Bad (2008)\n"+ep)
	if !got.Episode || got.Season != 1 || got.Number != 2 || got.TMDBID != 1396 {
		t.Fatalf("signed episode: %+v", got)
	}
	if LookupTag(got) != "#tm_tv_1396_1_2" {
		t.Fatal(LookupTag(got))
	}
	head := Caption(Head{Title: "The Matrix", Year: 1999, TMDB: 603, IMDb: "tt0133093", Label: "1080p", Soft: []string{"fa (srt)", "en (ass)"}, Hard: "none"})
	if !strings.Contains(head, "The Matrix (1999)") || strings.Contains(head, "#") || !strings.Contains(head, "fa (srt)") {
		t.Fatal(head)
	}
	withSource := Caption(Head{Title: "The Matrix", Year: 1999, Source: SourceLine("Movies", "movies")})
	if !strings.HasSuffix(withSource, "\n"+SourceLine("Movies", "movies")) {
		t.Fatalf("source line: %q", withSource)
	}
	if SourceLine("Movies", "movies") != "📡 Source: Movies (@movies)" {
		t.Fatal(SourceLine("Movies", "movies"))
	}
	if SourceLine("Secret", "") != "📡 Source: private channel" || strings.Contains(SourceLine("Secret", ""), "Secret") {
		t.Fatal(SourceLine("Secret", ""))
	}
	if SourceIn("note\n"+SourceLine("Movies", "movies")) != SourceLine("Movies", "movies") || SourceIn("note") != "" {
		t.Fatal("SourceIn")
	}
	if len(head) > 900 {
		t.Fatalf("head is %d characters", len(head))
	}
	shown := ShowTracks([]sign.Track{{Lang: "en", Codec: "srt"}, {Lang: "fa", Codec: "srt"}, {Lang: "de", Codec: "ass"}, {Lang: "fr", Codec: "srt"}, {Lang: "es", Codec: "srt"}, {Lang: "it", Codec: "srt"}, {Lang: "ja", Codec: "srt"}}, "fa")
	if len(shown) != 7 || shown[0] != "fa (srt)" || shown[6] != "+1" {
		t.Fatalf("tracks %v", shown)
	}
	if HardSub("Movie.fa.hardsub.mkv", "") != "fa" {
		t.Fatal(HardSub("Movie.fa.hardsub.mkv", ""))
	}
	if HardSub("Movie.softsub.mkv", "") != "none" {
		t.Fatal("softsub")
	}
	if HardSub("Movie.mkv", "") != "unknown" {
		t.Fatal("unknown")
	}
}

func TestSection(t *testing.T) {
	cases := []struct{ file, caption, mime, want string }{
		{"Inception.2010.mkv", "", "video/x-matroska", SectionMovie},
		{"Lost.S01E02.mkv", "", "video/x-matroska", SectionSeries},
		{"clip.mp4", "birthday", "video/mp4", SectionVideo},
		{"Song.mp3", "", "audio/mpeg", SectionMusic},
		{"voice.ogg", "", "audio/ogg", SectionMusic},
	}
	for _, c := range cases {
		if got := Section(c.file, c.caption, c.mime); got != c.want {
			t.Errorf("Section(%q) = %s, want %s", c.file, got, c.want)
		}
	}
}

// A status and a claim that a bot wrote above a caption are not part of the title.
func TestParseIgnoresTheHeaderAboveACaption(t *testing.T) {
	plain := Parse("video 12.mkv", "The Matrix (1999)")
	withStatus := Parse("video 12.mkv", sign.StatusLine(sign.StatusReview, "")+"\nThe Matrix (1999)")
	if plain.Title != "The Matrix" || plain.Year != 1999 {
		t.Fatalf("plain = %+v", plain)
	}
	if withStatus.Title != plain.Title || withStatus.Year != plain.Year {
		t.Fatalf("a status line changed the title: %+v", withStatus)
	}
	for _, note := range []string{"hardsub", "softsub", "hardsub english"} {
		if HardSub("a.mkv", sign.StatusLine(sign.StatusReady, "")+"\n"+note) != HardSub("a.mkv", note) {
			t.Fatalf("a status line changed what %q says", note)
		}
	}
}
