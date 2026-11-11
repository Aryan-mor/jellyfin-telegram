// Package naming turns a Telegram file name and caption into a Jellyfin library path.
package naming

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Aryan-mor/jellyfin-telegram/internal/lang"
	"github.com/Aryan-mor/jellyfin-telegram/internal/sign"
)

// Parsed is what could be learned from a file name and caption.
type Parsed struct {
	Title   string
	Year    int
	TMDBID  int
	Episode bool
	Season  int
	Number  int
	// Label is a version label such as 1080p, used to separate versions of one title.
	Label string
	// AudioLang and SubLang are set when the tracks are known. LookupTag adds them to the hashtag.
	// Empty means that language is not known, and the hashtag stays without it.
	AudioLang string
	SubLang   string
}

// Matchable reports whether Jellyfin can be expected to find online metadata.
func (p Parsed) Matchable() bool { return p.TMDBID > 0 || p.Year > 0 || p.Episode }

var (
	reTMDBURL = regexp.MustCompile(`(?i)themoviedb\.org/(?:movie|tv)/(\d+)`)
	reTMDBTag = regexp.MustCompile(`(?i)\btmdb(?:id)?[\s:=_-]*(\d+)\b`)
	// A slug may sit between the id and /season, as in /tv/322499-muhtemel-ask/season/1/episode/20.
	reTMDBPage = regexp.MustCompile(`(?i)themoviedb\.org/(movie|tv)/(\d+)[^/\s]*(?:/season/(\d+)/episode/(\d+))?`)
	reEpisode  = regexp.MustCompile(`(?i)\bS(\d{1,2})\s*-?\s*E(\d{1,3})\b`)
	reEpisodeX = regexp.MustCompile(`\b(\d{1,2})x(\d{2,3})\b`)
	// E009 is an episode with no season. The E is glued to the number, unlike a bare 020.
	reEEpisode = regexp.MustCompile(`(?i)\bE(\d{2,3})\b`)
	// "41. Bölüm" is the Turkish way to write an episode number.
	reBolum = regexp.MustCompile(`(?i)\b(\d{1,4})\s+b[oö]l[uü]m\b`)
	// A zero-padded number with no season, as in Show_020_720p. The zero is padding, not season 0.
	reBareEpisode = regexp.MustCompile(`\b0(\d{2})\b`)
	reFaSeason    = regexp.MustCompile(`فصل\s*[:：]?\s*(اول|دوم|سوم|چهارم|پنجم|ششم|هفتم|هشتم|نهم|دهم|\d{1,2})`)
	reFaEpisode   = regexp.MustCompile(`قسمت\s*[:：]?\s*(\d{1,4})`)
	reQuoted      = regexp.MustCompile(`«\s*([^»]+?)\s*»`)
	reYear        = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
	reLabel       = regexp.MustCompile(`(?i)\b(2160p|1440p|1080p|720p|576p|480p|360p|4k)\b`)
	reTag         = regexp.MustCompile(`(?i)\b(2160p|1440p|1080p|720p|576p|480p|360p|4k|uhd|hdr|bluray|blu ray|brrip|bdrip|web dl|webdl|webrip|hdtv|dvdrip|remux|x264|x265|h264|h265|hevc|av1|aac|dts|ac3|10bit|dubbed|softsub|hardsub)\b`)
	reBrackets    = regexp.MustCompile(`\[[^\]]*\]|\{[^}]*\}`)
	// A handle stops at a separator, because names like @chan_The_Movie_2018 use
	// underscores between words and \w would swallow the whole title.
	reHandle = regexp.MustCompile(`@[^\s._-]+`)
	// reCaptionTitle matches a caption line that is exactly "Title (Year)", with any
	// leading emoji. Free text does not match, so it cannot replace a file name.
	reCaptionTitle = regexp.MustCompile(`^[^\p{L}\p{N}]*(.+?)\s*\((19\d{2}|20\d{2})\)\s*$`)
	reExt          = regexp.MustCompile(`(?i)\.(mkv|mp4|m4v|avi|mov|webm|ts|m2ts|wmv|flv|mpg|mpeg)$`)
	reGeneric      = regexp.MustCompile(`(?i)^(video|movie|film|file|document|untitled|vid|telegram)[\s\d_-]*$`)
	reNumeric      = regexp.MustCompile(`^[\s\d_-]*$`)
)

