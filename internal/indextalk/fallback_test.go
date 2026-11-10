package indextalk

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fake struct {
	reply string
	err   error
	calls int
}

func (f *fake) TalkToBot(context.Context, string, string, time.Duration) (string, error) {
	f.calls++
	return f.reply, f.err
}

func TestFallbackUsesTheBotFirst(t *testing.T) {
	bot := &fake{reply: "ok"}
	account := &fake{reply: "from account"}
	got, err := (&Fallback{Bot: bot, Account: account}).TalkToBot(context.Background(), "index", "/add", time.Second)
	if err != nil || got != "ok" || bot.calls != 1 || account.calls != 0 {
		t.Fatalf("got %q err %v bot %d account %d", got, err, bot.calls, account.calls)
	}
}

func TestFallbackUsesTheAccountWhenBotsCannotTalk(t *testing.T) {
	bot := &fake{err: errors.New("rpc error code 400: USER_BOT_TO_BOT_DISABLED")}
	account := &fake{reply: "pending @chan film"}
	got, err := (&Fallback{Bot: bot, Account: account}).TalkToBot(context.Background(), "index", "/add", time.Second)
	if err != nil || got != "pending @chan film" || account.calls != 1 {
		t.Fatalf("got %q err %v account %d", got, err, account.calls)
	}
}

func TestFallbackDoesNotRepeatARealSendError(t *testing.T) {
	bot := &fake{err: errors.New("the bot did not answer")}
	account := &fake{reply: "ok"}
	_, err := (&Fallback{Bot: bot, Account: account}).TalkToBot(context.Background(), "index", "/q", time.Second)
	if err == nil || account.calls != 0 {
		t.Fatalf("err %v account %d", err, account.calls)
	}
}

func TestFallbackUsesTheAccountWhenNoBotIsConnected(t *testing.T) {
	account := &fake{reply: "ok"}
	got, err := (&Fallback{Account: account}).TalkToBot(context.Background(), "index", "/add", time.Second)
	if err != nil || got != "ok" || account.calls != 1 {
		t.Fatalf("got %q err %v calls %d", got, err, account.calls)
	}
}
