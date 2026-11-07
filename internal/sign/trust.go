package sign

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	trustKey = "trust.keys"
	repKey   = "trust.rep"
	checkPre = "trust.check."

	// reputableAfter is how many checked posts, all of them right, make a key reputable. Its posts
	// are then checked only now and then.
	reputableAfter = 20
	// blockAfter is how many wrong posts block a key, and blockFor for how long.
	blockAfter = 3
	blockFor   = 24 * time.Hour
)

// State stores short strings. The catalog implements it.
type State interface {
	StateValue(ctx context.Context, key string) (string, error)
	SetStateValue(ctx context.Context, key, value string) error
}

// Standing is how far a key is believed.
type Standing int

const (
	// StandingUnknown: every post is checked against its file before it counts.
	StandingUnknown Standing = iota
	// StandingReputable: many posts were checked and none was wrong, so only a few are checked.
	StandingReputable
	// StandingTrusted: believed without a check. This installation's own key, and a key that was
	// added by hand.
	StandingTrusted
	// StandingBlocked: too many wrong posts lately, so its posts are not looked at.
	StandingBlocked
)

// record is what one key has done so far.
type record struct {
	OK       int   `json:"ok"`
	Fail     int   `json:"fail"`
	LastFail int64 `json:"last_fail,omitempty"`
}

// Trust decides how far a key is believed. Nobody has to tell it: a key earns belief from posts
// that were checked against their files and found right, and loses it by being wrong. A key can
// still be trusted by hand, for an installation that wants that.
type Trust struct {
	State State
	Own   ed25519.PublicKey

	mu sync.Mutex
}

// Row is one key as the panel shows it.
type Row struct {
	Fingerprint string `json:"fingerprint"`
	Own         bool   `json:"own,omitempty"`
	Manual      bool   `json:"manual,omitempty"`
	// Verified is how many of its posts were checked and right, Wrong how many were not.
	Verified int `json:"verified,omitempty"`
	Wrong    int `json:"wrong,omitempty"`
}

// Public returns the key that was added by hand with this id. ok is false when there is none.
// The installation's own key is always returned.
func (t *Trust) Public(ctx context.Context, keyID string) (ed25519.PublicKey, bool, error) {
	if t != nil && len(t.Own) == ed25519.PublicKeySize && KeyID(t.Own) == keyID {
		return t.Own, true, nil
	}
	list, err := t.keys(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, pub := range list {
		if KeyID(pub) == keyID {
			return pub, true, nil
		}
	}
	return nil, false, nil
}

// Standing says how far to believe a key right now.
func (t *Trust) Standing(ctx context.Context, keyID string, now time.Time) (Standing, error) {
	if _, ok, err := t.Public(ctx, keyID); err != nil || ok {
		if ok {
			return StandingTrusted, nil
		}
		return StandingUnknown, err
	}
	reps, err := t.records(ctx)
	if err != nil {
		return StandingUnknown, err
	}
	r := reps[keyID]
	switch {
	case r.Fail >= blockAfter && now.Sub(time.Unix(r.LastFail, 0)) < blockFor:
		return StandingBlocked, nil
	case r.OK >= reputableAfter && r.Fail == 0:
		return StandingReputable, nil
	}
	return StandingUnknown, nil
}

// Record adds the result of one check of a post to the key's record.
func (t *Trust) Record(ctx context.Context, keyID string, good bool, now time.Time) error {
	if t == nil || t.State == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	reps, err := t.records(ctx)
	if err != nil {
		return err
	}
	r := reps[keyID]
	if good {
		r.OK++
	} else {
		r.Fail++
		r.LastFail = now.Unix()
	}
	reps[keyID] = r
	raw, err := json.Marshal(reps)
	if err != nil {
		return err
	}
	return t.State.SetStateValue(ctx, repKey, string(raw))
}

// factKey names what a check looked at, so the same post is not checked twice. A new status on the
// post does not change the facts, so it does not start a new check.
func factKey(m Meta) string {
	text := fmt.Sprintf("%s|%s|%s|%s|%d", m.KeyID, m.UniqueID, m.Hash, m.OSDb, m.Duration)
	sum := sha256.Sum256([]byte(text))
	return checkPre + hex.EncodeToString(sum[:12])
}

// checked returns the result of an earlier check of the same facts.
func (t *Trust) checked(ctx context.Context, m Meta) (good, known bool, err error) {
	if t == nil || t.State == nil {
		return false, false, nil
	}
	v, err := t.State.StateValue(ctx, factKey(m))
	if err != nil || v == "" {
		return false, false, err
	}
	return v == "ok", true, nil
}

func (t *Trust) setChecked(ctx context.Context, m Meta, good bool) error {
	if t == nil || t.State == nil {
		return nil
	}
	v := "bad"
	if good {
		v = "ok"
	}
	return t.State.SetStateValue(ctx, factKey(m), v)
}

// Add trusts a platform key by hand. The installation's own key is already trusted.
func (t *Trust) Add(ctx context.Context, pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return errKey
	}
	if t != nil && KeyID(pub) == KeyID(t.Own) {
		return nil
	}
	list, err := t.keys(ctx)
	if err != nil {
		return err
	}
	id := KeyID(pub)
	for _, item := range list {
		if KeyID(item) == id {
			return nil
		}
	}
	list = append(list, pub)
	return t.write(ctx, list)
}

