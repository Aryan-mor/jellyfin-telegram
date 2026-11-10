package tgsearch

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Aryan-mor/jellyfin-telegram/internal/naming"
	"github.com/Aryan-mor/jellyfin-telegram/internal/sign"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// Thresholds are the limits a found file must pass before it is copied.
type Thresholds struct {
	// MinSeconds is the shortest running time of a real film or episode. Shorter files are samples
	// or trailers. Zero turns the check off.
	MinSeconds int
	// MinBytes is the smallest file accepted. Zero turns the check off.
	MinBytes int64
	// RuntimeTolerance is how far a film's running time may differ from TMDB's runtime, as a
	// fraction of that runtime (0.25 is 25%). Zero turns the check off. It applies to films only,
	// and only when the runtime is known.
	RuntimeTolerance float64
}

// DefaultThresholds are the limits used when nothing is configured.
func DefaultThresholds() Thresholds {
	return Thresholds{MinSeconds: 10 * 60, MinBytes: 50 << 20, RuntimeTolerance: 0.25}
}

// Verdict is the outcome of checking one candidate file.
type Verdict struct {
	OK     bool
	Reason string
	// KeyID is the signer of a valid signature, "" for an unsigned file. It is set only when the
	// signature itself is authentic, so a forged line cannot blame someone else's key.
	KeyID string
	// Signed is true when the caption carries a jftg line.
	Signed bool
	// BlameKey is true when an authentic signature made a claim the file does not back up.
	BlameKey bool
}

// Verify checks a candidate against the wanted title. tag is the exact hashtag Choose found in the
// caption. The check uses only what the search result carries, so it costs no extra Telegram call.
func Verify(q Query, hit tguser.SearchHit, tag string, th Thresholds) Verdict {
	v := Verdict{}
	if tagOf(q, hit.Caption) != tag || tag == "" {
		v.Reason = "the hashtag does not match the wanted title"
		return v
	}
	duration := hit.Duration
	if meta, ok := sign.ParseCaption(hit.Caption); ok {
		v.Signed = true
		if !meta.Authentic() {
			v.Reason = "the signature is not valid"
			return v
		}
		v.KeyID = sign.KeyID(meta.PubKey)
		if reason := metaMismatch(q, meta, hit, tag); reason != "" {
			v.Reason, v.BlameKey = reason, true
			return v
		}
		if duration == 0 {
			duration = meta.Duration
		}
	} else if hasSignatureLine(hit.Caption) {
		v.Signed = true
		v.Reason = "the signature line cannot be read"
		return v
	}
	if ok, reason := checkFile(q, hit, duration, th); !ok {
		v.Reason = reason
		return v
	}
	v.OK = true
	return v
}

// checkFile applies the size, length and runtime limits. It does not require a hashtag, so a
// file found by title in a direct channel can use the same limits as a hashtag hit.
func checkFile(q Query, hit tguser.SearchHit, duration int, th Thresholds) (bool, string) {
	if th.MinBytes > 0 && hit.Size > 0 && hit.Size < th.MinBytes {
		return false, fmt.Sprintf("the file is only %d bytes, so it looks like a sample", hit.Size)
	}
	if duration > 0 {
		if th.MinSeconds > 0 && duration < th.MinSeconds {
			return false, fmt.Sprintf("it runs %d seconds, so it looks like a sample or a trailer", duration)
		}
		if q.Movie && q.Runtime > 0 && th.RuntimeTolerance > 0 {
			want := float64(q.Runtime * 60)
			if diff := float64(duration) - want; diff > want*th.RuntimeTolerance || -diff > want*th.RuntimeTolerance {
				return false, fmt.Sprintf("it runs %d seconds but the film runs about %d minutes", duration, q.Runtime)
			}
		}
	}
	return true, ""
}

func hasSignatureLine(caption string) bool { return strings.Contains(caption, "jftg:") }

// credibleVideo reports that a channel post is a real video or a video sent as a document, not a
// sample, a trailer, or a file whose signature does not hold.
func credibleVideo(p tguser.Post, th Thresholds) bool {
	if p.ID == 0 || p.Animated || p.Round || !videoFile(p) {
		return false
	}
	hit := tguser.SearchHit{
		MessageID: p.ID, Caption: p.Caption, Size: p.Size,
		Video: true, Duration: int(p.Duration.Seconds()), NoForward: p.NoForward,
	}
	if q, tag, ok := queryIn(p.Caption); ok {
		return Verify(q, hit, tag, th).OK
	}
	if hasSignatureLine(p.Caption) {
		meta, ok := sign.ParseCaption(p.Caption)
		if !ok || !meta.Authentic() {
			return false
		}
	}
	if th.MinBytes > 0 && p.Size > 0 && p.Size < th.MinBytes {
		return false
	}
	if th.MinSeconds > 0 && p.Duration > 0 && int(p.Duration.Seconds()) < th.MinSeconds {
		return false
	}
	return p.Size > 0 || p.Duration > 0
}

func videoFile(p tguser.Post) bool {
	if strings.HasPrefix(strings.ToLower(p.MIME), "video/") || p.Duration > 0 {
		return true
	}
	name := strings.ToLower(p.Name)
	for _, ext := range []string{".mp4", ".mkv", ".webm", ".mov", ".avi"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

// queryIn reads the first library hashtag in a caption.
func queryIn(caption string) (Query, string, bool) {
	if m := reMovie.FindStringSubmatch(caption); m != nil {
		id, _ := strconv.Atoi(m[1])
		if id > 0 {
			return Query{TMDB: id, Movie: true}, m[0], true
		}
	}
	if m := reTV.FindStringSubmatch(caption); m != nil {
		id, _ := strconv.Atoi(m[1])
		if id > 0 {
			return Query{TMDB: id}, m[0], true
		}
	}
	return Query{}, "", false
}

// metaMismatch compares the signed claim with the wanted title and with the file at hand.
func metaMismatch(q Query, m sign.Meta, hit tguser.SearchHit, tag string) string {
	if q.Movie != (m.Kind == "m") || m.TMDB != q.TMDB {
		return "the signed title is a different one"
	}
	if naming.BaseTag(tag) != naming.Tag(m.Kind == "e", m.TMDB, m.Season, m.Episode) {
		return "the signed episode differs from the hashtag"
	}
	if m.Size > 0 && hit.Size > 0 && m.Size != hit.Size {
		return "the signed size differs from the file"
	}
	return ""
}