// Parse reads the file name first and falls back to the caption for missing fields.
func Parse(fileName, caption string) Parsed {
	// A status and a claim that a bot wrote above the caption are not part of what a person wrote.
	caption = sign.StripHeader(caption)
	var p Parsed
	p.TMDBID = tmdbID(fileName + "\n" + caption)

	stem := strings.TrimSpace(reExt.ReplaceAllString(fileName, ""))
	firstLine := firstNonEmptyLine(caption)

	// A name like video_2024 says nothing. A bare number may be a title such as 1917,
	// so it is only replaced when the caption offers something better.
	name := stem
	if reGeneric.MatchString(name) || (reNumeric.MatchString(name) && firstLine != "") {
		name = firstLine
	}
	parseInto(&p, name)

	_, _, _, explicit, _ := locateEpisode(normalize(name))
	if !p.Episode {
		if s, e, _, ex, ok := locateEpisode(normalize(caption)); ok {
			p.Episode, p.Season, p.Number = true, s, e
			explicit = ex
		}
	}
	// A channel post often says the season and episode in Persian under the file name.
	// "فصل اول" / "قسمت 9" fills whatever the file name left out. A written S02E04 is kept.
	if cs, ce, ok := faShow(caption); ok {
		if !p.Episode {
			p.Episode, p.Season, p.Number = true, cs, ce
		} else if !explicit && cs > 0 {
			p.Season = cs
		}
	}
	if title := captionTitle(caption); title != "" && (WeakTitle(p.Title) || !hasLatin(p.Title)) {
		p.Title = title
	}
	if p.Label == "" {
		if m := reLabel.FindString(normalize(caption)); m != "" {
			p.Label = strings.ToLower(m)
		}
	}
	// File names often carry site tags and channel handles, so an explicit caption title
	// is the cleaner source.
	if m := reCaptionTitle.FindStringSubmatch(firstLine); m != nil {
		p.Title = cleanTitle(m[1])
		p.Year, _ = strconv.Atoi(m[2])
	} else if p.Year == 0 && name != firstLine {
		// Captions hold free text, so a number in one counts as the release year only when
		// the caption names the same title as the file.
		var fromCaption Parsed
		parseInto(&fromCaption, firstLine)
		if strings.EqualFold(fromCaption.Title, p.Title) {
			p.Year = fromCaption.Year
		}
	}
	// A TMDB page names the title outright. An episode page wins over the file name, which is how
	// an admin corrects a file the parser could not place. A signed line still wins over that.
	if ref := tmdbPage(fileName + "\n" + caption); ref.ok {
		p.TMDBID = ref.id
		if ref.tv && ref.season > 0 && ref.episode > 0 {
			p.Episode = true
			p.Season = ref.season
			p.Number = ref.episode
		} else if ref.movie {
			p.Episode = false
			p.Season = 0
			p.Number = 0
		}
	}
	// A signed library line wins over the file name. Old captions have none, so they
	// keep the result above.
	if meta, ok := sign.ParseCaption(caption); ok {
		applySigned(&p, meta)
	}
	return p
}

// HintReady reports whether text names a film, or one episode, by a TMDB link or id.
// A link to a whole series, with no season and episode, is not enough to index one file.
func HintReady(text string) bool {
	if Parse("", text).TMDBID <= 0 {
		return false
	}
	ref := tmdbPage(text)
	return !ref.ok || !ref.tv || (ref.season > 0 && ref.episode > 0)
}

type tmdbRef struct {
	id      int
	movie   bool
	tv      bool
	season  int
	episode int
	ok      bool
}

func tmdbPage(s string) tmdbRef {
	m := reTMDBPage.FindStringSubmatch(s)
	if m == nil {
		return tmdbRef{}
	}
	id, _ := strconv.Atoi(m[2])
	if id <= 0 {
		return tmdbRef{}
	}
	ref := tmdbRef{id: id, ok: true, movie: strings.EqualFold(m[1], "movie"), tv: strings.EqualFold(m[1], "tv")}
	if m[4] != "" {
		ref.season, _ = strconv.Atoi(m[3])
		ref.episode, _ = strconv.Atoi(m[4])
	}
	return ref
}