// Remove drops a key that was added by hand. The installation's own key cannot be removed.
func (t *Trust) Remove(ctx context.Context, keyID string) error {
	id, err := cleanKeyID(keyID)
	if err != nil {
		return err
	}
	if t != nil && len(t.Own) == ed25519.PublicKeySize && id == KeyID(t.Own) {
		return nil
	}
	list, err := t.keys(ctx)
	if err != nil {
		return err
	}
	next := list[:0]
	for _, item := range list {
		if KeyID(item) != id {
			next = append(next, item)
		}
	}
	return t.write(ctx, next)
}

// List returns the installation key first, then the keys added by hand, then the keys that earned
// a record.
func (t *Trust) List(ctx context.Context) ([]Row, error) {
	reps, err := t.records(ctx)
	if err != nil {
		return nil, err
	}
	var out []Row
	seen := map[string]bool{}
	if t != nil && len(t.Own) == ed25519.PublicKeySize {
		id := KeyID(t.Own)
		out = append(out, Row{Fingerprint: id, Own: true})
		seen[id] = true
	}
	list, err := t.keys(ctx)
	if err != nil {
		return nil, err
	}
	for _, pub := range list {
		id := KeyID(pub)
		out = append(out, Row{Fingerprint: id, Manual: true, Verified: reps[id].OK, Wrong: reps[id].Fail})
		seen[id] = true
	}
	for id, r := range reps {
		if !seen[id] {
			out = append(out, Row{Fingerprint: id, Verified: r.OK, Wrong: r.Fail})
		}
	}
	return out, nil
}

func (t *Trust) keys(ctx context.Context) ([]ed25519.PublicKey, error) {
	if t == nil || t.State == nil {
		return nil, nil
	}
	raw, err := t.State.StateValue(ctx, trustKey)
	if err != nil || raw == "" {
		return nil, err
	}
	var encoded []string
	if err := json.Unmarshal([]byte(raw), &encoded); err != nil {
		return nil, err
	}
	var out []ed25519.PublicKey
	for _, item := range encoded {
		b, err := base64.RawURLEncoding.DecodeString(item)
		if err != nil || len(b) != ed25519.PublicKeySize {
			continue
		}
		out = append(out, ed25519.PublicKey(b))
	}
	return out, nil
}

func (t *Trust) write(ctx context.Context, list []ed25519.PublicKey) error {
	encoded := make([]string, len(list))
	for i, pub := range list {
		encoded[i] = base64.RawURLEncoding.EncodeToString(pub)
	}
	raw, err := json.Marshal(encoded)
	if err != nil {
		return err
	}
	return t.State.SetStateValue(ctx, trustKey, string(raw))
}

func (t *Trust) records(ctx context.Context) (map[string]record, error) {
	out := map[string]record{}
	if t == nil || t.State == nil {
		return out, nil
	}
	raw, err := t.State.StateValue(ctx, repKey)
	if err != nil || raw == "" {
		return out, err
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]record{}
	}
	return out, nil
}

func cleanKeyID(s string) (string, error) {
	s = strings.TrimSpace(s)
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) != 8 {
		return "", errKey
	}
	return s, nil
}

var errKey = errString("a key id is 11 characters")

type errString string

func (e errString) Error() string { return string(e) }
