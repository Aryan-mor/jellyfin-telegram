// Package mirror copies files from the main channel into other channels.
//
// Each mirror names the parts it wants (films, series, music, general video) and the bot that
// posts them. That bot has to be an admin of both channels.
package mirror

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/naming"
)

const (
	// copyBatch is how many messages one pass posts into one mirror.
	copyBatch = 25
	// copyGap is the pause between two posts, so a mirror does not trip Telegram's flood limit
	// on its own.
	copyGap = 2 * time.Second
)

// Service walks the mirrors until ctx ends.
type Service struct {
	Cat  *catalog.Catalog
	Copy func(ctx context.Context, botID int64, messageID int, channelID int64) (int, error)
	Log  *slog.Logger
}

// Run copies until ctx ends. A full library is spread over many passes.
func (s *Service) Run(ctx context.Context) {
	if s.Log == nil {
		s.Log = slog.Default()
	}
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		if err := s.Pass(ctx); err != nil && ctx.Err() == nil {
			s.Log.Warn("mirror", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
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
	var failed string
	for _, it := range items {
		if !m.Wants(naming.Section(it.FileName, it.Caption, it.MIME)) {
			continue
		}
		if copied > 0 {
			if err := wait(ctx, copyGap); err != nil {
				return err
			}
		}
		dest, err := s.Copy(ctx, m.BotID, it.MessageID, m.ChannelID)
		if err != nil {
			failed = err.Error()
			s.log().Warn("mirror copy failed", "channel", m.Title, "message", it.MessageID, "err", err)
			break
		}
		if err := s.Cat.MarkSent(ctx, m.ID, it.MessageID, dest); err != nil {
			return err
		}
		s.log().Info("mirrored", "channel", m.Title, "message", it.MessageID, "dest", dest)
		copied++
		if copied >= copyBatch {
			break
		}
	}
	return s.Cat.SetMirrorError(ctx, m.ID, failed)
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