// Head is the readable part of a signed library caption.
type Head struct {
	Title   string
	Year    int
	Episode bool
	Season  int
	Number  int
	TMDB    int
	IMDb    string
	Label   string
	// Soft is already display text, such as "fa (srt)".
	Soft []string
	// Audio is already display text, such as "fa (dubbed)". Empty leaves the audio line out.
	Audio []string
	Hard  string
	// Source is already a caption line, such as "📡 Source: Movies (@movies)". Empty leaves it out.
	Source string
	// AudioLang is the first audio track's language. Empty means it is not known.
	AudioLang string
	// SubLang is the subtitle language for the lookup tag. Empty means it is not known.
	SubLang string
}

// Caption builds the readable head. The signed line and the hashtag are added by the caller.
// The title stays. Ids that already live in the hashtag or the signed line are not spelled out again.
func Caption(h Head) string {
	title := h.Title
	if title == "" {
		title = "Untitled"
	}
	if h.Year > 0 {
		title = fmt.Sprintf("%s (%d)", title, h.Year)
	}
	var bits []string
	if h.Episode {
		bits = append(bits, fmt.Sprintf("S%02dE%02d", h.Season, h.Number))
	}
	if h.Label != "" {
		bits = append(bits, h.Label)
	}
	if h.IMDb != "" {
		bits = append(bits, h.IMDb)
	}
	head := title
	if len(bits) > 0 {
		head += "\n" + strings.Join(bits, " · ")
	}
	if line := audioLine(h.Audio); line != "" {
		head += "\n" + line
	}
	if line := softLine(h.Soft, h.Hard); line != "" {
		head += "\n" + line
	}
	if src := compactSource(h.Source); src != "" {
		head += "\n" + src
	}
	return head
}

// Shrink drops one readable detail, least important first.
// The title, the year, and the episode numbers stay. false means nothing is left to drop.
func (h Head) Shrink() (Head, bool) {
	switch {
	case h.Source != "":
		h.Source = ""
	case len(h.Soft) > 0:
		h.Soft = nil
	case h.Hard != "" && h.Hard != "none" && h.Hard != "unknown":
		h.Hard = ""
	case len(h.Audio) > 0:
		h.Audio = nil
	case h.IMDb != "":
		h.IMDb = ""
	case h.Label != "":
		h.Label = ""
	default:
		return h, false
	}
	return h, true
}

// softLine is the readable subtitle line. "none" and "unknown" hardsubs are not written.
func softLine(soft []string, hard string) string {
	showHard := hard != "" && hard != "none" && hard != "unknown"
	if len(soft) == 0 && !showHard {
		return ""
	}
	line := "💬"
	if len(soft) > 0 {
		line += " " + strings.Join(soft, ", ")
	}
	if showHard {
		if len(soft) > 0 {
			line += " · "
		} else {
			line += " "
		}
		line += "hard " + hard
	}
	return line
}

// Source prefix of the line that says where a copied file came from.
const sourcePrefix = "📡 "

// legacySourcePrefix is the longer form written before the caption was shortened.
const legacySourcePrefix = "📡 Source: "

// SourceLine names the channel a file was copied from.
// A public channel (one with a username) is named. A private channel is not: the caption is
// copied onward, and a private title should not travel with it.
func SourceLine(title, username string) string {
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	title = strings.TrimSpace(title)
	if username == "" {
		return sourcePrefix + "private"
	}
	if title == "" {
		return sourcePrefix + "@" + username
	}
	return fmt.Sprintf("%s%s (@%s)", sourcePrefix, title, username)
}

// compactSource writes a source line without the word "Source". An empty string stays empty.
func compactSource(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	if strings.HasPrefix(line, legacySourcePrefix) {
		line = strings.TrimSpace(strings.TrimPrefix(line, legacySourcePrefix))
	} else if strings.HasPrefix(line, sourcePrefix) {
		line = strings.TrimSpace(strings.TrimPrefix(line, sourcePrefix))
	}
	if line == "" {
		return ""
	}
	if line == "private channel" {
		line = "private"
	}
	return sourcePrefix + line
}

