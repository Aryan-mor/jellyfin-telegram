package chanindex

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type bot struct {
	sent    []string
	answers []string
}

func (b *bot) TalkToBot(_ context.Context, username, text string, _ time.Duration) (string, error) {
	b.sent = append(b.sent, username+": "+text)
	a := b.answers[0]
	b.answers = b.answers[1:]
	return a, nil
}

func TestSearchParsesAPage(t *testing.T) {
	b := &bot{answers: []string{`list 4812 tag news page 2/7
1 @somechannel +14 18300 Title text
2 c/1029384756 +9 2100 Private title
next: /search news 3`}}
	c := &Client{Bot: "idx", TG: b}
	page, err := c.Search(context.Background(), "news", 2)
	if err != nil {
		t.Fatal(err)
	}
	if page.ListID != 4812 || page.Tag != "news" || page.Page != 2 || page.Pages != 7 || len(page.Items) != 2 {
		t.Fatalf("%+v", page)
	}
	if page.Items[0].Ref != "@somechannel" || page.Items[0].Score != 14 || page.Items[0].Members != 18300 || page.Items[0].Title != "Title text" {
		t.Fatalf("%+v", page.Items[0])
	}
	if page.Items[1].Ref != "c/1029384756" || page.Items[1].Title != "Private title" {
		t.Fatalf("%+v", page.Items[1])
	}
	if b.sent[0] != "idx: /search news 2" {
		t.Fatal(b.sent)
	}
}

func TestSearchNone(t *testing.T) {
	b := &bot{answers: []string{"none"}}
	page, err := (&Client{Bot: "idx", TG: b}).Search(context.Background(), "news", 1)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("%+v %v", page, err)
	}
	if b.sent[0] != "idx: /search news" {
		t.Fatal(b.sent)
	}
}

func TestVoteUsesTheChannelForm(t *testing.T) {
	b := &bot{answers: []string{"ok news @somechannel +1 score 15"}}
	if err := (&Client{Bot: "idx", TG: b}).Vote(context.Background(), "@somechannel", "news", 1); err != nil {
		t.Fatal(err)
	}
	if b.sent[0] != "idx: /accept @somechannel news" {
		t.Fatal(b.sent)
	}
	b.answers = []string{"ok direct-file c/5 -1 score 0"}
	if err := (&Client{Bot: "idx", TG: b}).Vote(context.Background(), "c/5", "direct-file", -1); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(b.sent[1], "/reject c/5 direct-file") {
		t.Fatal(b.sent)
	}
}

func TestIntroduce(t *testing.T) {
	b := &bot{answers: []string{"pending @pub movie series"}}
	err := (&Client{Bot: "idx", TG: b}).Introduce(context.Background(), "pub", []string{"series", "movie"})
	if err != nil {
		t.Fatal(err)
	}
	if b.sent[0] != "idx: /add @pub movie series" {
		t.Fatal(b.sent)
	}
	b.answers = []string{"limited"}
	if err := (&Client{Bot: "idx", TG: b}).Introduce(context.Background(), "pub", []string{"news"}); !errors.Is(err, ErrLimited) {
		t.Fatal(err)
	}
	b.answers = []string{"no"}
	if err := (&Client{Bot: "idx", TG: b}).Introduce(context.Background(), "pub", []string{"news"}); !errors.Is(err, ErrRejected) {
		t.Fatal(err)
	}
}
