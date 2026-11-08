package tgsetup

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strconv"
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
	// Owned is set when no new bot can be made because the account already has 20.
	// The caller shows them and asks which ones to reuse.
	Owned []OwnedBot
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

// Step is one line of progress. state is "wait" while the work is unfinished, "ok" only after it
// has succeeded, and "bad" when it failed. A line is never "ok" on a guess.
type Step func(text, state string)

// Create makes the channel if none is given, then the bots, and makes them admins. On a failure it
// returns what was done so far together with the error.
func (r *Runner) Create(ctx context.Context, o CreateOptions, progress Step) (Found, error) {
	if progress == nil {
		progress = func(string, string) {}
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

	fresh := strings.TrimSpace(o.Channel) == ""
	opening := "Opening the channel"
	if fresh {
		opening = "Creating the channel"
	}
	progress(opening, "wait")
	val, err := r.S.Run(ctx, func(ctx context.Context, api *tg.Client) (any, error) {
		if !fresh {
			ch, err := tguser.ResolveChannelInput(ctx, api, o.Channel)
			if err != nil {
				return nil, err
			}
			return Created{ID: ch.ID, AccessHash: ch.AccessHash, Title: ch.Title}, nil
		}
		return CreateChannel(ctx, api, title, "Library files. Managed by jellyfin-telegram.")
	})
	if err != nil {
		progress(opening, "bad")
		return found, err
	}
	ch := val.(Created)
	found.ChannelID, found.Title, hash = ch.ID, ch.Title, ch.AccessHash
	if fresh {
		progress(fmt.Sprintf("Channel %q created", ch.Title), "ok")
		progress("Moving the channel to Archive", "wait")
		if err := r.archive(ctx, ch.ID, hash); err != nil {
			progress("Moving the channel to Archive", "bad")
			return found, err
		}
		progress("Channel moved to Archive", "ok")
		// Muting is a comfort, not a requirement: a channel that stays loud is still a working library.
		progress("Muting the channel", "wait")
		if err := r.mute(ctx, ch.ID, hash); err != nil {
			progress("Could not mute the channel: "+err.Error(), "bad")
		} else {
			progress("Channel muted", "ok")
		}
	} else {
		progress(fmt.Sprintf("Channel %q found", ch.Title), "ok")
	}

	for i := 1; i <= o.Bots; i++ {
		if i > 1 {
			if err := sleep(ctx, r.Pause); err != nil {
				return found, err
			}
		}
		making := fmt.Sprintf("Creating bot %d of %d", i, o.Bots)
		progress(making, "wait")
		bot, err := r.newBot(ctx, fmt.Sprintf("%s %d", title, i), slug(title), progress)
		if errors.Is(err, ErrTooManyBots) {
			progress(making, "bad")
			progress("Listing the bots you already have", "wait")
			owned, lerr := r.ownedBots(ctx)
			if lerr != nil || len(owned) == 0 {
				progress("Listing the bots you already have", "bad")
				if lerr != nil {
					return found, fmt.Errorf("%w, and the existing bots could not be listed: %v", ErrTooManyBots, lerr)
				}
				return found, ErrTooManyBots
			}
			progress("Listing the bots you already have", "ok")
			found.Owned = excludeOwned(owned, found.Bots)
			if len(found.Owned) == 0 {
				return found, ErrTooManyBots
			}
			return found, ErrTooManyBots
		}
		if err != nil {
			progress(making, "bad")
			return found, err
		}
		progress(fmt.Sprintf("Bot @%s created", bot.Username), "ok")
		admin := fmt.Sprintf("Making @%s an admin", bot.Username)
		progress(admin, "wait")
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
			progress(admin, "bad")
			return found, err
		}
		progress(fmt.Sprintf("@%s is an admin", bot.Username), "ok")
	}
	return found, nil
}

// ReuseOptions says which existing bots replace their token and join the channel. Tokens and Bots
// are ones already made in this run; they are kept and not revoked again.
type ReuseOptions struct {
	ChannelID int64
	Title     string
	Tokens    []string
	Bots      []string
	Usernames []string
}

