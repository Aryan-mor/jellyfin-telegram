package subtitles

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"github.com/Aryan-mor/jellyfin-telegram/internal/lang"
)

const (
	maxSubBytes = 4 << 20
	maxZipFiles = 2000
)

// SubFile is one subtitle from a pack, decoded to UTF-8 with the advertisement cue removed.
type SubFile struct {
	Name string
	Ext  string // ".srt" or ".ass"
	Text string
}

// ExtractSubs returns the .srt and .ass files of a pack. Entry names are never used as
// paths, and each entry is size limited, because the archive comes from a third party. The
// language decides how a file that is not Unicode is decoded.
func ExtractSubs(zipBytes []byte, code string) ([]SubFile, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, fmt.Errorf("subtitle zip: %w", err)
	}
	var out []SubFile
	for i, f := range zr.File {
		if i >= maxZipFiles {
			break
		}
		ext := strings.ToLower(path.Ext(f.Name))
		if f.FileInfo().IsDir() || (ext != ".srt" && ext != ".ass") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxSubBytes))
		rc.Close()
		if err != nil || len(data) == 0 {
			continue
		}
		text := decodeText(data, code)
		if ext == ".srt" {
			text = stripAdCue(text)
		}
		out = append(out, SubFile{Name: f.Name, Ext: ext, Text: text})
	}
	return out, nil
}

// DecodeFile prepares one subtitle file that did not come from an archive.
func DecodeFile(name string, data []byte, code string) SubFile {
	return SubFile{Name: name, Ext: strings.ToLower(path.Ext(name)), Text: decodeText(data, code)}
}

// decodeText turns a subtitle file into UTF-8. UTF-16 is checked first, because those bytes
// also decode under a legacy encoding and would come out as garbage.
func decodeText(b []byte, code string) string {
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	switch {
	case len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE:
		return decodeUTF16(b, unicode.LittleEndian)
	case len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF:
		return decodeUTF16(b, unicode.BigEndian)
	case looksLikeUTF16LE(b):
		return decodeUTF16(b, unicode.LittleEndian)
	case utf8.Valid(b):
		return string(b)
	}
	if dec, err := lang.LegacyDecoder(code).NewDecoder().Bytes(b); err == nil {
		return string(dec)
	}
	return string(b)
}

func decodeUTF16(b []byte, endian unicode.Endianness) string {
	dec := unicode.UTF16(endian, unicode.IgnoreBOM).NewDecoder()
	out, err := io.ReadAll(transform.NewReader(bytes.NewReader(b), dec))
	if err != nil || len(out) == 0 {
		return string(b)
	}
	return string(out)
}

func looksLikeUTF16LE(b []byte) bool {
	if len(b) < 8 || len(b)%2 != 0 {
		return false
	}
	nuls := 0
	for i := 1; i < len(b); i += 2 {
		if b[i] == 0 {
			nuls++
		}
	}
	return nuls*4 >= len(b)
}

// stripAdCue drops a leading cue that advertises the subtitle site. It looks at the first block
// only. The markers stay in Persian whatever the user's language, because that is how the site
// writes its advertisement.
func stripAdCue(srt string) string {
	norm := strings.ReplaceAll(srt, "\r\n", "\n")
	blocks := strings.SplitN(norm, "\n\n", 2)
	if len(blocks) != 2 || !looksLikeAd(blocks[0]) {
		return srt
	}
	return strings.TrimLeft(blocks[1], "\n")
}

func looksLikeAd(block string) bool {
	low := strings.ToLower(block)
	for _, marker := range []string{"sub-plus", "subplus", "sub plus", "ساب پلاس", "ساب‌پلاس"} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}

var (
	reSE      = regexp.MustCompile(`s(\d{1,2})[ ._-]?e(\d{1,3})`)
	reCross   = regexp.MustCompile(`\b(\d{1,2})x(\d{1,3})\b`)
	reSeason  = regexp.MustCompile(`(?:\bs|season)[ ._-]?(\d{1,2})\b`)
	reEpisode = regexp.MustCompile(`(?:\be|episode|ep)[ ._-]?(\d{1,3})\b`)
)

type seasonEpisode struct {
	season, episode int
	hasS, hasE      bool
}

func parseSeasonEpisode(raw string) seasonEpisode {
	s := strings.ToLower(raw)
	for _, re := range []*regexp.Regexp{reSE, reCross} {
		if m := re.FindStringSubmatch(s); m != nil {
			season, _ := strconv.Atoi(m[1])
			episode, _ := strconv.Atoi(m[2])
			return seasonEpisode{season: season, episode: episode, hasS: true, hasE: true}
		}
	}
	var out seasonEpisode
	if m := reSeason.FindStringSubmatch(s); m != nil {
		out.season, _ = strconv.Atoi(m[1])
		out.hasS = true
	}
	if m := reEpisode.FindStringSubmatch(s); m != nil {
		out.episode, _ = strconv.Atoi(m[1])
		out.hasE = true
	}
	return out
}

func looksSDH(name string) bool    { return hasMarker(name, "sdh") }
func looksForced(name string) bool { return hasMarker(name, "forced") }

func hasMarker(name, word string) bool {
	n := strings.ToLower(name)
	for _, p := range []string{"[" + word + "]", "." + word + ".", "_" + word, "-" + word, " " + word} {
		if strings.Contains(n, p) {
			return true
		}
	}
	return false
}

// pickEpisode returns the file for one episode. With several files and no match it returns
// nil, because a season pack's first entry is usually another episode.
func pickEpisode(files []SubFile, season, episode int) *SubFile {
	if len(files) == 1 {
		return &files[0]
	}
	var episodeOnly *SubFile
	for i := range files {
		se := parseSeasonEpisode(files[i].Name)
		if !se.hasE || se.episode != episode {
			continue
		}
		if se.hasS && se.season == season {
			return &files[i]
		}
		if !se.hasS && episodeOnly == nil {
			episodeOnly = &files[i]
		}
	}
	return episodeOnly
}

// pickMovie prefers a full dialogue track over hearing-impaired or forced-only copies, and the
// file sharing the most release tokens with the video.
func pickMovie(files []SubFile, videoName string) *SubFile {
	var best *SubFile
	bestScore := -1 << 30
	for i := range files {
		score := tokenOverlap(files[i].Name, videoName)
		if looksSDH(files[i].Name) {
			score -= 100
		}
		if looksForced(files[i].Name) {
			score -= 200
		}
		if score > bestScore {
			best, bestScore = &files[i], score
		}
	}
	return best
}
