package newsfeed

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/fuckjoin"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

type fakeFJ struct {
	running     bool
	submits     []string
	interrupted bool
}

func (f *fakeFJ) EnsureRunning(context.Context) error {
	f.running = true
	return nil
}
func (f *fakeFJ) Submit(text string) error {
	f.submits = append(f.submits, text)
	return nil
}
func (f *fakeFJ) View() fuckjoin.Status { return fuckjoin.Status{Running: f.running} }
func (f *fakeFJ) Interrupt()            { f.interrupted = true; f.running = false }

type scriptedSaved struct {
	fj     *fakeFJ
	report bool
}

func (s scriptedSaved) SendSaved(context.Context, string) (int, error) { return 0, nil }
func (s scriptedSaved) SavedAfter(context.Context, int) ([]tguser.Saved, error) {
	if s.fj == nil {
		return nil, nil
	}
	var out []tguser.Saved
	for i, text := range s.fj.submits {
		id := i + 1
		if strings.Contains(text, "https://") {
			out = append(out, tguser.Saved{ID: id, Text: text})
		}
		if text == "cancel" && s.report {
			out = append(out, tguser.Saved{ID: id, Text: "[Task Report] done", Post: tguser.Post{ID: id, DocID: 9}})
		}
	}
	return out, nil
}

func TestAStuckLinkIsInterrupted(t *testing.T) {
	fj := &fakeFJ{running: true}
	b := &Bridge{
		FJ: fj, TG: scriptedSaved{fj: fj}, Wait: time.Millisecond, Grace: time.Millisecond, Ack: time.Millisecond,
		Step: func(context.Context, time.Duration) error { return nil },
	}
	_, err := b.Open(context.Background(), "https://t.me/bot?start=1")
	if err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Fatal(err)
	}
	if !fj.interrupted || !contains(fj.submits, "cancel") {
		t.Fatalf("submits %v interrupted %v", fj.submits, fj.interrupted)
	}
}

func TestADroppedLinkIsSentAgain(t *testing.T) {
	fj := &fakeFJ{running: true}
	b := &Bridge{
		FJ: fj, TG: scriptedSaved{}, Wait: time.Millisecond, Grace: time.Millisecond, Ack: time.Millisecond,
		Step: func(context.Context, time.Duration) error { return nil },
	}
	_, err := b.Open(context.Background(), "https://t.me/bot?start=1")
	if err == nil || !strings.Contains(err.Error(), "did not receive") {
		t.Fatal(err)
	}
	n := 0
	for _, s := range fj.submits {
		if strings.Contains(s, "https://") {
			n++
		}
	}
	if !fj.interrupted || n != 2 {
		t.Fatalf("submits %v interrupted %v", fj.submits, fj.interrupted)
	}
}

func TestCancelUnblocksAFinishedLink(t *testing.T) {
	fj := &fakeFJ{running: true}
	b := &Bridge{
		FJ: fj, TG: scriptedSaved{fj: fj, report: true}, Wait: time.Millisecond, Grace: time.Second, Ack: time.Millisecond,
		Step: func(context.Context, time.Duration) error { return nil },
	}
	posts, err := b.Open(context.Background(), "https://t.me/bot?start=1")
	if err != nil {
		t.Fatal(err)
	}
	if fj.interrupted || len(posts) != 1 || posts[0].DocID != 9 {
		t.Fatalf("posts %+v interrupted %v", posts, fj.interrupted)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