// Reuse revokes the token of each chosen bot and makes that bot an admin of the channel.
// Revoking is what drops the old token, so only this install can use the bot afterwards.
func (r *Runner) Reuse(ctx context.Context, o ReuseOptions, progress Step) (Found, error) {
	if progress == nil {
		progress = func(string, string) {}
	}
	names := make([]string, 0, len(o.Usernames))
	seen := map[string]bool{}
	for _, raw := range o.Usernames {
		name := strings.TrimPrefix(strings.TrimSpace(raw), "@")
		if seen[strings.ToLower(name)] {
			continue
		}
		if !reBotUsername.MatchString(name) || !strings.HasSuffix(strings.ToLower(name), "bot") {
			return Found{}, fmt.Errorf("%q cannot be a bot username", name)
		}
		seen[strings.ToLower(name)] = true
		names = append(names, name)
	}
	if len(names) < 1 || len(names)+len(o.Bots) > MaxBots {
		return Found{}, fmt.Errorf("choose between 1 and %d bots", MaxBots)
	}
	found := Found{
		ChannelID: o.ChannelID, Title: strings.TrimSpace(o.Title),
		Tokens: append([]string{}, o.Tokens...), Bots: append([]string{}, o.Bots...),
	}
	val, err := r.S.Run(ctx, func(ctx context.Context, api *tg.Client) (any, error) {
		return tguser.ResolveChannelInput(ctx, api, strconv.FormatInt(o.ChannelID, 10))
	})
	if err != nil {
		return found, err
	}
	ch := val.(*tg.Channel)
	if found.Title == "" {
		found.Title = ch.Title
	}
	have := map[string]bool{}
	for _, b := range found.Bots {
		have[strings.ToLower(b)] = true
	}
	made := 0
	for _, name := range names {
		if have[strings.ToLower(name)] {
			continue
		}
		if made > 0 {
			if err := sleep(ctx, r.Pause); err != nil {
				return found, err
			}
		}
		made++
		replacing := fmt.Sprintf("Replacing the token of @%s", name)
		progress(replacing, "wait")
		bot, err := r.revoke(ctx, name, progress)
		if err != nil {
			progress(replacing, "bad")
			return found, err
		}
		progress(fmt.Sprintf("New token for @%s", bot.Username), "ok")
		found.Tokens = append(found.Tokens, bot.Token)
		found.Bots = append(found.Bots, bot.Username)
		admin := fmt.Sprintf("Making @%s an admin", bot.Username)
		progress(admin, "wait")
		if _, err := r.S.Run(ctx, func(ctx context.Context, api *tg.Client) (any, error) {
			u, err := ResolveBot(ctx, api, bot.Username)
			if err != nil {
				return nil, err
			}
			return nil, PromoteBot(ctx, api, ch.ID, ch.AccessHash, u)
		}); err != nil {
			progress(admin, "bad")
			return found, err
		}
		progress(fmt.Sprintf("@%s is an admin", bot.Username), "ok")
	}
	return found, nil
}

// newBot makes one bot and waits out a rate limit once.
func (r *Runner) archive(ctx context.Context, id, hash int64) error {
	_, err := r.S.Run(ctx, func(ctx context.Context, api *tg.Client) (any, error) {
		return nil, ArchiveChannel(ctx, api, id, hash)
	})
	return err
}

func (r *Runner) mute(ctx context.Context, id, hash int64) error {
	_, err := r.S.Run(ctx, func(ctx context.Context, api *tg.Client) (any, error) {
		return nil, MuteChannel(ctx, api, id, hash)
	})
	return err
}

func (r *Runner) newBot(ctx context.Context, name, base string, progress Step) (Bot, error) {
	val, err := r.once(ctx, progress, func(ctx context.Context, api *tg.Client) (any, error) {
		chat, err := newBotFatherChat(ctx, api)
		if err != nil {
			return nil, err
		}
		return NewBot(ctx, chat, name, func(int) string { return username(base) })
	})
	if err != nil {
		return Bot{}, err
	}
	return val.(Bot), nil
}

func (r *Runner) revoke(ctx context.Context, username string, progress Step) (Bot, error) {
	val, err := r.once(ctx, progress, func(ctx context.Context, api *tg.Client) (any, error) {
		chat, err := newBotFatherChat(ctx, api)
		if err != nil {
			return nil, err
		}
		return Revoke(ctx, chat, username)
	})
	if err != nil {
		return Bot{}, err
	}
	return val.(Bot), nil
}

func (r *Runner) ownedBots(ctx context.Context) ([]OwnedBot, error) {
	val, err := r.S.Run(ctx, func(ctx context.Context, api *tg.Client) (any, error) {
		chat, err := newBotFatherChat(ctx, api)
		if err != nil {
			return nil, err
		}
		return ListBots(ctx, chat)
	})
	if err != nil {
		return nil, err
	}
	return val.([]OwnedBot), nil
}

// once runs fn and, if BotFather asked for a short pause, waits and tries a second time.
func (r *Runner) once(ctx context.Context, progress Step, fn func(context.Context, *tg.Client) (any, error)) (any, error) {
	if progress == nil {
		progress = func(string, string) {}
	}
	for try := 0; ; try++ {
		val, err := r.S.Run(ctx, fn)
		var rl *RateLimitError
		if errors.As(err, &rl) && try == 0 && rl.Wait <= 5*time.Minute {
			progress(fmt.Sprintf("@BotFather asked to wait %d seconds", int(rl.Wait.Seconds())), "wait")
			if err := sleep(ctx, rl.Wait+2*time.Second); err != nil {
				return nil, err
			}
			continue
		}
		return val, err
	}
}

func excludeOwned(owned []OwnedBot, have []string) []OwnedBot {
	skip := map[string]bool{}
	for _, b := range have {
		skip[strings.ToLower(b)] = true
	}
	var out []OwnedBot
	for _, b := range owned {
		if !skip[strings.ToLower(b.Username)] {
			out = append(out, b)
		}
	}
	return out
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
