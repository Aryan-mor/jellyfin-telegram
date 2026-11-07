package tgsetup

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/tg"

	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// Session runs a function on the signed-in account. *tguser.Manager is one.
type Session interface {
	Run(ctx context.Context, fn func(context.Context, *tg.Client) (any, error)) (any, error)
}

// Found is what setup learned or made. Tokens are secrets: they stay on the server.
type Found struct {
	ChannelID int64
	Title     string
	Tokens    []string
	// Bots are the usernames behind Tokens.
	Bots []string
	// Skipped are bot admins of the channel that this account does not own.
	Skipped []string
}

// Runner does the automatic setup steps on one account.
type Runner struct {
	S Session
	// Pause is how long to wait between two new bots, so BotFather is not hurried.
	Pause time.Duration
}

// MaxBots is how many bots one setup makes at most.
const MaxBots = 10

// Existing reads a channel that was set up before and takes back the bots this account owns.
func (r *Runner) Existing(ctx context.Context, channel string) (Found, error) {
	val, err := r.S.Run(ctx, func(ctx context.Context, api *tg.Client) (any, error) {
		ch, err := tguser.ResolveChannelInput(ctx, api, channel)
		if err != nil {
			return nil, err
		}
		admins, err := BotAdmins(ctx, api, ch.ID, ch.AccessHash)
		if err != nil {
			return nil, err
		}
		found := Found{ChannelID: ch.ID, Title: ch.Title}
		if len(admins) == 0 {
			return found, nil
		}
		chat, err := newBotFatherChat(ctx, api)
		if err != nil {
			return nil, err
		}
		for _, a := range admins {
			bot, err := TokenOf(ctx, chat, a.Username)
			switch {
			case errors.Is(err, ErrNotOwner):
				found.Skipped = append(found.Skipped, a.Username)
			case err != nil:
				return nil, err
			case bot.ID != a.ID:
				// The token belongs to another bot than the one asked for. Never trust it.
				found.Skipped = append(found.Skipped, a.Username)
			default:
				found.Tokens = append(found.Tokens, bot.Token)
				found.Bots = append(found.Bots, bot.Username)
			}
		}
		return found, nil
	})
	if err != nil {
		return Found{}, err
	}
	return val.(Found), nil
}

// CreateOptions says what to make. A Channel given as text is used as it is, otherwise a new one is made.
type CreateOptions struct {
	Title   string
	Channel string
	Bots    int
}

// Create makes the channel if none is given, then the bots, and makes them admins. On a failure it
// returns what was done so far together with the error.
func (r *Runner) Create(ctx context.Context, o CreateOptions, progress func(string)) (Found, error) {
	if progress == nil {
		progress = func(string) {}
	}
	if o.Bots < 1 || o.Bots > MaxBots {
		return Found{}, fmt.Errorf("choose between 1 and %d bots", MaxBots)
	}
	title := strings.TrimSpace(o.Title)
	if title == "" {
		title = "Jellyfin library"
	}
	var found Found
	var hash int64

	progress("Setting up the channel")
	val, err := r.S.Run(ctx, func(ctx context.Context, api *tg.Client) (any, error) {
		if strings.TrimSpace(o.Channel) != "" {
			ch, err := tguser.ResolveChannelInput(ctx, api, o.Channel)
			if err != nil {
				return nil, err
			}
			return Created{ID: ch.ID, AccessHash: ch.AccessHash, Title: ch.Title}, nil
		}
		return CreateChannel(ctx, api, title, "Library files. Managed by jellyfin-telegram.")
	})
	if err != nil {
		return found, err
	}
	ch := val.(Created)
	found.ChannelID, found.Title, hash = ch.ID, ch.Title, ch.AccessHash

	for i := 1; i <= o.Bots; i++ {
		if i > 1 {
			if err := sleep(ctx, r.Pause); err != nil {
				return found, err
			}
		}
		progress(fmt.Sprintf("Creating bot %d of %d", i, o.Bots))
		bot, err := r.newBot(ctx, fmt.Sprintf("%s %d", title, i), slug(title), progress)
		if err != nil {
			return found, err
		}
		progress(fmt.Sprintf("Making @%s an admin", bot.Username))
		// The token is kept before the admin step, so a failure there loses nothing.
		found.Tokens = append(found.Tokens, bot.Token)
		found.Bots = append(found.Bots, bot.Username)
		if _, err := r.S.Run(ctx, func(ctx context.Context, api *tg.Client) (any, error) {
			u, err := ResolveBot(ctx, api, bot.Username)
			if err != nil {
				return nil, err
			}
			return nil, PromoteBot(ctx, api, ch.ID, hash, u)
		}); err != nil {
			return found, err
		}
	}
	return found, nil
}

// newBot makes one bot and waits out a rate limit once.
func (r *Runner) newBot(ctx context.Context, name, base string, progress func(string)) (Bot, error) {
	for try := 0; ; try++ {
		val, err := r.S.Run(ctx, func(ctx context.Context, api *tg.Client) (any, error) {
			chat, err := newBotFatherChat(ctx, api)
			if err != nil {
				return nil, err
			}
			return NewBot(ctx, chat, name, func(int) string { return username(base) })
		})
		var rl *RateLimitError
		if errors.As(err, &rl) && try == 0 && rl.Wait <= 5*time.Minute {
			progress(fmt.Sprintf("@BotFather asked to wait %d seconds", int(rl.Wait.Seconds())))
			if err := sleep(ctx, rl.Wait+2*time.Second); err != nil {
				return Bot{}, err
			}
			continue
		}
		if err != nil {
			return Bot{}, err
		}
		return val.(Bot), nil
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// slug keeps the letters and digits of a title for use inside a bot username.
func slug(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
		if b.Len() >= 10 {
			break
		}
	}
	if b.Len() == 0 {
		return "library"
	}
	if c := b.String()[0]; c >= '0' && c <= '9' {
		return "l" + b.String()
	}
	return b.String()
}

// username builds a bot username that ends in "_bot" with a random middle, so two tries differ.
func username(base string) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	var raw [5]byte
	_, _ = rand.Read(raw[:])
	suffix := make([]byte, len(raw))
	for i, b := range raw {
		suffix[i] = letters[int(b)%len(letters)]
	}
	return base + "_" + string(suffix) + "_bot"
}
