// Package mirror copies files from the main channel into other channels.
//
// Each mirror names the parts it wants (films, series, music, general video). Every bot is an
// admin of the mirror, and the bots take the copies in turn.
package mirror

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/gotd/td/tgerr"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/hold"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
	"github.com/Aryan-mor/jellyfin-telegram/internal/naming"
	"github.com/Aryan-mor/jellyfin-telegram/internal/pace"
	"github.com/Aryan-mor/jellyfin-telegram/internal/sign"
)

const (
	// copyGap is the short pause between two posts of one burst.
	copyGap = time.Second
)

// Service walks the mirrors until ctx ends.
type Service struct {
	Cat  *catalog.Catalog
	Copy func(ctx context.Context, messageID int, channelID int64) (int, error)
	Log  *slog.Logger
	// ReadyOnly is asked at each pass. When it is true, only posts that are finished are copied:
	// their status line says ready and so does their signed line. A post that is still being
	// reviewed, or still waits for its subtitles, is left for a later pass. Nil copies everything.
	ReadyOnly func() bool
	// Replace builds a new channel when the current mirror is banned, and points this mirror at it.
	// Nil leaves the failure recorded and tries again on a later pass.
	Replace func(ctx context.Context, m catalog.Mirror) error
	// replaced remembers a mirror that was just replaced or that failed to be replaced, so a banned
	// channel does not create a new one on every pass.
	replaced map[int64]time.Time
}

// Run copies until ctx ends. A full library is spread over many passes.
func (s *Service) Run(ctx context.Context) {
	if s.Log == nil {
		s.Log = slog.Default()
	}
	for {
		if !hold.Stopped() {
			work, done := hold.Begin(ctx)
			err := s.Pass(work)
			cancelled := work.Err() != nil
			done()
			if err != nil && ctx.Err() == nil && !cancelled {
				s.Log.Warn("mirror", "err", err)
			}
		}
		// Each pass sends a burst of 20 to 50 posts, then rests a random 10 to 60 seconds.
		// A pause or a resume ends the rest early.
		if ok, _ := hold.Sleep(ctx, pace.Pause(pace.MaxPause), nil); !ok {
			return
		}
	}
}

// Pass copies the next batch into every mirror. A file whose part the mirror does not want is
// left uncopied, so turning that part on later still sends it.
func (s *Service) Pass(ctx context.Context) error {
	if s.Copy == nil {
		return errors.New("mirror copy is not available")
	}
	mirrors, err := s.Cat.Mirrors(ctx)
	if err != nil {
		return err
	}
	for _, m := range mirrors {
		if err := s.one(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) one(ctx context.Context, m catalog.Mirror) error {
	items, err := s.Cat.Unmirrored(ctx, m.ID)
	if err != nil {
		return err
	}
	copied := 0
	copyBatch := pace.Burst()
	var failed string
	// send copies one message and records it. It reports whether Telegram refused.
	send := func(messageID int) (refused bool, err error) {
		if copied > 0 {
			if err := wait(ctx, copyGap); err != nil {
				return false, err
			}
		}
		dest, err := s.Copy(ctx, messageID, m.ChannelID)
		if err != nil {
			if channelGone(err) {
				return true, err
			}
			failed = err.Error()
			s.log().Warn("mirror copy failed", "channel", m.Title, "message", messageID, "err", err)
			return true, nil
		}
		if err := s.Cat.MarkSent(ctx, m.ID, messageID, dest); err != nil {
			return false, err
		}
		s.log().Info("mirrored", "channel", m.Title, "message", messageID, "dest", dest)
		copied++
		return false, nil
	}
	for _, it := range items {
		if copied >= copyBatch {
			break
		}
		kind := naming.Section(it.FileName, it.Caption, it.MIME)
		if !m.Wants(kind) {
			continue
		}
		if s.ReadyOnly != nil && s.ReadyOnly() && kind != naming.SectionMusic && !sign.IsReady(it.Caption) {
			continue
		}
		// A post that names a subtitle archive goes with it, so a reader of the mirror gets both. If
		// the archive has not been seen yet, the post waits for it.
		var archive media.Attachment
		hasArchive := false
		if meta, ok := sign.ParseCaption(it.Caption); ok && meta.Subs == "yes" {
			if archive, hasArchive, err = s.Cat.ArchiveOf(ctx, it.MessageID); err != nil {
				return err
			}
			if !hasArchive {
				continue
			}
		}
		refused, err := send(it.MessageID)
		if err != nil {
			if channelGone(err) {
				return s.replaceBanned(ctx, m, err)
			}
			return err
		}
		if refused {
			break
		}
		if hasArchive {
			if refused, err = send(archive.MessageID); err != nil {
				if channelGone(err) {
					return s.replaceBanned(ctx, m, err)
				}
				return err
			} else if refused {
				break
			}
		}
	}
	// An archive that turned up after its video went is sent now.
	if failed == "" && copied < copyBatch {
		late, err := s.Cat.UnmirroredArchives(ctx, m.ID)
		if err != nil {
			return err
		}
		for _, id := range late {
			if copied >= copyBatch {
				break
			}
			if refused, err := send(id); err != nil {
				if channelGone(err) {
					return s.replaceBanned(ctx, m, err)
				}
				return err
			} else if refused {
				break
			}
		}
	}
	return s.Cat.SetMirrorError(ctx, m.ID, failed)
}

// channelGone reports that Telegram will not let this account use the channel any more.
func channelGone(err error) bool {
	return tgerr.Is(err, "CHANNEL_PRIVATE", "CHANNEL_INVALID", "USER_BANNED_IN_CHANNEL", "CHANNEL_PUBLIC_GROUP_NA")
}

// replaceBanned creates one new mirror channel and stops this pass. A second ban within six hours
// is only recorded, so a failure cannot keep creating channels.
func (s *Service) replaceBanned(ctx context.Context, m catalog.Mirror, cause error) error {
	if s.replaced != nil {
		if t, ok := s.replaced[m.ID]; ok && time.Since(t) < 6*time.Hour {
			return s.Cat.SetMirrorError(ctx, m.ID, cause.Error())
		}
	}
	if s.replaced == nil {
		s.replaced = map[int64]time.Time{}
	}
	s.replaced[m.ID] = time.Now()
	if s.Replace == nil {
		return s.Cat.SetMirrorError(ctx, m.ID, cause.Error())
	}
	s.log().Warn("mirror channel is banned; creating a new one", "channel", m.Title, "err", cause)
	if err := s.Replace(ctx, m); err != nil {
		s.log().Warn("could not create a replacement mirror", "channel", m.Title, "err", err)
		return s.Cat.SetMirrorError(ctx, m.ID, "could not create a replacement channel: "+err.Error())
	}
	s.log().Info("replaced a banned mirror channel", "channel", m.Title)
	return nil
}

func (s *Service) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

func wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ParseTarget reads a channel id, a t.me/c link, or an @username.
func ParseTarget(s string) (id int64, username string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, "", errors.New("enter a channel id, @username or message link")
	}
	if id, err := config.ParseChannelID(s); err == nil {
		return id, "", nil
	}
	name := s
	name = strings.TrimPrefix(name, "https://")
	name = strings.TrimPrefix(name, "http://")
	name = strings.TrimPrefix(name, "t.me/")
	name = strings.TrimPrefix(name, "@")
	if i := strings.IndexAny(name, "/?"); i >= 0 {
		name = name[:i]
	}
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, "c") {
		return 0, "", errors.New("the channel must be a link to one of its messages, its id, or its @username")
	}
	return 0, name, nil
}