// SourceIn reads a source line already written in a caption. An empty string means there is none.
func SourceIn(caption string) string {
	for _, line := range strings.Split(caption, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, legacySourcePrefix) || strings.HasPrefix(line, sourcePrefix) {
			return line
		}
	}
	return ""
}

// Tag is the hashtag that ends a library caption: #tm_m_<tmdb> for a film and
// #tm_tv_<tmdb>_<season>_<episode> for an episode. Telegram search finds every post of one title
// from it. The id is the film's or the series' TMDB id. An empty string means there is no id yet.
// Tag has no language suffix. LookupTag adds one when this head knows the languages.
func (h Head) Tag() string { return Tag(h.Episode, h.TMDB, h.Season, h.Number) }

// LookupTag is the hashtag that ends the caption, including a language suffix when the languages
// are known. With none known it is the same as Tag.
func (h Head) LookupTag() string {
	return TagWith(h.Episode, h.TMDB, h.Season, h.Number, h.AudioLang, h.SubLang)
}

// Tag builds the hashtag for one film or episode.
func Tag(episode bool, tmdb, season, number int) string {
	if tmdb <= 0 {
		return ""
	}
	if episode {
		return fmt.Sprintf("#tm_tv_%d_%d_%d", tmdb, season, number)
	}
	return fmt.Sprintf("#tm_m_%d", tmdb)
}

// TagWith builds the hashtag and, when a language is known, a suffix.
// audio is the first audio track. sub is the subtitle language. Either may be empty.
// Both empty keeps Tag's form. A known subtitle with an unknown audio writes "none" in the audio slot.
// A known audio with no subtitle writes nothing after the audio code.
func TagWith(episode bool, tmdb, season, number int, audio, sub string) string {
	base := Tag(episode, tmdb, season, number)
	if base == "" {
		return ""
	}
	audio, sub = langCode(audio), langCode(sub)
	switch {
	case audio == "" && sub == "":
		return base
	case audio == "":
		return base + "_none_" + sub
	case sub == "":
		return base + "_" + audio
	default:
		return base + "_" + audio + "_" + sub
	}
}

// langCode is a lower-case ISO 639 code of two or three letters, or "" when code is not one.
func langCode(code string) string {
	code = lang.Normalize(code)
	if len(code) < 2 || len(code) > 3 {
		return ""
	}
	for _, r := range code {
		if r < 'a' || r > 'z' {
			return ""
		}
	}
	return code
}

// langSuffix is the optional _<audio> or _<audio>_<sub> tail. "none" fills the audio slot.
const langSuffix = `(?:_(?:none|[a-z]{2,3})(?:_[a-z]{2,3})?)?`

var (
	reSplitTag = regexp.MustCompile(`^(#tm_m_\d+|#tm_tv_\d+_\d+_\d+)(?:_(none|[a-z]{2,3})(?:_([a-z]{2,3}))?)?$`)
	reAnyTag   = regexp.MustCompile(`#tm_m_\d+` + langSuffix + `\b|#tm_tv_\d+_\d+_\d+` + langSuffix + `\b`)
)

// SplitTag reads a lookup hashtag. base is the film or episode tag, audio and sub are the suffix.
// audio is "none" when the tag says the audio language is unknown and a subtitle language is present.
// A string that is not one of these hashtags returns three empty strings.
func SplitTag(tag string) (base, audio, sub string) {
	m := reSplitTag.FindStringSubmatch(strings.TrimSpace(tag))
	if m == nil {
		return "", "", ""
	}
	return m[1], m[2], m[3]
}

// BaseTag returns the hashtag without a language suffix.
// A string that is not a film or episode hashtag returns "".
func BaseTag(tag string) string {
	base, _, _ := SplitTag(tag)
	return base
}

// TagsIn lists lookup hashtags in caption, in the order they appear. A language suffix stays on the tag.
func TagsIn(caption string) []string {
	return reAnyTag.FindAllString(caption, -1)
}

