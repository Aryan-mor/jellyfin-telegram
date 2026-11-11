package hashindex

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/hold"
)

// Shares is the queue of mirror posts to hand over.
type Shares interface {
	UnsharedMirrorPosts(ctx context.Context, limit int) ([]catalog.SharePost, error)
	MarkShared(ctx context.Context, mirrorID int64, messageID int) error
}

// Publisher hands the posts of public mirrors to the index. The queue lives in the catalog, so a
// restart loses nothing and a post is never handed over twice.
type Publisher struct {
	Store Shares
	Index *Client
	Log   *slog.Logger
	// Every is the pause between two messages. Zero means one minute, which keeps a sender under
	// the index's 30 links a minute.
	Every time.Duration
}

// A language suffix may follow the title: _<audio> or _<audio>_<sub>. "none" may fill the audio slot.
var reTag = regexp.MustCompile(`#tm_(?:m_\d+|tv_\d+_\d+_\d+)(?:_(?:none|[a-z]{2,3})(?:_[a-z]{2,3})?)?\b`)

// tagIn finds the lookup hashtag of a caption. The last one wins, as the tag ends the caption.
func tagIn(caption string) string {
	all := reTag.FindAllString(caption, -1)
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1]
}

// Run shares until ctx ends.
func (p *Publisher) Run(ctx context.Context) {
	every := p.Every
	if every <= 0 {
		every = time.Minute
	}
	for {
		var full bool
		var err error
		if !hold.Stopped() {
			work, done := hold.Begin(ctx)
			full, err = p.Pass(work)
			cancelled := work.Err() != nil
			done()
			if err != nil && ctx.Err() == nil && !cancelled && p.Log != nil {
				p.Log.Warn("hashtag index share", "err", err)
			}
			if cancelled {
				err = context.Canceled
			}
		}
		wait := every
		switch {
		case errors.Is(err, ErrRateLimited):
			wait = 2 * every
		case err == nil && !full && !hold.Stopped():
			// Nothing waits: look again later.
			wait = 5 * time.Minute
		}
		if ok, _ := hold.Sleep(ctx, wait, nil); !ok {
			return
		}
	}
}

// Pass shares one batch. It reports whether the batch was full, so more may wait.
func (p *Publisher) Pass(ctx context.Context) (bool, error) {
	posts, err := p.Store.UnsharedMirrorPosts(ctx, batch)
	if err != nil || len(posts) == 0 {
		return false, err
	}
	var items []Item
	var sharing []catalog.SharePost
	for _, post := range posts {
		tag := tagIn(post.Caption)
		if tag == "" {
			// Nothing to share for this post: do not look at it again.
			if err := p.Store.MarkShared(ctx, post.MirrorID, post.MessageID); err != nil {
				return false, err
			}
			continue
		}
		items = append(items, Item{Tag: tag, Channel: post.Username, Message: post.DestID})
		sharing = append(sharing, post)
	}
	if len(items) == 0 {
		return true, nil
	}
	sent, err := p.Index.Submit(ctx, items)
	for _, post := range sharing[:sent] {
		if merr := p.Store.MarkShared(ctx, post.MirrorID, post.MessageID); merr != nil {
			return false, merr
		}
	}
	if p.Log != nil && sent > 0 {
		p.Log.Info("shared mirror posts with the hashtag index", "count", sent)
	}
	return len(posts) == batch, err
}
