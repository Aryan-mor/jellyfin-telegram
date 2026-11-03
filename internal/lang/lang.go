// Package lang answers language questions for any language the user configures. Nothing else in
// the service should know about a specific language.
package lang

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/abadojack/whatlanggo"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

const (
	// minDetectLetters is the least text worth running language detection on.
	minDetectLetters = 20
	// maxDetectBytes caps the input, because the detector gives wrong answers on long text. On a
	// whole subtitle file it called Persian Uyghur and reported negative confidence for every
	// language tried, while the first few kilobytes of the same files came out right.
	maxDetectBytes = 4000
)

// Normalize folds any ISO 639 code to one form, so per, fas and fa compare equal. Placeholders
// and unknown codes return an empty string.
func Normalize(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	switch code {
	case "", "und", "mul", "zxx", "mis":
		return ""
	}
	tag, err := language.All.Parse(code)
	if err != nil {
		return ""
	}
	base, conf := tag.Base()
	if conf == language.No {
		return ""
	}
	return base.String()
}

// ISO3 returns the three-letter code of a normalized language.
func ISO3(code string) string {
	tag, err := language.Parse(code)
	if err != nil {
		return code
	}
	base, _ := tag.Base()
	return base.ISO3()
}

// aliases holds names that display names do not cover.
var aliases = map[string][]string{
	"fa": {"farsi", "پارسی"},
}

// InTitle reports whether a free-text track title names the language, in English or natively.
func InTitle(code, title string) bool {
	title = strings.ToLower(title)
	if code == "" || title == "" {
		return false
	}
	tag := language.Make(code)
	names := append([]string{display.English.Languages().Name(tag), display.Self.Name(tag)}, aliases[code]...)
	for _, name := range names {
		if name != "" && strings.Contains(title, strings.ToLower(name)) {
			return true
		}
	}
	return false
}

// scripts lists languages written in a script of their own, where a title in that script is good
// evidence of the language. Latin script says nothing, because most languages share it.
var scripts = map[string]*unicode.RangeTable{
	"fa": unicode.Arabic, "ar": unicode.Arabic, "ur": unicode.Arabic, "ps": unicode.Arabic, "ku": unicode.Arabic,
	"ru": unicode.Cyrillic, "uk": unicode.Cyrillic, "bg": unicode.Cyrillic, "sr": unicode.Cyrillic, "mk": unicode.Cyrillic,
	"el": unicode.Greek, "he": unicode.Hebrew, "hi": unicode.Devanagari, "bn": unicode.Bengali, "ta": unicode.Tamil,
	"te": unicode.Telugu, "th": unicode.Thai, "ko": unicode.Hangul, "ja": unicode.Han, "zh": unicode.Han,
}

// TitleInScript reports whether a title is written in the script of a language.
func TitleInScript(code, title string) bool {
	table, ok := scripts[code]
	if !ok {
		return false
	}
	for _, r := range title {
		if unicode.Is(table, r) {
			return true
		}
	}
	return false
}

var (
	reTiming = regexp.MustCompile(`(?m)^\s*\d+\s*$|^.*-->.*$`)
	reMarkup = regexp.MustCompile(`<[^>]*>|\{[^}]*\}`)
)

// IsLanguage reports whether subtitle text is written in the language. A short or unreliable
// sample counts as no, so a wrong guess leaves the track as it was tagged.
func IsLanguage(code, subtitleText string) bool {
	text := reMarkup.ReplaceAllString(reTiming.ReplaceAllString(subtitleText, " "), " ")
	letters := 0
	for _, r := range text {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	if letters < minDetectLetters {
		return false
	}
	if len(text) > maxDetectBytes {
		text = strings.ToValidUTF8(text[:maxDetectBytes], "")
	}
	info := whatlanggo.Detect(text)
	// The three-letter code is used because some languages, Persian among them, have no
	// two-letter code in the detection library.
	return info.IsReliable() && Normalize(info.Lang.Iso6393()) == code
}

// LegacyDecoder returns the usual pre-Unicode encoding of a language, for subtitle files that are
// not UTF-8.
func LegacyDecoder(code string) encoding.Encoding {
	switch code {
	case "fa", "ar", "ur", "ps", "ku":
		return charmap.Windows1256
	case "ru", "uk", "bg", "sr", "mk":
		return charmap.Windows1251
	case "el":
		return charmap.Windows1253
	case "tr":
		return charmap.Windows1254
	case "he":
		return charmap.Windows1255
	case "pl", "cs", "sk", "hu", "ro", "hr", "sl":
		return charmap.Windows1250
	case "vi":
		return charmap.Windows1258
	case "th":
		return charmap.Windows874
	case "zh":
		return simplifiedchinese.GB18030
	case "ja":
		return japanese.ShiftJIS
	case "ko":
		return korean.EUCKR
	}
	return charmap.Windows1252
}