// TrackLangs picks the languages a lookup tag records.
// Audio is the first audio track. The subtitle is prefer when that language is one of the tracks,
// otherwise the first subtitle track. An unknown language is an empty string.
func TrackLangs(audio []sign.AudioTrack, soft []sign.Track, prefer string) (audioLang, subLang string) {
	if len(audio) > 0 {
		audioLang = langCode(audio[0].Lang)
	}
	prefer = langCode(prefer)
	if prefer != "" {
		for _, t := range soft {
			if langCode(t.Lang) == prefer {
				return audioLang, prefer
			}
		}
	}
	if len(soft) > 0 {
		subLang = langCode(soft[0].Lang)
	}
	return audioLang, subLang
}

// ShowTracks formats soft tracks for the readable caption.
// The preferred language comes first. At most that language and five others are shown,
// and anything left becomes "+N".
func ShowTracks(tracks []sign.Track, prefer string) []string {
	if len(tracks) == 0 {
		return nil
	}
	var known []sign.Track
	for _, t := range tracks {
		if lang.Normalize(t.Lang) == "" {
			continue
		}
		known = append(known, t)
	}
	tracks = known
	var first, rest []sign.Track
	for _, t := range tracks {
		if prefer != "" && strings.EqualFold(t.Lang, prefer) && len(first) == 0 {
			first = append(first, t)
			continue
		}
		rest = append(rest, t)
	}
	ordered := append(first, rest...)
	const max = 6
	show := len(ordered)
	if show > max {
		show = max
	}
	var out []string
	for _, t := range ordered[:show] {
		out = append(out, fmt.Sprintf("%s (%s)", t.Lang, t.Codec))
	}
	if len(ordered) > show {
		out = append(out, fmt.Sprintf("+%d", len(ordered)-show))
	}
	return out
}

// LookupTag is the hashtag Telegram search uses for one title.
// An empty string means the file has no TMDB id yet.
// AudioLang and SubLang add the language suffix when the caller knows the tracks.
func LookupTag(p Parsed) string {
	return TagWith(p.Episode, p.TMDBID, p.Season, p.Number, p.AudioLang, p.SubLang)
}

var (
	reHard     = regexp.MustCompile(`(?i)\bhard[\s._-]*subs?\b`)
	reHardLang = regexp.MustCompile(`(?i)(?:^|[\s._\[-])([a-z]{2,3})[\s._-]*hard[\s._-]*subs?\b`)
	reSoft     = regexp.MustCompile(`(?i)\bsoft[\s._-]*subs?\b`)
)

// HardSub reads a hardcoded-subtitle tag from the file name or caption.
// "none" means the name says the subtitles are soft. "unknown" means the name
// does not say, or says hardsub without a language. There is no picture scan.
func HardSub(fileName, caption string) string {
	text := fileName + "\n" + sign.StripHeader(caption)
	if reHard.MatchString(text) {
		if m := reHardLang.FindStringSubmatch(text); len(m) > 1 && m[1] != "" {
			return strings.ToLower(m[1])
		}
		return "unknown"
	}
	if reSoft.MatchString(text) {
		return "none"
	}
	return "unknown"
}

func applySigned(p *Parsed, m sign.Meta) {
	if m.TMDB > 0 {
		p.TMDBID = m.TMDB
	}
	if m.Quality != "" {
		p.Label = strings.ToLower(m.Quality)
	}
	if m.Kind == "e" {
		p.Episode = true
		p.Season = m.Season
		p.Number = m.Episode
		return
	}
	p.Episode = false
	p.Season = 0
	p.Number = 0
}

// Parts of a library channel that a mirror can receive.
const (
	SectionMovie  = "movie"  // a film, named with a year or a TMDB id
	SectionSeries = "series" // an episode
	SectionMusic  = "music"  // an audio file
	SectionVideo  = "video"  // any other video: personal footage, not a film or a series
)

var audioExt = map[string]bool{
	".mp3": true, ".flac": true, ".m4a": true, ".aac": true,
	".ogg": true, ".opus": true, ".wav": true, ".wma": true,
}

// Section classifies a file for mirroring. A video with no year, id or episode number is
// SectionVideo.
func Section(fileName, caption, mime string) string {
	ext := strings.ToLower(path.Ext(fileName))
	if strings.HasPrefix(strings.ToLower(mime), "audio/") || audioExt[ext] {
		return SectionMusic
	}
	p := Parse(fileName, caption)
	if p.Episode {
		return SectionSeries
	}
	if p.Year > 0 || p.TMDBID > 0 {
		return SectionMovie
	}
	return SectionVideo
}

