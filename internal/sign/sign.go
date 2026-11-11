// Package sign builds and checks the signed caption line of a library post.
// One Ed25519 key belongs to the installation. Every bot of that installation
// publishes the public half in its About text.
package sign

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Aryan-mor/jellyfin-telegram/internal/fileid"
)

const (
	// LinePrefix starts the machine-readable caption line.
	LinePrefix = "jftg:2"
	// ClaimPrefix starts a signed claim card.
	ClaimPrefix = "jftg-claim:1"
	// KeyPrefix starts the public-key line stored in a bot's About text.
	KeyPrefix = "jftg-key:"

	keyFile = "platform.key"

	// AboutLimit is the length Telegram allows for a bot's About text.
	AboutLimit = 120

	// CaptionLimit is Telegram's caption length. The readable head and the machine line share it.
	CaptionLimit = 1024
)

// Track is one soft subtitle stream, written as lang.codec@stream. The stream number tells a
// reader where in the file the track is, so it need not probe the file to find it.
type Track struct {
	Lang  string
	Codec string
	// Index is the stream number in the file, valid when HasIndex is set.
	Index    int
	HasIndex bool
}

// Audio roles. A track is a dub when it is not in the language the film or series was made in.
const (
	RoleDub      = "dub"
	RoleOriginal = "orig"
)

// AudioTrack is one audio stream, written as lang, lang.role or lang.role@stream. Role is empty
// when nobody can tell. The stream number tells a reader where in the file the track is.
type AudioTrack struct {
	Lang     string
	Role     string
	Index    int
	HasIndex bool
}

// maxAudio is how many audio tracks a line carries. Releases with more are not worth the space.
const maxAudio = 8

// Meta is the signed description of one document.
type Meta struct {
	Kind    string // m film, e episode
	TMDB    int
	IMDb    string
	Quality string
	Season  int
	Episode int
	Soft    []Track
	// Audio lists the audio tracks with their language and whether each is a dub. Empty when the
	// file was not inspected. It is optional, so a line without it is read as before.
	Audio []AudioTrack
	// Hard is a language code, "none", or "unknown".
	Hard string
	// Subs is "yes" once a subtitle archive exists, otherwise "no".
	Subs string
	// Status is StatusSubtitles while the subtitles are being prepared, and StatusReady when the
	// post is finished. It is signed, so only a key holder can mark a post ready.
	Status string
	DocID  int64
	Size   int64
	// UniqueID is Telegram's file_unique_id of the document. A bot that speaks only the Bot API
	// can read it, and it must agree with DocID.
	UniqueID string
	// Hash and OSDb name the content, so the same movie is found again after a new upload. See
	// package fileid. Duration is the running time in seconds. All three are optional.
	Hash     string
	OSDb     string
	Duration int
	// ArchiveHash names the subtitle archive of the post once there is one.
	ArchiveHash string
	// PubKey is the key that signed the line. The line carries it, so anyone can check the
	// signature. Whether the key deserves belief is for the reader to decide.
	PubKey ed25519.PublicKey
	// KeyID is the signing platform, the first eight bytes of SHA-256 in base64url. It is worked
	// out from PubKey.
	KeyID string
	// Cut is set when ss was shortened to keep the caption inside CaptionLimit.
	Cut bool
	Sig string
}

// Key is the installation key. The private half never leaves DATA_DIR.
type Key struct {
	priv ed25519.PrivateKey
}

// LoadOrCreate reads the platform key, or writes a new one on first use.
func LoadOrCreate(dir string) (*Key, error) {
	path := filepath.Join(dir, keyFile)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		// Written aside and renamed, so a crash never leaves half a key that would block start-up.
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, priv, 0o600); err != nil {
			return nil, err
		}
		if err := os.Rename(tmp, path); err != nil {
			return nil, err
		}
		return &Key{priv: priv}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, errors.New("platform key has the wrong size")
	}
	return &Key{priv: ed25519.PrivateKey(raw)}, nil
}

