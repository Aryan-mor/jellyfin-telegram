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
		return p
	}
	// Captions hold free text, so a number in one counts as the release year only when
	// the caption names the same title as the file.
	if p.Year == 0 && name != firstLine {
		var fromCaption Parsed
		parseInto(&fromCaption, firstLine)
		if strings.EqualFold(fromCaption.Title, p.Title) {
			p.Year = fromCaption.Year
		}
	}
	return p
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
