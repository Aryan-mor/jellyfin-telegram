package tgsetup

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// script answers each message with the next reply.
type script struct {
	replies []string
	sent    []string
	last    int
}

func (s *script) Send(_ context.Context, text string) (int, error) {
	s.sent = append(s.sent, text)
	s.last++
	return s.last, nil
}

func (s *script) Reply(context.Context, int) (string, error) {
	if len(s.replies) == 0 {
		return "", ErrBotFatherSilent
	}
	r := s.replies[0]
	s.replies = s.replies[1:]
	return r, nil
}

const tok = "123456789:AAH1234567890abcdefghijklmnopqrstuv"

func TestParseToken(t *testing.T) {
	token, id, ok := ParseToken("Done! Use this token to access the HTTP API:\n" + tok + "\nKeep it safe")
	if !ok || token != tok || id != 123456789 {
		t.Fatalf("got %q %d %v", token, id, ok)
	}
	if _, _, ok := ParseToken("no token here 12:ab"); ok {
		t.Fatal("found a token in plain text")
	}
}

func TestClassify(t *testing.T) {
	var rl *RateLimitError
	err := classify("Sorry, too many attempts. Please try again in 8 seconds.")
	if !errors.As(err, &rl) || rl.Wait != 8*time.Second {
		t.Fatalf("rate limit: %v", err)
	}
	if !errors.Is(classify("Sorry, this username is already taken. Please try something different."), ErrUsernameTaken) {
		t.Fatal("taken")
	}
	if !errors.Is(classify("Invalid bot selected."), ErrNotOwner) {
		t.Fatal("not owner")
	}
	if !errors.Is(classify("Sorry, you can't add more than 20 bots. Contact @BotSupport."), ErrTooManyBots) {
		t.Fatal("english limit")
	}
	if !errors.Is(classify("شما ربات‌های زیادی ساخته‌اید"), ErrTooManyBots) {
		t.Fatal("persian limit")
	}
	if classify("Alright, a new bot. How are we going to call it?") != nil {
		t.Fatal("a normal prompt is not a refusal")
	}
	const ask = "Good. Now let's choose a username for your bot. It must end in `bot`. Like this, for example: TetrisBot or tetris_bot."
	if classify(ask) != nil {
		t.Fatal("the username prompt was treated as a refusal, so no username would be sent")
	}
	if !errors.Is(classify("متاسفانه این نام کاربری قبلا گرفته شده است"), ErrUsernameTaken) {
		t.Fatal("persian taken")
	}
}

func TestNewBotRetriesTakenUsername(t *testing.T) {
	s := &script{replies: []string{
		"No active command to cancel.",
		"Alright, a new bot. How are we going to call it?",
		"Good. Now let's choose a username for your bot. It must end in `bot`. Like this, for example: TetrisBot or tetris_bot.",
		"Sorry, this username is already taken.",
		"Done! Congratulations on your new bot.\nUse this token to access the HTTP API:\n" + tok,
	}}
	names := []string{"first_bot", "second_bot"}
	bot, err := NewBot(context.Background(), s, "Library 1", func(i int) string { return names[i] })
	if err != nil {
		t.Fatal(err)
	}
	if bot.Username != "second_bot" || bot.Token != tok || bot.ID != 123456789 {
		t.Fatalf("got %+v", bot)
	}
	if strings.Join(s.sent, "|") != "/cancel|/newbot|Library 1|first_bot|second_bot" {
		t.Fatalf("sent %v", s.sent)
	}
}

func TestNewBotRefusesBadUsername(t *testing.T) {
	s := &script{replies: []string{"ok", "ok", "ok"}}
	if _, err := NewBot(context.Background(), s, "x", func(int) string { return "plainname" }); err == nil {
		t.Fatal("a username that does not end in bot was sent")
	}
}

func TestNewBotStopsOnRateLimit(t *testing.T) {
	s := &script{replies: []string{"ok", "Sorry, too many attempts. Please try again in 30 seconds."}}
	_, err := NewBot(context.Background(), s, "x", func(int) string { return "a_bot" })
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("got %v", err)
	}
}

