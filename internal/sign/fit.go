package sign

import (
	"strings"
)

// CaptionLen is the length Telegram counts for a caption: UTF-16 code units.
func CaptionLen(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// Fit is the last guard before a caption is sent. It never returns more than CaptionLimit
// characters, so Telegram cannot answer MEDIA_CAPTION_TOO_LONG.
//
// The title, the signed machine line (which holds the file hash) and the hashtag are what the
// library needs, so they stay. Everything else goes one line at a time until the caption fits:
// first the claim, then the readable details from the last line upwards. The signed line is never
// cut, because that would break its signature. If the title alone is still too long, it is
// shortened.
func Fit(caption string) string {
	if CaptionLen(caption) <= CaptionLimit {
		return caption
	}
	lines := strings.Split(caption, "\n")
	keep := make([]bool, len(lines))
	for i := range keep {
		keep[i] = true
	}
	first := 0
	if _, _, ok := ParseStatus(lines[0]); ok {
		first = 1
	}
	claim := -1
	if first < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[first]), ClaimPrefix) {
		claim = first
		first++
	}
	title := first
	if title >= len(lines) {
		title = -1
	}
	protected := func(i int) bool {
		l := strings.TrimSpace(lines[i])
		return i == title || i < first && claim != i || strings.HasPrefix(l, LinePrefix) ||
			strings.HasPrefix(l, SubsPrefix) || strings.HasPrefix(l, "#")
	}
	join := func() string {
		out := make([]string, 0, len(lines))
		for i, l := range lines {
			if keep[i] {
				out = append(out, l)
			}
		}
		return strings.Join(out, "\n")
	}
	if claim >= 0 {
		keep[claim] = false
		if out := join(); CaptionLen(out) <= CaptionLimit {
			return out
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if !keep[i] || protected(i) {
			continue
		}
		keep[i] = false
		if out := join(); CaptionLen(out) <= CaptionLimit {
			return out
		}
	}
	if title < 0 {
		return join()
	}
	// Only the title can give now.
	runes := []rune(lines[title])
	for len(runes) > 1 {
		runes = runes[:len(runes)-1]
		lines[title] = strings.TrimRight(string(runes), " ") + "…"
		if out := join(); CaptionLen(out) <= CaptionLimit {
			return out
		}
	}
	return join()
}