// Public returns the public half.
func (k *Key) Public() ed25519.PublicKey {
	return k.priv.Public().(ed25519.PublicKey)
}

// KeyID identifies this key. It is the value stored in a caption as p.
func (k *Key) KeyID() string { return KeyID(k.Public()) }

// KeyID is the first eight bytes of SHA-256, base64url, with no padding.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return base64.RawURLEncoding.EncodeToString(sum[:8])
}

// Canonical is the signed text: fixed field order, no signature.
func (m Meta) Canonical() string {
	kind := m.Kind
	if kind != "e" {
		kind = "m"
	}
	var b strings.Builder
	b.WriteString(LinePrefix)
	field := func(k, v string) {
		b.WriteByte(';')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(v)
	}
	field("k", kind)
	field("t", strconv.Itoa(m.TMDB))
	if m.IMDb != "" {
		field("i", m.IMDb)
	}
	if m.Quality != "" {
		field("q", m.Quality)
	}
	if kind == "e" {
		field("se", strconv.Itoa(m.Season))
		field("ep", strconv.Itoa(m.Episode))
	}
	unique := m.UniqueID
	if unique == "" {
		unique = fileid.UniqueID(m.DocID)
	}
	field("fu", unique)
	if m.Hash != "" {
		field("h", m.Hash)
	}
	if m.OSDb != "" {
		field("oh", m.OSDb)
	}
	if m.Duration > 0 {
		field("du", strconv.Itoa(m.Duration))
	}
	var soft []string
	for _, t := range m.Soft {
		codec := t.Codec
		if codec == "" {
			codec = "sub"
		}
		lang := t.Lang
		if lang == "" {
			lang = "und"
		}
		item := lang + "." + codec
		if t.HasIndex {
			item += "@" + strconv.Itoa(t.Index)
		}
		soft = append(soft, item)
	}
	if m.Cut {
		soft = append(soft, "+")
	}
	field("ss", strings.Join(soft, ","))
	if audio := audioField(m.Audio); audio != "" {
		field("au", audio)
	}
	hard := m.Hard
	if hard == "" {
		hard = "unknown"
	}
	field("hs", hard)
	sa := "no"
	if m.Subs == "yes" {
		sa = "yes"
	}
	field("sa", sa)
	status := m.Status
	if status != StatusSubtitles {
		status = StatusReady
	}
	field("st", status)
	if m.ArchiveHash != "" {
		field("ah", m.ArchiveHash)
	}
	field("d", strconv.FormatInt(m.DocID, 10))
	field("z", strconv.FormatInt(m.Size, 10))
	field("pk", base64.RawURLEncoding.EncodeToString(m.PubKey))
	return b.String()
}

// audioField writes the audio tracks as lang.role@stream, separated by commas.
func audioField(tracks []AudioTrack) string {
	if len(tracks) > maxAudio {
		tracks = tracks[:maxAudio]
	}
	items := make([]string, 0, len(tracks))
	for _, t := range tracks {
		lang := t.Lang
		if lang == "" {
			lang = "und"
		}
		item := lang
		if t.Role != "" {
			item += "." + t.Role
		}
		if t.HasIndex {
			item += "@" + strconv.Itoa(t.Index)
		}
		items = append(items, item)
	}
	return strings.Join(items, ",")
}

// parseAudio reads what audioField wrote.
func parseAudio(s string) ([]AudioTrack, error) {
	var out []AudioTrack
	for _, item := range strings.Split(s, ",") {
		if item == "" {
			continue
		}
		name, stream, hasStream := strings.Cut(item, "@")
		lang, role, _ := strings.Cut(name, ".")
		if role != "" && role != RoleDub && role != RoleOriginal {
			return nil, errors.New("audio role")
		}
		t := AudioTrack{Lang: lang, Role: role}
		if hasStream {
			n, err := strconv.Atoi(stream)
			if err != nil || n < 0 {
				return nil, errors.New("audio stream number")
			}
			t.Index, t.HasIndex = n, true
		}
		out = append(out, t)
	}
	if len(out) > maxAudio {
		return nil, errors.New("too many audio tracks")
	}
	return out, nil
}

