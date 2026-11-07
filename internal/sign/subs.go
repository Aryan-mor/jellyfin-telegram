package sign

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

// SubsPrefix starts the machine-readable line of a subtitle archive's caption.
const SubsPrefix = "jftg-subs:1"

// Subs is the signed description of a subtitle archive. It says which video the archive is for,
// by document id and by content hash, so it still finds its video after a forward or a copy, and
// it names its own bytes, so a reader can tell that the file it downloaded is the one described.
type Subs struct {
	// DocID is the document id of the video. VideoHash is its content hash, when the video post
	// has one.
	DocID     int64
	VideoHash string
	// ArchiveHash names the bytes of the archive. See ArchiveHash.
	ArchiveHash string
	// Tracks lists the subtitle tracks in the archive, with their place in the video file.
	Tracks []Track
	PubKey ed25519.PublicKey
	KeyID  string
	Sig    string
}

// Canonical is the signed text: fixed field order, no signature.
func (s Subs) Canonical() string {
	var b strings.Builder
	b.WriteString(SubsPrefix)
	field := func(k, v string) {
		b.WriteByte(';')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(v)
	}
	field("vd", strconv.FormatInt(s.DocID, 10))
	if s.VideoHash != "" {
		field("vh", s.VideoHash)
	}
	field("ah", s.ArchiveHash)
	var tracks []string
	for _, t := range s.Tracks {
		item := t.Lang
		if item == "" {
			item = "und"
		}
		codec := t.Codec
		if codec == "" {
			codec = "sub"
		}
		item += "." + codec
		if t.HasIndex {
			item += "@" + strconv.Itoa(t.Index)
		}
		tracks = append(tracks, item)
	}
	field("ss", strings.Join(tracks, ","))
	field("pk", base64.RawURLEncoding.EncodeToString(s.PubKey))
	return b.String()
}

// SignSubs returns the full machine line of an archive caption, including the signature.
func (k *Key) SignSubs(s Subs) string {
	s.PubKey = k.Public()
	c := s.Canonical()
	return c + ";s=" + base64.RawURLEncoding.EncodeToString(ed25519.Sign(k.priv, []byte(c)))
}

// ParseSubsCaption finds the machine line in an archive's caption.
func ParseSubsCaption(caption string) (Subs, bool) {
	for _, line := range strings.Split(caption, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, SubsPrefix+";") {
			continue
		}
		s, err := parseSubsLine(line)
		if err != nil {
			return Subs{}, false
		}
		return s, true
	}
	return Subs{}, false
}

func parseSubsLine(line string) (Subs, error) {
	got := map[string]string{}
	for _, p := range strings.Split(line, ";")[1:] {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return Subs{}, errors.New("broken subtitle line")
		}
		got[k] = v
	}
	doc, err := strconv.ParseInt(got["vd"], 10, 64)
	if err != nil {
		return Subs{}, errors.New("video document id")
	}
	pub, err := base64.RawURLEncoding.DecodeString(got["pk"])
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return Subs{}, errors.New("public key")
	}
	s := Subs{DocID: doc, VideoHash: got["vh"], ArchiveHash: got["ah"], Sig: got["s"], PubKey: ed25519.PublicKey(pub)}
	s.KeyID = KeyID(s.PubKey)
	if s.ArchiveHash == "" {
		return Subs{}, errors.New("archive hash")
	}
	if ss := got["ss"]; ss != "" {
		for _, item := range strings.Split(ss, ",") {
			name, stream, hasStream := strings.Cut(item, "@")
			lang, codec, _ := strings.Cut(name, ".")
			t := Track{Lang: lang, Codec: codec}
			if hasStream {
				n, err := strconv.Atoi(stream)
				if err != nil || n < 0 {
					return Subs{}, errors.New("stream number")
				}
				t.Index, t.HasIndex = n, true
			}
			s.Tracks = append(s.Tracks, t)
		}
	}
	return s, nil
}

// Authentic reports that the line was signed by the key it carries.
func (s Subs) Authentic() bool {
	raw, err := base64.RawURLEncoding.DecodeString(s.Sig)
	if err != nil || len(s.PubKey) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(s.PubKey, []byte(s.Canonical()), raw)
}