// UploadName is the file name and caption posted to the channel for one imported video.
// Parse(fileName, caption) returns the same title, year, id, episode and label.
func UploadName(p Parsed, ext string) (fileName, caption string) {
	if p.Title == "" {
		p.Title = "Untitled"
	}
	caption = p.Title
	if p.Year > 0 {
		caption = fmt.Sprintf("%s (%d)", p.Title, p.Year)
	}
	name := caption
	if p.Episode {
		name = fmt.Sprintf("%s S%02dE%02d", caption, p.Season, p.Number)
	}
	if p.TMDBID > 0 {
		name += fmt.Sprintf(" [tmdbid-%d]", p.TMDBID)
	}
	if !p.Episode && p.Label != "" {
		name += " " + p.Label
	}
	ext = strings.ToLower(ext)
	if ext == "" {
		ext = ".mkv"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return name + ext, caption
}

// RelPath returns the .strm path inside the library, using forward slashes. A non-empty
// suffix separates files that would otherwise collide.
func (p Parsed) RelPath(messageID int, suffix string) string {
	title := p.Title
	if title == "" {
		title = "Telegram " + strconv.Itoa(messageID)
	}
	base := title
	if p.Year > 0 {
		base += fmt.Sprintf(" (%d)", p.Year)
	}
	if p.TMDBID > 0 {
		base += fmt.Sprintf(" [tmdbid-%d]", p.TMDBID)
	}
	if p.Episode {
		// Jellyfin shows the episode file name as a title until online metadata arrives,
		// so a resolution label stays out of it. Only a collision adds a suffix.
		season := fmt.Sprintf("Season %02d", p.Season)
		file := fmt.Sprintf("%s S%02dE%02d%s.strm", title, p.Season, p.Number, versionTail(suffix))
		return path.Join("Shows", Sanitize(base), season, Sanitize(file))
	}
	return path.Join("Movies", Sanitize(base), Sanitize(base+versionTail(p.Label, suffix)+".strm"))
}

func versionTail(parts ...string) string {
	tail := ""
	for _, part := range parts {
		if part != "" {
			tail += " - " + part
		}
	}
	return tail
}

func parseInto(p *Parsed, raw string) {
	clean := normalize(raw)
	if clean == "" {
		return
	}
	if m := reLabel.FindString(clean); m != "" {
		p.Label = strings.ToLower(m)
	}
	head := clean
	if s, e, ok := findEpisode(clean); ok {
		p.Episode, p.Season, p.Number = true, s, e
		head = clean[:episodeIndex(clean)]
	}
	if loc := reTag.FindStringIndex(head); loc != nil && loc[0] > 0 {
		head = head[:loc[0]]
	}
	// The last year with text before it is the release year, so "2012 2009" is
	// the 2009 film called 2012 and "1917" alone is a title.
	matches := reYear.FindAllStringSubmatchIndex(head, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		start := matches[i][0]
		if strings.Trim(head[:start], " -") == "" {
			continue
		}
		p.Year, _ = strconv.Atoi(head[matches[i][2]:matches[i][3]])
		head = head[:start]
		break
	}
	p.Title = cleanTitle(head)
}

func normalize(s string) string {
	s = reBrackets.ReplaceAllString(s, " ")
	s = reHandle.ReplaceAllString(s, " ")
	s = strings.NewReplacer(".", " ", "_", " ", "-", " ", "(", " ", ")", " ", "\n", " ", "\r", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

func cleanTitle(s string) string {
	notWord := func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }
	s = strings.TrimFunc(s, notWord)
	return strings.Join(strings.Fields(s), " ")
}

func findEpisode(s string) (season, episode int, ok bool) {
	season, episode, _, _, ok = locateEpisode(s)
	return season, episode, ok
}

func episodeIndex(s string) int {
	if _, _, at, _, ok := locateEpisode(s); ok {
		return at
	}
	return len(s)
}

// locateEpisode finds a season and episode. explicit is true when the name itself wrote the
// season, as in S01E02 or 1x02. E009, "41. Bölüm" and a bare 020 name no season, so the season
// is 1. A written S00 stays season 0: that is a special, not a missing season.
func locateEpisode(s string) (season, episode, at int, explicit, ok bool) {
	for _, re := range []*regexp.Regexp{reEpisode, reEpisodeX} {
		loc := re.FindStringSubmatchIndex(s)
		if loc == nil {
			continue
		}
		season, _ = strconv.Atoi(s[loc[2]:loc[3]])
		episode, _ = strconv.Atoi(s[loc[4]:loc[5]])
		if episode <= 0 {
			continue
		}
		return season, episode, loc[0], true, true
	}
	for _, re := range []*regexp.Regexp{reEEpisode, reBolum, reBareEpisode} {
		all := re.FindAllStringSubmatchIndex(s, -1)
		for i := len(all) - 1; i >= 0; i-- {
			loc := all[i]
			episode, _ = strconv.Atoi(s[loc[2]:loc[3]])
			if episode <= 0 {
				continue
			}
			head := strings.Trim(s[:loc[0]], " -")
			if head == "" || !strings.ContainsFunc(head, unicode.IsLetter) {
				continue
			}
			return 1, episode, loc[0], false, true
		}
	}
	return 0, 0, 0, false, false
}

var faOrdinals = map[string]int{
	"اول": 1, "دوم": 2, "سوم": 3, "چهارم": 4, "پنجم": 5,
	"ششم": 6, "هفتم": 7, "هشتم": 8, "نهم": 9, "دهم": 10,
}

var faDigits = strings.NewReplacer(
	"۰", "0", "۱", "1", "۲", "2", "۳", "3", "۴", "4",
	"۵", "5", "۶", "6", "۷", "7", "۸", "8", "۹", "9",
)

// faShow reads "فصل اول" and "قسمت 9" from a channel caption. The season is 1 when the
// caption names an episode and no season.
func faShow(caption string) (season, episode int, ok bool) {
	text := faDigits.Replace(caption)
	if m := reFaEpisode.FindStringSubmatch(text); m != nil {
		episode, _ = strconv.Atoi(m[1])
	}
	if episode <= 0 {
		return 0, 0, false
	}
	season = 1
	if m := reFaSeason.FindStringSubmatch(text); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			season = n
		} else if n, known := faOrdinals[m[1]]; known {
			season = n
		}
	}
	return season, episode, true
}

