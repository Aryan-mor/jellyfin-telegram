package tgsearch

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/Aryan-mor/jellyfin-telegram/internal/naming"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

var (
	reIMDb   = regexp.MustCompile(`(?i)\b(tt\d{7,9})\b`)
	reSample = regexp.MustCompile(`(?i)\b(samples?|trailers?|teasers?)\b`)
)

// MatchTitle reports whether a hit is the wanted film. An IMDb id is enough. Otherwise the
// title must be the whole title, not a shorter part of the same franchise, and the year must
// be within one year. Samples and trailers are refused either way.
func MatchTitle(q Query, hit tguser.SearchHit) (bool, string) {
	blob := hit.Name + "\n" + hit.Caption
	if reSample.MatchString(blob) {
		return false, "it looks like a sample or a trailer"
	}
	if q.IMDb != "" {
		want := strings.ToLower(q.IMDb)
		for _, id := range reIMDb.FindAllString(blob, -1) {
			if strings.EqualFold(id, want) {
				return true, "imdb"
			}
		}
	}
	wantTitle := normTitle(q.Title)
	wantAlt := normTitle(q.AltTitle)
	if q.Year <= 0 || (wantTitle == "" && wantAlt == "") {
		return false, "the title or the year is missing"
	}
	for _, parsed := range []naming.Parsed{
		naming.Parse(hit.Name, ""),
		naming.Parse("", hit.Caption),
		naming.Parse(hit.Name, hit.Caption),
	} {
		if parsed.Year == 0 || abs(parsed.Year-q.Year) > 1 {
			continue
		}
		got := normTitle(parsed.Title)
		if got == "" {
			continue
		}
		if got == wantTitle || (wantAlt != "" && got == wantAlt) {
			return true, "title"
		}
	}
	return false, "the title does not match"
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// normTitle folds a title so "Spider-Man: Brand New Day" and "spider man brand new day" compare equal.
// Articles and punctuation are dropped. A shorter franchise name stays different.
func normTitle(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '.' || r == '_' || r == '-' || r == ':' || unicode.IsSpace(r):
			b.WriteByte(' ')
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	var keep []string
	for _, word := range strings.Fields(b.String()) {
		if word == "a" || word == "an" || word == "the" {
			continue
		}
		keep = append(keep, word)
	}
	return strings.Join(keep, " ")
}

// searchTexts is the order a channel is asked: IMDb id, the full title, then the most
// distinctive part after a colon (Brand New Day, not Spider-Man).
func searchTexts(q Query) []string {
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		for _, have := range out {
			if strings.EqualFold(have, s) {
				return
			}
		}
		out = append(out, s)
	}
	add(q.IMDb)
	full := strings.TrimSpace(q.Title)
	if full == "" {
		full = strings.TrimSpace(q.AltTitle)
	}
	add(full)
	if !strings.EqualFold(q.AltTitle, full) {
		add(q.AltTitle)
	}
	add(distinctive(full))
	add(distinctive(q.AltTitle))
	return out
}

// distinctive is the longest phrase after a colon. A title with no colon has none,
// so a one-word franchise name is not searched on its own.
func distinctive(title string) string {
	i := strings.Index(title, ":")
	if i < 0 {
		return ""
	}
	best := ""
	for _, part := range strings.Split(title[i+1:], ":") {
		part = strings.TrimSpace(part)
		if len([]rune(part)) > len([]rune(best)) {
			best = part
		}
	}
	return best
}
