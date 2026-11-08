// Package tgsetup does the Telegram work of the automatic setup, as the signed-in user account:
// creating the channel, creating bots in @BotFather, and making them admins.
package tgsetup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	// ErrUsernameTaken means BotFather refused the username. Another one can be tried.
	ErrUsernameTaken = errors.New("that bot username is taken or not allowed")
	// ErrNotOwner means the bot does not belong to the signed-in account.
	ErrNotOwner = errors.New("this account does not own that bot")
	// ErrBotFatherSilent means @BotFather did not answer in time.
	ErrBotFatherSilent = errors.New("@BotFather did not answer")
	// ErrTooManyBots means the account is at Telegram's limit of 20 bots, so no new one can be made.
	ErrTooManyBots = errors.New("Telegram allows 20 bots on one account, and this one is full")
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
	case tooManyBots(reply):
		return ErrTooManyBots
	case strings.Contains(low, "too many attempts"), strings.Contains(low, "flood"):
		wait := 30 * time.Second
		if m := reWaitTime.FindStringSubmatch(reply); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				wait = time.Duration(n) * time.Second
			}
		}
		return &RateLimitError{Wait: wait}
	case usernameRefused(low):
		return ErrUsernameTaken
	case strings.Contains(low, "invalid bot selected"), strings.Contains(low, "can't find"),
		strings.Contains(low, "do not have any bots"), strings.Contains(low, "don't have any bots"):
		return ErrNotOwner
	}
	return nil
}