// Sign returns the full machine line, including the signature.
func (k *Key) Sign(m Meta) string {
	m.PubKey = k.Public()
	m.KeyID = k.KeyID()
	c := m.Canonical()
	sig := ed25519.Sign(k.priv, []byte(c))
	return c + ";s=" + base64.RawURLEncoding.EncodeToString(sig)
}

// Post signs m so that head and the machine line together stay within CaptionLimit.
// Soft tracks are dropped from the end until the caption fits, and ss then ends with "+".
// The content hash is kept. Callers drop lesser fields before asking for tracks to be cut.
func (k *Key) Post(head string, m Meta) string {
	for {
		line := k.Sign(m)
		if len(head)+1+len(line) <= CaptionLimit || len(m.Soft) == 0 {
			return line
		}
		m.Soft = m.Soft[:len(m.Soft)-1]
		m.Cut = true
	}
}

// ShrinkOptional drops one signed field that is not needed to find the file.
// The content hash, the ids, and the archive hash stay. false means nothing is left to drop.
func (m Meta) ShrinkOptional() (Meta, bool) {
	switch {
	case len(m.Audio) > 0:
		m.Audio = nil
	case m.OSDb != "":
		m.OSDb = ""
	case m.Duration > 0:
		m.Duration = 0
	case m.Quality != "":
		m.Quality = ""
	case m.IMDb != "":
		m.IMDb = ""
	case m.Hard != "":
		m.Hard = ""
	default:
		return m, false
	}
	return m, true
}

// ParseCaption finds the machine line in a caption.
func ParseCaption(caption string) (Meta, bool) {
	for _, line := range strings.Split(caption, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, LinePrefix+";") || line == LinePrefix {
			m, err := ParseLine(line)
			if err != nil {
				return Meta{}, false
			}
			return m, true
		}
	}
	return Meta{}, false
}

// ParseLine reads one machine line. The signature is kept on Meta.Sig. A line whose
// file_unique_id does not agree with its document id is refused.
func ParseLine(line string) (Meta, error) {
	parts := strings.Split(strings.TrimSpace(line), ";")
	if len(parts) == 0 || parts[0] != LinePrefix {
		return Meta{}, errors.New("not a library line")
	}
	got := map[string]string{}
	for _, p := range parts[1:] {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return Meta{}, errors.New("broken library line")
		}
		got[k] = v
	}
	m := Meta{
		Kind: got["k"], IMDb: got["i"], Quality: got["q"],
		Hard: got["hs"], Subs: got["sa"], Status: got["st"], Sig: got["s"],
		Hash: got["h"], OSDb: got["oh"], ArchiveHash: got["ah"],
	}
	if m.Status != StatusSubtitles {
		m.Status = StatusReady
	}
	var err error
	if m.TMDB, err = atoi(got["t"]); err != nil {
		return Meta{}, err
	}
	if m.Duration, err = atoi(got["du"]); err != nil {
		return Meta{}, err
	}
	if m.Kind == "e" {
		if m.Season, err = atoi(got["se"]); err != nil {
			return Meta{}, err
		}
		if m.Episode, err = atoi(got["ep"]); err != nil {
			return Meta{}, err
		}
	}
	if m.DocID, err = strconv.ParseInt(got["d"], 10, 64); err != nil {
		return Meta{}, errors.New("document id")
	}
	if m.Size, err = strconv.ParseInt(got["z"], 10, 64); err != nil {
		return Meta{}, errors.New("size")
	}
	m.UniqueID = got["fu"]
	if m.UniqueID == "" {
		m.UniqueID = fileid.UniqueID(m.DocID)
	} else if m.UniqueID != fileid.UniqueID(m.DocID) {
		return Meta{}, errors.New("file_unique_id does not match the document id")
	}
	pub, err := base64.RawURLEncoding.DecodeString(got["pk"])
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return Meta{}, errors.New("public key")
	}
	m.PubKey = ed25519.PublicKey(pub)
	m.KeyID = KeyID(m.PubKey)
	if au := got["au"]; au != "" {
		if m.Audio, err = parseAudio(au); err != nil {
			return Meta{}, err
		}
	}
	if ss := got["ss"]; ss != "" {
		for _, item := range strings.Split(ss, ",") {
			if item == "+" {
				m.Cut = true
				continue
			}
			name, stream, hasStream := strings.Cut(item, "@")
			lang, codec, ok := strings.Cut(name, ".")
			if !ok {
				lang, codec = name, "sub"
			}
			t := Track{Lang: lang, Codec: codec}
			if hasStream {
				n, err := strconv.Atoi(stream)
				if err != nil || n < 0 {
					return Meta{}, errors.New("stream number")
				}
				t.Index, t.HasIndex = n, true
			}
			m.Soft = append(m.Soft, t)
		}
	}
	return m, nil
}

