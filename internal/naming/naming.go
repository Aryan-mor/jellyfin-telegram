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
}

// Matchable reports whether Jellyfin can be expected to find online metadata.
func (p Parsed) Matchable() bool { return p.TMDBID > 0 || p.Year > 0 || p.Episode }

var (
	reTMDBURL  = regexp.MustCompile(`(?i)themoviedb\.org/(?:movie|tv)/(\d+)`)
	reTMDBTag  = regexp.MustCompile(`(?i)\btmdb(?:id)?[\s:=_-]*(\d+)\b`)
	reEpisode  = regexp.MustCompile(`(?i)\bS(\d{1,2})\s*-?\s*E(\d{1,3})\b`)
	reEpisodeX = regexp.MustCompile(`\b(\d{1,2})x(\d{2,3})\b`)
	reYear     = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
	reLabel    = regexp.MustCompile(`(?i)\b(2160p|1440p|1080p|720p|576p|480p|360p|4k)\b`)
	reTag      = regexp.MustCompile(`(?i)\b(2160p|1440p|1080p|720p|576p|480p|360p|4k|uhd|hdr|bluray|blu ray|brrip|bdrip|web dl|webdl|webrip|hdtv|dvdrip|remux|x264|x265|h264|h265|hevc|av1|aac|dts|ac3|10bit|dubbed|softsub|hardsub)\b`)
	reBrackets = regexp.MustCompile(`\[[^\]]*\]|\{[^}]*\}`)
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

	if !p.Episode {
		if s, e, ok := findEpisode(normalize(caption)); ok {
			p.Episode, p.Season, p.Number = true, s, e
		}
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
	// A signed library line wins over the file name. Old captions have none, so they
	// keep the result above.
	if meta, ok := sign.ParseCaption(caption); ok {
		applySigned(&p, meta)
	}
	return p
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
	Hard string
}

// Caption builds the readable head. The signed line is added by the caller.
func Caption(h Head) string {
	title := h.Title
	if title == "" {
		title = "Untitled"
	}
	if h.Year > 0 {
		title = fmt.Sprintf("%s (%d)", title, h.Year)
	}
	kind := "Movie"
	if h.Episode {
		kind = "Episode"
	}
	var bits []string
	bits = append(bits, kind)
	if h.TMDB > 0 {
		bits = append(bits, fmt.Sprintf("TMDB %d", h.TMDB))
	}
	if h.IMDb != "" {
		bits = append(bits, "IMDb "+h.IMDb)
	}
	if h.Label != "" {
		bits = append(bits, h.Label)
	}
	soft := "none"
	if len(h.Soft) > 0 {
		soft = strings.Join(h.Soft, ", ")
	}
	hard := h.Hard
	if hard == "" {
		hard = "unknown"
	}
	return fmt.Sprintf("%s\n🎬 %s\n💬 Soft: %s · Hard: %s",
		title, strings.Join(bits, " · "), soft, hard)
}

// Tag is the hashtag that ends a library caption: #tm_m_<tmdb> for a film and
// #tm_tv_<tmdb>_<season>_<episode> for an episode. Telegram search finds every post of one title
// from it. The id is the film's or the series' TMDB id. An empty string means there is no id yet.
func (h Head) Tag() string { return Tag(h.Episode, h.TMDB, h.Season, h.Number) }

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

// ShowTracks formats soft tracks for the readable caption.
// The preferred language comes first. At most that language and five others are shown,
// and anything left becomes "+N".
func ShowTracks(tracks []sign.Track, prefer string) []string {
	if len(tracks) == 0 {
		return nil
	}
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
func LookupTag(p Parsed) string {
	return Tag(p.Episode, p.TMDBID, p.Season, p.Number)
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
	s = strings.NewReplacer(".", " ", "_", " ", "(", " ", ")", " ", "\n", " ", "\r", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

func cleanTitle(s string) string {
	notWord := func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }
	s = strings.TrimFunc(s, notWord)
	return strings.Join(strings.Fields(s), " ")
}

func findEpisode(s string) (season, episode int, ok bool) {
	for _, re := range []*regexp.Regexp{reEpisode, reEpisodeX} {
		if m := re.FindStringSubmatch(s); m != nil {
			season, _ = strconv.Atoi(m[1])
			episode, _ = strconv.Atoi(m[2])
			return season, episode, true
		}
	}
	return 0, 0, false
}

func episodeIndex(s string) int {
	for _, re := range []*regexp.Regexp{reEpisode, reEpisodeX} {
		if loc := re.FindStringIndex(s); loc != nil {
			return loc[0]
		}
	}
	return len(s)
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