// captionTitle is the clean name a post wrote under the file: a Latin line such as
// "Evlilik Guzeldir", or the name inside « ». Metadata lines are skipped.
func captionTitle(caption string) string {
	var latin, quoted string
	for _, line := range strings.Split(caption, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "@") {
			continue
		}
		if m := reQuoted.FindStringSubmatch(line); m != nil && quoted == "" {
			if t := cleanTitle(m[1]); t != "" {
				quoted = t
			}
		}
		bare := cleanTitle(strings.TrimLeftFunc(line, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		}))
		if bare == "" || captionMeta(bare) || !latinTitle(bare) {
			continue
		}
		if latin == "" {
			latin = bare
		}
	}
	if latin != "" {
		return latin
	}
	return quoted
}

func captionMeta(s string) bool {
	for _, word := range []string{"فصل", "قسمت", "کیفیت", "زیرنویس", "سانسور", "سریال"} {
		if strings.Contains(s, word) {
			return true
		}
	}
	return false
}

func latinTitle(s string) bool {
	letters := 0
	for _, r := range s {
		if unicode.IsDigit(r) {
			return false
		}
		if unicode.IsLetter(r) {
			if !unicode.Is(unicode.Latin, r) {
				return false
			}
			letters++
		}
	}
	return letters >= 4 && strings.Contains(s, " ")
}

// WeakTitle reports a name that is not a show or a film: too short, or a uuid.
func WeakTitle(s string) bool {
	letters := 0
	hex := true
	for _, r := range s {
		switch {
		case unicode.IsLetter(r):
			letters++
			if !unicode.Is(unicode.Hex_Digit, r) {
				hex = false
			}
		case unicode.IsDigit(r), unicode.IsSpace(r), r == '-':
		default:
			hex = false
		}
	}
	return letters < 4 || hex
}

func hasLatin(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Latin, r) {
			return true
		}
	}
	return false
}

