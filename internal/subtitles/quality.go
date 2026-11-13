package subtitles

import (
	"regexp"
	"strings"
)

var (
	reQuality = regexp.MustCompile(`(?i)\b(2160p|1440p|1080p|720p|576p|480p|360p|4k)\b`)
	reSignedQ = regexp.MustCompile(`(?i)(?:^|[;\s])q=([0-9]{3,4}p|4k)\b`)
)

// FileQuality is the picture size of a release. A signed q= wins over a token in the name,
// because the signature is what this library writes for the file it actually has.
func FileQuality(name, caption string) string {
	if m := reSignedQ.FindStringSubmatch(caption); m != nil {
		return canonQuality(m[1])
	}
	if m := reQuality.FindString(name + "\n" + caption); m != "" {
		return canonQuality(m)
	}
	return ""
}

// SameQuality reports that two releases are the same size. An unknown size never matches,
// so a subtitle is not paired with a file whose cut we cannot check.
func SameQuality(a, b string) bool {
	a, b = canonQuality(a), canonQuality(b)
	return a != "" && a == b
}

func canonQuality(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "4k" {
		return "2160p"
	}
	return s
}
