package naming

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Aryan-mor/jellyfin-telegram/internal/lang"
	"github.com/Aryan-mor/jellyfin-telegram/internal/sign"
)

// AudioIn is one audio stream as the file reports it.
type AudioIn struct {
	// Lang is the normalized language code, empty when the file does not say.
	Lang  string
	Title string
	Index int
}

var (
	// reDub finds a dub tag in a file name or a track title. The Persian word is "dubbed".
	reDub = regexp.MustCompile(`(?i)(\bdub(?:bed|bing)?\b|\bdual[\s._-]*audio\b|\bmulti[\s._-]*audio\b|دوبله)`)
	// reOriginal finds a tag that says a track is in the original language.
	reOriginal = regexp.MustCompile(`(?i)(\boriginal\b|\borig\b|صدای اصلی|زبان اصلی)`)
	// reTrackDub is stricter than reDub. A track title such as "Dual Audio" would say nothing about
	// the one track it labels, so only a plain dub tag counts there.
	reTrackDub = regexp.MustCompile(`(?i)(\bdub(?:bed|bing)?\b|دوبله)`)
)

// AudioTracks says, for each audio stream, whether it is a dub or the original.
//
// A tag in the track's own title decides first. Otherwise, when the original language of the film
// or series is known, a track in another language is a dub and a track in that language is the
// original. When it is not known, only a file that says "dubbed" and holds a single audio track
// is read as a dub. Anything else is left without a role, which is honest: the file does not say.
func AudioTracks(in []AudioIn, original, fileText string) []sign.AudioTrack {
	original = lang.Normalize(original)
	fileDubbed := reDub.MatchString(fileText)
	out := make([]sign.AudioTrack, 0, len(in))
	for _, a := range in {
		t := sign.AudioTrack{Lang: a.Lang, Index: a.Index, HasIndex: true}
		if t.Lang == "" {
			t.Lang = "und"
		}
		switch {
		case reTrackDub.MatchString(a.Title):
			t.Role = sign.RoleDub
		case reOriginal.MatchString(a.Title):
			t.Role = sign.RoleOriginal
		case original != "" && a.Lang != "":
			t.Role = sign.RoleDub
			if a.Lang == original {
				t.Role = sign.RoleOriginal
			}
		case fileDubbed && len(in) == 1:
			t.Role = sign.RoleDub
		}
		out = append(out, t)
	}
	return out
}

// ShowAudio formats audio tracks for the readable caption, such as "fa (dubbed)". At most six
// are shown and anything left becomes "+N".
func ShowAudio(tracks []sign.AudioTrack) []string {
	const max = 6
	var out []string
	for i, t := range tracks {
		if i == max {
			out = append(out, fmt.Sprintf("+%d", len(tracks)-max))
			break
		}
		switch t.Role {
		case sign.RoleDub:
			out = append(out, t.Lang+" (dubbed)")
		case sign.RoleOriginal:
			out = append(out, t.Lang+" (original)")
		default:
			out = append(out, t.Lang)
		}
	}
	return out
}

// audioLine is the readable audio line, or "" when the file was not inspected.
func audioLine(shown []string) string {
	if len(shown) == 0 {
		return ""
	}
	return "🔊 Audio: " + strings.Join(shown, " · ")
}