func TestTokenOf(t *testing.T) {
	s := &script{replies: []string{"ok", "Choose a bot to generate a new token.", "Here is the token for the bot:\n" + tok}}
	bot, err := TokenOf(context.Background(), s, "@mine_bot")
	if err != nil || bot.Token != tok || bot.Username != "mine_bot" {
		t.Fatalf("got %+v %v", bot, err)
	}
	s = &script{replies: []string{"ok", "Choose a bot", "Invalid bot selected."}}
	if _, err := TokenOf(context.Background(), s, "theirs_bot"); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("got %v", err)
	}
}

func TestRevokeReplacesTheToken(t *testing.T) {
	s := &script{replies: []string{"ok", "Choose a bot to revoke.", "Done. New token:\n" + tok}}
	bot, err := Revoke(context.Background(), s, "@mine_bot")
	if err != nil || bot.Token != tok || bot.Username != "mine_bot" {
		t.Fatalf("got %+v %v", bot, err)
	}
	if strings.Join(s.sent, "|") != "/cancel|/revoke|@mine_bot" {
		t.Fatalf("sent %v", s.sent)
	}
}

func TestRevokeConfirmsWhenAsked(t *testing.T) {
	s := &script{replies: []string{"ok", "Choose a bot.", "To confirm, please send Yes, I am totally sure.", "Done\n" + tok}}
	bot, err := Revoke(context.Background(), s, "mine_bot")
	if err != nil || bot.Token != tok {
		t.Fatalf("got %+v %v", bot, err)
	}
	if s.sent[len(s.sent)-1] != "Yes, I am totally sure." {
		t.Fatalf("sent %v", s.sent)
	}
}

func TestOwnedFromReadsNamesAndSkipsArrows(t *testing.T) {
	found, rest := ownedFrom("Your bots, managed with @BotFather", []Button{
		{Text: "@alpha_bot"},
		{Text: "Library (@beta_bot)"},
		{Text: "Old name", Data: []byte("gamma_bot")},
		{Text: "Just a title", Data: []byte{1, 2, 3}},
		{Text: "»", Data: []byte("n")},
	})
	got := map[string]string{}
	for _, b := range found {
		got[strings.ToLower(b.Username)] = b.Name
	}
	if _, ok := got["botfather"]; ok || got["alpha_bot"] != "" || got["beta_bot"] != "Library" || got["gamma_bot"] != "Old name" {
		t.Fatalf("found %#v", found)
	}
	if _, ok := got["alpha_bot"]; !ok {
		t.Fatal("missing alpha")
	}
	if len(rest) != 1 || rest[0].Text != "Just a title" {
		t.Fatalf("rest %#v", rest)
	}
}

type menuFake struct {
	sent  []string
	page  answer
	press map[string]answer
	last  int
}

type answer struct {
	text    string
	id      int
	buttons []Button
}

func (m *menuFake) Send(_ context.Context, text string) (int, error) {
	m.sent = append(m.sent, text)
	m.last++
	return m.last, nil
}
func (m *menuFake) Reply(context.Context, int) (string, error) { return "ok", nil }
func (m *menuFake) Marked(context.Context, int) (string, int, []Button, error) {
	return m.page.text, m.page.id, m.page.buttons, nil
}
func (m *menuFake) Press(_ context.Context, _ int, data []byte) (string, int, []Button, error) {
	a, ok := m.press[string(data)]
	if !ok {
		return "", 0, nil, ErrBotFatherSilent
	}
	return a.text, a.id, a.buttons, nil
}

