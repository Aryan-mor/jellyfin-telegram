package sign

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Statuses. A bot writes the status as the first line of a caption, so everybody who opens the
// channel sees where a file is, and every installation can read it.
const (
	// StatusReview is on a raw file while a bot identifies it.
	StatusReview = "review"
	// StatusIndexed is on a raw file once its signed post exists.
	StatusIndexed = "indexed"
	// StatusRejected is on a raw file that could not be identified. The reason follows.
	StatusRejected = "rejected"
	// StatusSubtitles is on a signed post while its subtitles are being prepared.
	StatusSubtitles = "subtitles"
	// StatusReady is on a signed post that is finished. Only these are copied to mirrors.
	StatusReady = "ready"
)

var statusText = map[string]string{
	StatusReview:    "🔎 Review",
	StatusIndexed:   "📤 Indexed",
	StatusRejected:  "⛔ Rejected",
	StatusSubtitles: "💬 Subtitles",
	StatusReady:     "✅ Ready",
}

// StatusLine is the first line of a caption for one status. why follows a rejection.
func StatusLine(status, why string) string {
	line := statusText[status]
	if line == "" {
		return ""
	}
	if why != "" {
		line += ": " + why
	}
	return line
}

// ParseStatus reads a status line.
func ParseStatus(line string) (status, why string, ok bool) {
	line = strings.TrimSpace(line)
	for name, text := range statusText {
		if line == text {
			return name, "", true
		}
		if strings.HasPrefix(line, text+":") {
			return name, strings.TrimSpace(strings.TrimPrefix(line, text+":")), true
		}
	}
	return "", "", false
}

// Claim says that one installation is working on a message. The key travels in the claim, so any
// installation can check it without a list of keys.
type Claim struct {
	Op     string // "review" or "subs"
	Target int    // the message the work is about
	TTL    int    // seconds after the edit that wrote the claim
	PubKey ed25519.PublicKey
	Sig    string
}

// KeyID is the key that made the claim.
func (c Claim) KeyID() string { return KeyID(c.PubKey) }

// ClaimCanonical is the signed text of a claim.
func ClaimCanonical(op string, target, ttl int, pub ed25519.PublicKey) string {
	return fmt.Sprintf("%s;op=%s;m=%d;ttl=%d;pk=%s", ClaimPrefix, op, target, ttl, base64.RawURLEncoding.EncodeToString(pub))
}

// SignClaim writes a claim line.
func (k *Key) SignClaim(op string, target, ttl int) string {
	c := ClaimCanonical(op, target, ttl, k.Public())
	return c + ";s=" + base64.RawURLEncoding.EncodeToString(ed25519.Sign(k.priv, []byte(c)))
}

// ParseClaim reads one claim line.
func ParseClaim(line string) (Claim, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, ClaimPrefix+";") {
		return Claim{}, false
	}
	got := map[string]string{}
	for _, p := range strings.Split(line, ";")[1:] {
		if k, v, ok := strings.Cut(p, "="); ok {
			got[k] = v
		}
	}
	target, err1 := strconv.Atoi(got["m"])
	ttl, err2 := strconv.Atoi(got["ttl"])
	pub, err3 := base64.RawURLEncoding.DecodeString(got["pk"])
	if err1 != nil || err2 != nil || err3 != nil || ttl <= 0 || got["op"] == "" || len(pub) != ed25519.PublicKeySize || got["s"] == "" {
		return Claim{}, false
	}
	return Claim{Op: got["op"], Target: target, TTL: ttl, PubKey: pub, Sig: got["s"]}, true
}

// Verify checks the signature against the key in the claim.
func (c Claim) Verify() bool {
	raw, err := base64.RawURLEncoding.DecodeString(c.Sig)
	if err != nil || len(c.PubKey) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(c.PubKey, []byte(ClaimCanonical(c.Op, c.Target, c.TTL, c.PubKey)), raw)
}

// Live reports that the claim is genuine, is about this operation and message, and has not lapsed.
// edited is when the caption that holds it was last edited, which is when the claim was made.
func (c Claim) Live(op string, target int, edited, now time.Time) bool {
	if c.Op != op || c.Target != target || !c.Verify() {
		return false
	}
	return edited.Add(time.Duration(c.TTL) * time.Second).After(now)
}

// Header is what bots write above a caption: a status line and, while work is going on, a claim.
type Header struct {
	Status string
	Why    string
	Claim  *Claim
	// ClaimLine is the claim as written, so an edit can keep it as it is.
	ClaimLine string
}

// Render puts the header above a body.
func (h Header) Render(body string) string {
	return WithHeader(h.Status, h.Why, h.ClaimLine, body)
}

// SplitHeader separates the header from the rest of a caption. A caption with no status on its
// first line has no header, and all of it is the body.
func SplitHeader(caption string) (Header, string) {
	lines := strings.Split(caption, "\n")
	status, why, ok := ParseStatus(lines[0])
	if !ok {
		return Header{}, caption
	}
	h := Header{Status: status, Why: why}
	rest := lines[1:]
	if len(rest) > 0 && strings.HasPrefix(strings.TrimSpace(rest[0]), ClaimPrefix) {
		if c, ok := ParseClaim(rest[0]); ok {
			h.Claim = &c
			h.ClaimLine = strings.TrimSpace(rest[0])
		}
		rest = rest[1:]
	}
	return h, strings.Join(rest, "\n")
}

// StripHeader is the caption without its header: what a person wrote, or the readable head of a
// library post.
func StripHeader(caption string) string {
	_, body := SplitHeader(caption)
	return body
}

// WithHeader puts a status line, and a claim line if there is one, above a body.
func WithHeader(status, why, claimLine, body string) string {
	out := StatusLine(status, why)
	if claimLine != "" {
		out += "\n" + claimLine
	}
	if body != "" {
		out += "\n" + body
	}
	return out
}

// ArchiveHash names the bytes of a subtitle archive: 12 bytes of SHA-256 in url-safe base64. A
// reader that finds the archive checks it against the signed post before it uses it.
func ArchiveHash(data []byte) string {
	sum := sha256.Sum256(data)
	return base64.RawURLEncoding.EncodeToString(sum[:12])
}

// IsReady reports that a caption is a finished library post: its status line says ready, and so
// does the signed line under it. A mirror copies only these.
func IsReady(caption string) bool {
	h, body := SplitHeader(caption)
	meta, ok := ParseCaption(body)
	return ok && h.Status == StatusReady && meta.Status == StatusReady
}