func atoi(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, errors.New("number")
	}
	return n, nil
}

// Verify reports whether sig matches the canonical line under pub.
func Verify(pub ed25519.PublicKey, m Meta, sig string) bool {
	if len(pub) != ed25519.PublicKeySize || sig == "" {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return false
	}
	return ed25519.Verify(pub, []byte(m.Canonical()), raw)
}

// Authentic reports that the line was signed by the key it carries. It says nothing about
// whether the key deserves belief, or whether the line is true.
func (m Meta) Authentic() bool { return Verify(m.PubKey, m, m.Sig) }

// AboutLine is the line a bot publishes.
func AboutLine(pub ed25519.PublicKey) string {
	return KeyPrefix + base64.RawURLEncoding.EncodeToString(pub)
}

// MergeAbout keeps the bot's other About text and replaces any previous key line.
func MergeAbout(existing, line string) string {
	var kept []string
	for _, part := range strings.Split(existing, "\n") {
		if strings.Contains(part, KeyPrefix) {
			continue
		}
		kept = append(kept, part)
	}
	body := strings.TrimSpace(strings.Join(kept, "\n"))
	if body == "" {
		return line
	}
	// The key matters more than the old text. If both do not fit, only the key stays.
	if utf8.RuneCountInString(body)+1+utf8.RuneCountInString(line) > AboutLimit {
		return line
	}
	return body + "\n" + line
}

// PublicFromAbout reads the key line. ok is false when the About text has none.
func PublicFromAbout(about string) (ed25519.PublicKey, bool) {
	for _, line := range strings.Split(about, "\n") {
		i := strings.Index(line, KeyPrefix)
		if i < 0 {
			continue
		}
		raw := strings.TrimSpace(line[i+len(KeyPrefix):])
		if fields := strings.Fields(raw); len(fields) > 0 {
			raw = fields[0]
		}
		b, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || len(b) != ed25519.PublicKeySize {
			return nil, false
		}
		return ed25519.PublicKey(b), true
	}
	return nil, false
}

// ReplaceLine swaps the machine line where it stands, so the readable head before it and the
// hashtag after it stay in place. A caption without a machine line gets it at the end.
func ReplaceLine(caption, line string) string {
	parts := strings.Split(caption, "\n")
	out := make([]string, 0, len(parts)+1)
	done := false
	for _, part := range parts {
		trim := strings.TrimSpace(part)
		if strings.HasPrefix(trim, LinePrefix+";") || trim == LinePrefix {
			if !done {
				out = append(out, line)
				done = true
			}
			continue
		}
		out = append(out, part)
	}
	if !done {
		body := strings.TrimRight(strings.Join(out, "\n"), "\n")
		if body == "" {
			return line
		}
		return body + "\n" + line
	}
	return strings.Join(out, "\n")
}