// usernameRefused reports a rejected username. The prompt that asks for one also says it must end
// in bot, and that prompt is not a refusal.
func usernameRefused(low string) bool {
	if strings.Contains(low, "choose a username") || strings.Contains(low, "انتخاب کن") {
		return false
	}
	if strings.Contains(low, "already taken") || strings.Contains(low, "username is invalid") {
		return true
	}
	if strings.Contains(low, "sorry") && strings.Contains(low, "must end") {
		return true
	}
	return strings.Contains(low, "نام کاربری") &&
		(strings.Contains(low, "گرفته") || strings.Contains(low, "نامعتبر") || strings.Contains(low, "قبلا"))
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
	for attempt := 0; attempt < 8; attempt++ {
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

// tooManyBots recognises BotFather's refusal in English and Persian. The account's language
// decides which text comes back, and both name the same limit of 20.
func tooManyBots(reply string) bool {
	low := strings.ToLower(reply)
	if strings.Contains(low, "too many bots") || strings.Contains(low, "20 bots") ||
		strings.Contains(low, "more than 20 bots") || strings.Contains(low, "maximum number of bots") ||
		strings.Contains(low, "can't add more") || strings.Contains(low, "cannot add more") ||
		strings.Contains(low, "botsupport") {
		return true
	}
	return strings.Contains(reply, "ربات‌های زیادی") || strings.Contains(reply, "ربات های زیادی") ||
		strings.Contains(reply, "بیش از ۲۰") || strings.Contains(reply, "بیش از 20") ||
		strings.Contains(reply, "۲۰ ربات")
}

// Button is one key under a BotFather message. Data is set for an inline button.
type Button struct {
	Text string
	Data []byte
}

// OwnedBot is a bot this account already created. Name is the title BotFather showed, when it
// was not just the username.
type OwnedBot struct {
	Username string
	Name     string
}

// Menu is a BotFather conversation that can read and press the buttons under a message.
type Menu interface {
	Chat
	// Marked waits for the answer after a sent message and returns its buttons too.
	Marked(ctx context.Context, after int) (text string, msgID int, buttons []Button, err error)
	// Press pushes an inline button and returns the message BotFather shows next.
	Press(ctx context.Context, msgID int, data []byte) (text string, id int, buttons []Button, err error)
}

var reMention = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_])@?([A-Za-z][A-Za-z0-9_]{1,30}bot)(?:[^A-Za-z0-9_]|$)`)

// ownedFrom reads bot usernames out of a message and its buttons. Buttons that are only a title
// come back in rest so the caller can open them.
func ownedFrom(text string, buttons []Button) (found []OwnedBot, rest []Button) {
	for _, raw := range reMention.FindAllStringSubmatch(text, -1) {
		found = append(found, OwnedBot{Username: raw[1]})
	}
	for _, b := range buttons {
		if next, back := navKind(b.Text); next || back {
			continue
		}
		if user, ok := usernameOnly(b.Text); ok {
			found = append(found, OwnedBot{Username: user})
			continue
		}
		if user, ok := usernameOnly(string(b.Data)); ok && printable(b.Data) {
			found = append(found, OwnedBot{Username: user, Name: cleanName(b.Text, user)})
			continue
		}
		for _, raw := range reMention.FindAllStringSubmatch(b.Text, -1) {
			found = append(found, OwnedBot{Username: raw[1], Name: cleanName(b.Text, raw[1])})
		}
		if len(reMention.FindAllString(b.Text, -1)) == 0 {
			rest = append(rest, b)
		}
	}
	return found, rest
}

func usernameOnly(s string) (string, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "@")
	if reBotUsername.MatchString(s) && strings.HasSuffix(strings.ToLower(s), "bot") {
		return s, true
	}
	return "", false
}

func printable(b []byte) bool {
	if len(b) == 0 || len(b) > 32 {
		return false
	}
	for _, c := range b {
		if c < 32 || c > 126 {
			return false
		}
	}
	return true
}

func cleanName(text, user string) string {
	text = strings.ReplaceAll(text, "@"+user, "")
	text = strings.ReplaceAll(text, user, "")
	text = strings.Trim(text, " -—:()[]\n\t")
	if strings.EqualFold(text, user) {
		return ""
	}
	return strings.TrimSpace(text)
}

func navKind(text string) (next, back bool) {
	t := strings.ToLower(strings.TrimSpace(text))
	switch t {
	case "»", "▶", "▶️", "next", ">", "›", "→":
		return true, false
	case "«", "◀", "◀️", "back", "<", "‹", "←":
		return false, true
	}
	switch {
	case strings.HasPrefix(t, "«"), strings.HasPrefix(t, "◀"), strings.HasPrefix(t, "back"):
		return false, true
	case strings.HasPrefix(t, "»"), strings.HasPrefix(t, "▶"), strings.HasPrefix(t, "next"):
		return true, false
	default:
		return false, false
	}
}

func findNav(buttons []Button, wantNext bool) *Button {
	for i := range buttons {
		next, back := navKind(buttons[i].Text)
		if wantNext && next || !wantNext && back {
			return &buttons[i]
		}
	}
	return nil
}

// ListBots asks @BotFather for the bots this account owns. Buttons that carry a username are
// enough; a button that is only a title is opened so the username can be read, then closed.
func ListBots(ctx context.Context, m Menu) ([]OwnedBot, error) {
	_, _ = say(ctx, m, "/cancel")
	after, err := m.Send(ctx, "/mybots")
	if err != nil {
		return nil, err
	}
	text, msgID, buttons, err := m.Marked(ctx, after)
	if err != nil {
		return nil, err
	}
	var out []OwnedBot
	seen := map[string]int{}
	add := func(list []OwnedBot) {
		for _, b := range list {
			key := strings.ToLower(b.Username)
			if key == "" || key == "botfather" {
				continue
			}
			if i, ok := seen[key]; ok {
				if out[i].Name == "" && b.Name != "" {
					out[i].Name = b.Name
				}
				continue
			}
			seen[key] = len(out)
			out = append(out, OwnedBot{Username: b.Username, Name: b.Name})
		}
	}
	presses := 0
	for page := 0; page < 6; page++ {
		if err := classify(text); err != nil && len(buttons) == 0 {
			return nil, err
		}
		found, rest := ownedFrom(text, buttons)
		add(found)
		for _, b := range rest {
			if presses >= 30 || len(b.Data) == 0 || len(out) >= 20 {
				break
			}
			presses++
			t, id, btns, perr := m.Press(ctx, msgID, b.Data)
			if perr != nil {
				continue
			}
			more, _ := ownedFrom(t, btns)
			add(more)
			back := findNav(btns, false)
			if back == nil || len(back.Data) == 0 {
				break
			}
			presses++
			t, id, btns, perr = m.Press(ctx, id, back.Data)
			if perr != nil {
				break
			}
			text, msgID, buttons = t, id, btns
		}
		next := findNav(buttons, true)
		if next == nil || len(next.Data) == 0 || len(out) >= 20 {
			break
		}
		presses++
		t, id, btns, perr := m.Press(ctx, msgID, next.Data)
		if perr != nil {
			break
		}
		text, msgID, buttons = t, id, btns
	}
	if len(out) == 0 {
		return nil, errors.New("@BotFather did not list the bots: " + firstLine(text))
	}
	return out, nil
}

// confirmPhrase is the reply BotFather demands before it will revoke a token, or "" when the
// message is not asking for one.
func confirmPhrase(reply string) string {
	low := strings.ToLower(reply)
	switch {
	case strings.Contains(low, "yes, i am totally sure"):
		return "Yes, I am totally sure."
	case strings.Contains(low, "send") && strings.Contains(low, "yes"):
		return "Yes"
	default:
		return ""
	}
}

// Revoke tells BotFather to replace the bot's token. The old token stops working, and the new
// one is returned. That is what makes a reused bot belong to this install alone.
func Revoke(ctx context.Context, c Chat, username string) (Bot, error) {
	name := strings.TrimPrefix(strings.TrimSpace(username), "@")
	_, _ = say(ctx, c, "/cancel")
	if _, err := say(ctx, c, "/revoke"); err != nil {
		return Bot{}, err
	}
	reply, err := say(ctx, c, "@"+name)
	if err != nil {
		return Bot{}, err
	}
	if phrase := confirmPhrase(reply); phrase != "" {
		reply, err = say(ctx, c, phrase)
		if err != nil {
			return Bot{}, err
		}
	}
	token, id, ok := ParseToken(reply)
	if !ok {
		return Bot{}, errors.New("@BotFather did not give a token: " + firstLine(reply))
	}
	return Bot{Username: name, Token: token, ID: id}, nil
}

var (
	talkMu sync.Mutex
	talk   []string
)

// Record keeps one line of the setup transcript. Tokens are not written.
func Record(line string) {
	line = firstLine(reToken.ReplaceAllString(strings.TrimSpace(line), "[token]"))
	if line == "" {
		return
	}
	line = time.Now().Format("15:04:05") + "  " + line
	slog.Info("setup", "log", line)
	talkMu.Lock()
	talk = append(talk, line)
	if len(talk) > 200 {
		talk = append([]string(nil), talk[len(talk)-200:]...)
	}
	talkMu.Unlock()
}

// Transcript is the setup log, oldest first.
func Transcript() []string {
	talkMu.Lock()
	defer talkMu.Unlock()
	return append([]string(nil), talk...)
}

// ResetTranscript drops the log. A new account must not see the previous one.
func ResetTranscript() {
	talkMu.Lock()
	talk = nil
	talkMu.Unlock()
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if len(line) > 120 {
		line = line[:120]
	}
	return line
}
