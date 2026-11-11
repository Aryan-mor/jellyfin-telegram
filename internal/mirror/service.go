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
	"sync"
	"time"

	"github.com/gotd/td/tgerr"
	"golang.org/x/sync/errgroup"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/hold"
	"github.com/Aryan-mor/jellyfin-telegram/internal/naming"
	"github.com/Aryan-mor/jellyfin-telegram/internal/sign"
)

const (
	// copyGap is the short pause between two posts of one burst.
	copyGap = time.Second
	// mirrorBurst is how many posts one pass sends. Bots share the copies and already rest
	// between channel calls, so this is larger than a user-account forward.
	mirrorBurst = 30
	// mirrorRest is the pause after a pass. The bot pool's own gap is what avoids a flood.
	mirrorRest = 5 * time.Second
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
	// Width is how many copies run at once. Nil, or a number below 2, keeps one copy at a time.
	// Each copy still takes the next free bot, so the width is the bot count.
	Width func() int
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
		// Each pass sends up to mirrorBurst posts. Bots take those copies; the user account is not used.
		// A pause or a resume ends the rest early.
		if ok, _ := hold.Sleep(ctx, mirrorRest, nil); !ok {
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
	var units [][]int
	room := mirrorBurst
	push := func(ids ...int) {
		if room <= 0 || len(ids) == 0 {
			return
		}
		if len(ids) > room {
			ids = append([]int(nil), ids[:room]...)
		} else {
			ids = append([]int(nil), ids...)
		}
		units = append(units, ids)
		room -= len(ids)
	}
	for _, it := range items {
		if room <= 0 {
			break
		}
		kind := naming.Section(it.FileName, it.Caption, it.MIME)
		if !m.Wants(kind) {
			continue
		}
		if s.ReadyOnly != nil && s.ReadyOnly() && kind != naming.SectionMusic && !sign.IsReady(it.Caption) {
			continue
		}
		// The video goes now. Its subtitle archive, when one is named and already stored, goes
		// straight after it on the same bot. An archive that is not here yet follows on a later
		// pass, the same way a subtitle is delivered apart from the film.
		ids := []int{it.MessageID}
		if meta, ok := sign.ParseCaption(it.Caption); ok && meta.Subs == "yes" {
			archive, hasArchive, err := s.Cat.ArchiveOf(ctx, it.MessageID)
			if err != nil {
				return err
			}
			if hasArchive {
				ids = append(ids, archive.MessageID)
			}
		}
		push(ids...)
	}
	if room > 0 {
		late, err := s.Cat.UnmirroredArchives(ctx, m.ID)
		if err != nil {
			return err
		}
		for _, id := range late {
			if room <= 0 {
				break
			}
			push(id)
		}
	}
	var failed string
	if err := s.dispatch(ctx, m, units, &failed); err != nil {
		return err
	}
	return s.Cat.SetMirrorError(ctx, m.ID, failed)
}

// dispatch runs each unit on its own. A unit is one video and, when it is already stored, its
// subtitle archive, so the archive stays next to its film. Units run together up to Width.
func (s *Service) dispatch(ctx context.Context, m catalog.Mirror, units [][]int, failed *string) error {
	if len(units) == 0 {
		return nil
	}
	width := 1
	if s.Width != nil {
		if n := s.Width(); n > 1 {
			width = n
		}
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(width)
	var mu sync.Mutex
	var gone error
	for _, unit := range units {
		g.Go(func() error {
			for i, id := range unit {
				if i > 0 {
					if err := wait(gctx, copyGap); err != nil {
						return err
					}
				}
				dest, err := s.Copy(gctx, id, m.ChannelID)
				if err != nil {
					if channelGone(err) {
						mu.Lock()
						if gone == nil {
							gone = err
						}
						mu.Unlock()
						return err
					}
					if gctx.Err() != nil {
						return gctx.Err()
					}
					mu.Lock()
					if *failed == "" {
						*failed = err.Error()
					}
					mu.Unlock()
					s.log().Warn("mirror copy failed", "channel", m.Title, "message", id, "err", err)
					return err
				}
				if err := s.Cat.MarkSent(gctx, m.ID, id, dest); err != nil {
					return err
				}
				s.log().Info("mirrored", "channel", m.Title, "message", id, "dest", dest)
			}
			return nil
		})
	}
	err := g.Wait()
	if gone != nil {
		return s.replaceBanned(ctx, m, gone)
	}
	if err != nil && *failed == "" && ctx.Err() == nil {
		return err
	}
	return nil
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