func tmdbID(s string) int {
	for _, re := range []*regexp.Regexp{reTMDBURL, reTMDBTag} {
		if m := re.FindStringSubmatch(s); m != nil {
			id, _ := strconv.Atoi(m[1])
			return id
		}
	}
	return 0
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

var reserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

const maxSegmentRunes = 120

// Sanitize makes one path segment safe on both Windows and Linux.
func Sanitize(seg string) string {
	var b strings.Builder
	for _, r := range seg {
		switch {
		case r == ':':
			b.WriteString(" -")
		case strings.ContainsRune(`<>"/\|?*`, r), unicode.IsControl(r):
			// dropped
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if utf8.RuneCountInString(out) > maxSegmentRunes {
		ext := path.Ext(out)
		runes := []rune(strings.TrimSuffix(out, ext))
		out = string(runes[:maxSegmentRunes-utf8.RuneCountInString(ext)]) + ext
	}
	out = strings.TrimRight(out, ". ")
	if out == "" {
		return "_"
	}
	stem := out
	if i := strings.IndexByte(stem, '.'); i >= 0 {
		stem = stem[:i]
	}
	if reserved[strings.ToUpper(stem)] {
		out = "_" + out
	}
	return out
}

var (
	reSeasonDir = regexp.MustCompile(`(?i)^(?:season|series|saison|staffel|s)[\s._-]*0*(\d{1,2})$`)
	reSpecials  = regexp.MustCompile(`(?i)^specials?$`)
	// reLooseEpisode reads a file that is only a number, or "E05" or "Episode 5", inside a season folder.
	reLooseEpisode = regexp.MustCompile(`(?i)^\W*(?:e(?:p(?:isode)?)?[\s._-]*)?0*(\d{1,3})(?:\W|$)`)
)

// ParsePath reads a file the way Jellyfin does: the file name first, then the folders above it.
// rel is the path below the media folder, with forward slashes, and holds the file name last.
// A show folder gives the title of an episode, and a season folder gives its season. Folders only
// fill in what the file name leaves out, so a folder such as "Action" never renames a film.
func ParsePath(rel string) Parsed {
	rel = strings.Trim(path.Clean(strings.ReplaceAll(rel, `\`, "/")), "/")
	parts := strings.Split(rel, "/")
	file := parts[len(parts)-1]
	p := Parse(file, "")

	season, hasSeason := 0, false
	var named []string // folders that are not season folders, nearest first
	for i := len(parts) - 2; i >= 0; i-- {
		dir := parts[i]
		if m := reSeasonDir.FindStringSubmatch(dir); m != nil {
			if !hasSeason {
				season, _ = strconv.Atoi(m[1])
				hasSeason = true
			}
			continue
		}
		if reSpecials.MatchString(dir) {
			if !hasSeason {
				season, hasSeason = 0, true
			}
			continue
		}
		named = append(named, dir)
	}

	if !p.Episode && hasSeason {
		stem := strings.TrimSpace(reExt.ReplaceAllString(file, ""))
		if m := reLooseEpisode.FindStringSubmatch(stem); m != nil {
			n, _ := strconv.Atoi(m[1])
			// A number that looks like a year is not an episode.
			if n > 0 && !(n >= 1900 && n <= 2099) {
				p.Episode, p.Season, p.Number = true, season, n
				p.Title = ""
			}
		}
	}
	if p.Episode && hasSeason && p.Season == 0 && season > 0 {
		p.Season = season
	}

	// The nearest folder that names something is the show, or the film.
	var folder Parsed
	for _, dir := range named {
		if f := Parse(dir, ""); f.Title != "" {
			folder = f
			break
		}
	}
	switch {
	case p.Episode && folder.Title != "":
		p.Title, p.Year, p.TMDBID = folder.Title, folder.Year, folder.TMDBID
	case p.Title == "" && folder.Title != "":
		p.Title, p.Year, p.TMDBID = folder.Title, folder.Year, folder.TMDBID
	case folder.Title != "" && strings.EqualFold(folder.Title, p.Title):
		if p.Year == 0 {
			p.Year = folder.Year
		}
		if p.TMDBID == 0 {
			p.TMDBID = folder.TMDBID
		}
	}
	if p.Episode && p.Title == "" {
		p.Episode = false
	}
	return p
}
