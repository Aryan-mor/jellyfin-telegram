package sign

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
)

// ErrMismatch is what a Verify function returns when the file is not what its caption says. Any
// other error means the check could not be made, and says nothing about the post.
var ErrMismatch = errors.New("the file does not match its caption")

// defaultSpotRate is how often a post of a reputable key is checked anyway.
const defaultSpotRate = 0.05

// Checker decides whether a signed post may enter this library.
//
// Nobody has to vouch for a key. The signature proves who signed, and the key travels in the line,
// so any installation can check it. Whether to believe what the line says is decided by looking at
// the file: the first time a key is seen, and for a while after, every post is compared with its
// file by Verify. A key whose posts keep being right is checked only now and then, and a key whose
// posts are wrong is ignored for a day.
type Checker struct {
	// Key is this installation's key. Its own posts need no check.
	Key   *Key
	Trust *Trust
	// Verify compares a post with its file, cheaply: the first and last blocks, and the head of the
	// file for the running time and the tracks. It returns an error wrapping ErrMismatch when the
	// file is not what the caption says. Nil means no check can be made, and a key that is not
	// trusted already is refused.
	Verify func(ctx context.Context, f media.File, m Meta) error
	Log    *slog.Logger
	// Now, Rand and SpotRate are for tests. Zero values use the clock, a random number and 5%.
	Now      func() time.Time
	Rand     func() float64
	SpotRate float64
}

// Allow reports that f is a signed post this installation may keep.
// An error means the answer is not known, for instance because Telegram did not answer. The
// caller must then leave the file as it is and ask again later, never treat it as refused.
func (c *Checker) Allow(ctx context.Context, f media.File) (bool, error) {
	meta, ok := ParseCaption(f.Caption)
	if !ok {
		c.log("skipped unsigned", f.MessageID, "")
		return false, nil
	}
	if meta.DocID != f.DocID || meta.Size != f.Size {
		c.log("skipped, signature does not match the file", f.MessageID, meta.KeyID)
		return false, nil
	}
	if !meta.Authentic() {
		c.log("skipped, signature failed", f.MessageID, meta.KeyID)
		return false, nil
	}
	if c.Key != nil && meta.KeyID == c.Key.KeyID() {
		return true, nil
	}
	now := c.now()
	standing, err := c.Trust.Standing(ctx, meta.KeyID, now)
	if err != nil {
		return false, err
	}
	switch standing {
	case StandingBlocked:
		c.log("skipped, this key was wrong too often lately", f.MessageID, meta.KeyID)
		return false, nil
	case StandingTrusted:
		return true, nil
	}
	if good, known, err := c.Trust.checked(ctx, meta); err != nil {
		return false, err
	} else if known {
		return good, nil
	}
	if standing == StandingReputable && c.roll() >= c.spotRate() {
		// Not picked for a check. The post counts, and is not asked about again.
		return true, c.Trust.setChecked(ctx, meta, true)
	}
	if c.Verify == nil {
		c.log("skipped, a key nobody knows and no way to check it", f.MessageID, meta.KeyID)
		return false, nil
	}
	switch err := c.Verify(ctx, f, meta); {
	case err == nil:
		c.log("checked against the file, right", f.MessageID, meta.KeyID)
		if err := c.Trust.Record(ctx, meta.KeyID, true, now); err != nil {
			return false, err
		}
		return true, c.Trust.setChecked(ctx, meta, true)
	case errors.Is(err, ErrMismatch):
		c.log("checked against the file, wrong: "+err.Error(), f.MessageID, meta.KeyID)
		if err := c.Trust.Record(ctx, meta.KeyID, false, now); err != nil {
			return false, err
		}
		return false, c.Trust.setChecked(ctx, meta, false)
	default:
		return false, err
	}
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Checker) roll() float64 {
	if c.Rand != nil {
		return c.Rand()
	}
	return rand.Float64()
}

func (c *Checker) spotRate() float64 {
	if c.SpotRate > 0 {
		return c.SpotRate
	}
	return defaultSpotRate
}

func (c *Checker) log(msg string, messageID int, keyID string) {
	if c.Log == nil {
		return
	}
	if keyID != "" {
		c.Log.Info(msg, "message", messageID, "key", keyID)
		return
	}
	c.Log.Info(msg, "message", messageID)
}