func TestListBotsFollowsTheNextPage(t *testing.T) {
	m := &menuFake{
		page: answer{text: "Choose", id: 4, buttons: []Button{{Text: "@alpha_bot"}, {Text: "»", Data: []byte("n")}}},
		press: map[string]answer{
			"n": {text: "Choose", id: 5, buttons: []Button{{Text: "@beta_bot"}}},
		},
	}
	got, err := ListBots(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Username != "alpha_bot" || got[1].Username != "beta_bot" {
		t.Fatalf("got %+v", got)
	}
}

func TestEnableBotToBotUsesSetBot2Bot(t *testing.T) {
	m := &menuFake{
		page: answer{text: "Choose a bot to change bot-to-bot mode.", id: 4, buttons: []Button{{Text: "@lib_bot", Data: []byte("bot")}}},
		press: map[string]answer{
			"bot": {text: "Bot to Bot communication is disabled.", id: 5, buttons: []Button{{Text: "Enable", Data: []byte("on")}}},
			"on":  {text: "Success. Bot to Bot communication enabled.", id: 6},
		},
	}
	if err := EnableBotToBot(context.Background(), m, "@lib_bot"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(m.sent, "/setbot2bot") {
		t.Fatalf("sent %v", m.sent)
	}
}

func TestEnableBotToBotPressesTheModeOn(t *testing.T) {
	m := &menuFake{
		page: answer{text: "Choose a bot", id: 4, buttons: []Button{{Text: "@lib_bot", Data: []byte("bot")}}},
		press: map[string]answer{
			"bot": {text: "What do you want to do?", id: 5, buttons: []Button{{Text: "Bot Settings", Data: []byte("set")}}},
			"set": {text: "Settings", id: 6, buttons: []Button{{Text: "Bot-to-Bot Communication", Data: []byte("b2b")}}},
			"b2b": {text: "This mode is off.", id: 7, buttons: []Button{{Text: "Enable", Data: []byte("on")}}},
			"on":  {text: "Success. Bot-to-bot communication is enabled.", id: 8},
		},
	}
	if err := EnableBotToBot(context.Background(), m, "@lib_bot"); err != nil {
		t.Fatal(err)
	}
}

func TestEnableBotToBotLeavesItWhenAlreadyOn(t *testing.T) {
	m := &menuFake{
		page: answer{text: "Choose", id: 4, buttons: []Button{{Text: "lib_bot", Data: []byte("bot")}}},
		press: map[string]answer{
			"bot": {text: "Bot", id: 5, buttons: []Button{{Text: "Bot Settings", Data: []byte("set")}}},
			"set": {text: "Settings", id: 6, buttons: []Button{{Text: "Bot to Bot Communication Mode", Data: []byte("b2b")}}},
			"b2b": {text: "Bot-to-bot communication is enabled.", id: 7, buttons: []Button{{Text: "Turn off", Data: []byte("off")}}},
		},
	}
	if err := EnableBotToBot(context.Background(), m, "lib_bot"); err != nil {
		t.Fatal(err)
	}
}

func TestEnableBotToBotRefusesAStranger(t *testing.T) {
	m := &menuFake{page: answer{text: "Choose", id: 4, buttons: []Button{{Text: "@other_bot", Data: []byte("o")}}}}
	if err := EnableBotToBot(context.Background(), m, "lib_bot"); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("err = %v", err)
	}
}

func TestListBotsOpensATitle(t *testing.T) {
	m := &menuFake{
		page: answer{text: "Choose", id: 4, buttons: []Button{
			{Text: "Library", Data: []byte("open")},
			{Text: "Other", Data: []byte("open2")},
		}},
		press: map[string]answer{
			"open":  {text: "Selected @lib_one_bot", id: 8, buttons: []Button{{Text: "« Back", Data: []byte("back")}}},
			"open2": {text: "Selected @other_bot", id: 9, buttons: []Button{{Text: "« Back", Data: []byte("back")}}},
			"back": {text: "Choose", id: 4, buttons: []Button{
				{Text: "Library", Data: []byte("open")},
				{Text: "Other", Data: []byte("open2")},
			}},
		},
	}
	got, err := ListBots(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Username != "lib_one_bot" || got[1].Username != "other_bot" {
		t.Fatalf("got %+v", got)
	}
}
