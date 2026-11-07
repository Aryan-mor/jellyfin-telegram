// Package tgsetup does the Telegram work of the automatic setup, as the signed-in user account:
// creating the channel, creating bots in @BotFather, and making them admins.
package tgsetup

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrUsernameTaken means BotFather refused the username. Another one can be tried.
	ErrUsernameTaken = errors.New("that bot username is taken or not allowed")
	// ErrNotOwner means the bot does not belong to the signed-in account.
	ErrNotOwner = errors.New("this account does not own that bot")
	// ErrBotFatherSilent means @BotFather did not answer in time.
	ErrBotFatherSilent = errors.New("@BotFather did not answer")
)

// RateLimitError means BotFather asked for a pause.
type RateLimitError struct{ Wait time.Duration }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("@BotFather asks to wait %d seconds before the next bot", int(e.Wait.Seconds()))
}

// Chat is the conversation with @BotFather. The real one talks to Telegram, tests use a script.
type Chat interface {
	// Send writes a message and returns its id.
	Send(ctx context.Context, text string) (int, error)
	// Reply waits for the first message from @BotFather newer than after, and returns its text.
	Reply(ctx context.Context, after int) (string, error)
}

var (
	reToken       = regexp.MustCompile(`(\d{6,12}):([A-Za-z0-9_-]{30,})`)
	reWaitTime    = regexp.MustCompile(`(?i)(?:try again in|wait|retry after)\D{0,12}(\d+)\s*(?:second|sec|s\b)`)
	reBotUsername = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{4,31}$`)
)

// ParseToken finds a bot token in a BotFather message.
func ParseToken(text string) (token string, botID int64, ok bool) {
	m := reToken.FindStringSubmatch(text)
	if m == nil {
		return "", 0, false
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return m[1] + ":" + m[2], id, true
}

// classify turns a BotFather refusal into an error. It returns nil for any other text.
func classify(reply string) error {
	low := strings.ToLower(reply)
	switch {
	case strings.Contains(low, "too many attempts"), strings.Contains(low, "flood"):
		wait := 30 * time.Second
		if m := reWaitTime.FindStringSubmatch(reply); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				wait = time.Duration(n) * time.Second
			}
		}
		return &RateLimitError{Wait: wait}
	case strings.Contains(low, "already taken"), strings.Contains(low, "username is invalid"),
		strings.Contains(low, "must end in"), strings.Contains(low, "username must"):
		return ErrUsernameTaken
	case strings.Contains(low, "invalid bot selected"), strings.Contains(low, "can't find"),
		strings.Contains(low, "do not have any bots"), strings.Contains(low, "don't have any bots"):
		return ErrNotOwner
	}
	return nil
}

// say sends one message and returns BotFather's answer. A refusal comes back as the error.
func say(ctx context.Context, c Chat, text string) (string, error) {
	id, err := c.Send(ctx, text)
	if err != nil {
		return "", err
	}
	reply, err := c.Reply(ctx, id)
	if err != nil {
		return "", err
	}
	if err := classify(reply); err != nil {
		return reply, err
	}
	return reply, nil
}

// Bot is a bot BotFather made or revealed.
type Bot struct {
	Username string
	Token    string
	ID       int64
}

// NewBot makes a bot. candidates gives the usernames to try in order; each must end in "bot".
func NewBot(ctx context.Context, c Chat, name string, candidates func(attempt int) string) (Bot, error) {
	// A half-finished earlier conversation would swallow the next message.
	_, _ = say(ctx, c, "/cancel")
	if _, err := say(ctx, c, "/newbot"); err != nil {
		return Bot{}, err
	}
	if _, err := say(ctx, c, name); err != nil {
		return Bot{}, err
	}
	for attempt := 0; attempt < 6; attempt++ {
		username := candidates(attempt)
		if !reBotUsername.MatchString(username) || !strings.HasSuffix(strings.ToLower(username), "bot") {
			return Bot{}, fmt.Errorf("%q cannot be a bot username", username)
		}
		reply, err := say(ctx, c, username)
		if errors.Is(err, ErrUsernameTaken) {
			continue
		}
		if err != nil {
			return Bot{}, err
		}
		token, id, ok := ParseToken(reply)
		if !ok {
			return Bot{}, errors.New("@BotFather did not give a token: " + firstLine(reply))
		}
		return Bot{Username: username, Token: token, ID: id}, nil
	}
	return Bot{}, ErrUsernameTaken
}

// TokenOf asks BotFather for the token of a bot this account owns.
func TokenOf(ctx context.Context, c Chat, username string) (Bot, error) {
	_, _ = say(ctx, c, "/cancel")
	if _, err := say(ctx, c, "/token"); err != nil {
		return Bot{}, err
	}
	reply, err := say(ctx, c, "@"+strings.TrimPrefix(username, "@"))
	if err != nil {
		return Bot{}, err
	}
	token, id, ok := ParseToken(reply)
	if !ok {
		return Bot{}, ErrNotOwner
	}
	return Bot{Username: strings.TrimPrefix(username, "@"), Token: token, ID: id}, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if len(line) > 120 {
		line = line[:120]
	}
	return line
}
