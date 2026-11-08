package hashindex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// bot is a fake index bot: it records what it was sent and answers from a script.
type bot struct {
	sent    []string
	answers []string
	err     error
}

func (b *bot) TalkToBot(_ context.Context, username, text string, _ time.Duration) (string, error) {
	b.sent = append(b.sent, username+": "+text)
	if b.err != nil {
		return "", b.err
	}
	if len(b.answers) == 0 {
		return "none", nil
	}
	a := b.answers[0]
	b.answers = b.answers[1:]
	return a, nil
}

func TestQueryReadsPages(t *testing.T) {
	b := &bot{answers: []string{
		"https://t.me/Chan1/12 video #tm_tv_5_1_2\nhttps://t.me/chan2/7 document #tm_tv_5_1_1\nnext: /q #tm_tv_5_* kind:video,document 90",
		"https://t.me/chan3/3 video #tm_tv_5_2_1",
	}}
	c := &Client{Bot: "idxbot", TG: b}
	got, err := c.Query(context.Background(), "#tm_tv_5_*")
	if err != nil || len(got) != 3 {
		t.Fatalf("got %v, %v", got, err)
	}
	if got[0] != (Entry{Channel: "chan1", Message: 12, Kind: "video", Tag: "#tm_tv_5_1_2"}) {
		t.Errorf("first entry %+v", got[0])
	}
	if len(b.sent) != 2 || b.sent[0] != "idxbot: /q #tm_tv_5_* kind:video,document" || b.sent[1] != "idxbot: /q #tm_tv_5_* kind:video,document 90" {
		t.Errorf("sent %q", b.sent)
	}
}

func TestQueryStopsAtMaxPagesAndReportsErrors(t *testing.T) {
	endless := "https://t.me/chan1/1 video #tm_m_1\nnext: /q #tm_m_1 kind:video,document 5"
	b := &bot{answers: []string{endless, endless, endless, endless, endless, endless}}
	c := &Client{Bot: "idxbot", TG: b, MaxPages: 3}
	if got, err := c.Query(context.Background(), "#tm_m_1"); err != nil || len(got) != 3 || len(b.sent) != 3 {
		t.Errorf("max pages: %d entries, %d messages, %v", len(got), len(b.sent), err)
	}
	for _, reply := range []string{"over the rate limit, try again in a minute", "error: unknown kind x", "not allowed to search"} {
		c := &Client{Bot: "idxbot", TG: &bot{answers: []string{reply}}}
		if _, err := c.Query(context.Background(), "#tm_m_1"); err == nil || !strings.Contains(err.Error(), "the index answered") {
			t.Errorf("reply %q gave %v", reply, err)
		}
	}
	none := &Client{Bot: "idxbot", TG: &bot{answers: []string{"none"}}}
	if got, err := none.Query(context.Background(), "#tm_m_1"); err != nil || len(got) != 0 {
		t.Errorf("none: %v, %v", got, err)
	}
}

func TestSubmitBatches(t *testing.T) {
	b := &bot{answers: []string{"added 25, duplicate 0, rejected 0, full 0", "added 5, duplicate 0, rejected 0, full 0"}}
	c := &Client{Bot: "idxbot", TG: b}
	var items []Item
	for i := 1; i <= 30; i++ {
		items = append(items, Item{Tag: fmt.Sprintf("#tm_m_%d", i), Channel: "mirror1", Message: 100 + i})
	}
	sent, err := c.Submit(context.Background(), items)
	if err != nil || sent != 30 || len(b.sent) != 2 {
		t.Fatalf("sent %d, %d messages, %v", sent, len(b.sent), err)
	}
	if !strings.HasPrefix(b.sent[0], "idxbot: /create\n#tm_m_1 https://t.me/mirror1/101\n") || strings.Count(b.sent[0], "\n") != 25 {
		t.Errorf("first message %q", b.sent[0])
	}

	// A rate limited batch is not counted, so it is sent again later.
	limited := &bot{answers: []string{"added 25, duplicate 0, rejected 0, full 0", "added 0, duplicate 0, rejected 0, full 0, over the rate limit 5"}}
	sent, err = (&Client{Bot: "idxbot", TG: limited}).Submit(context.Background(), items)
	if sent != 25 || !errors.Is(err, ErrRateLimited) {
		t.Errorf("rate limited: %d, %v", sent, err)
	}
	// Any other answer stops the run.
	sent, err = (&Client{Bot: "idxbot", TG: &bot{answers: []string{"not allowed to add links"}}}).Submit(context.Background(), items)
	if sent != 0 || err == nil {
		t.Errorf("refused: %d, %v", sent, err)
	}
	sent, err = (&Client{Bot: "idxbot", TG: &bot{err: errors.New("the bot did not answer")}}).Submit(context.Background(), items)
	if sent != 0 || err == nil {
		t.Errorf("silent bot: %d, %v", sent, err)
	}
}

// account is a fake signed-in account.
type account struct {
	global  []tguser.SearchHit
	globErr error
	byLink  map[string]tguser.SearchHit
	asked   []tguser.PostRef
}

func (a *account) SearchHashtag(context.Context, string) ([]tguser.SearchHit, error) {
	return a.global, a.globErr
}
func (a *account) InReady(context.Context, string) (bool, error)       { return false, nil }
func (a *account) Copy(context.Context, tguser.SourceRef, []int) error { return nil }
func (a *account) PostsByLink(_ context.Context, refs []tguser.PostRef) ([]tguser.SearchHit, error) {
	a.asked = append(a.asked, refs...)
	var out []tguser.SearchHit
	for _, r := range refs {
		if h, ok := a.byLink[fmt.Sprintf("%s/%d", r.Channel, r.Message)]; ok {
			out = append(out, h)
		}
	}
	return out, nil
}

func TestSearcherMergesGlobalAndIndexHits(t *testing.T) {
	acc := &account{
		global: []tguser.SearchHit{{ChannelID: 1, MessageID: 10}},
		byLink: map[string]tguser.SearchHit{
			"chan1/10": {ChannelID: 1, MessageID: 10}, // also found by the global search
			"chan2/20": {ChannelID: 2, MessageID: 20},
			"mine/30":  {ChannelID: 3, MessageID: 30},
		},
	}
	b := &bot{answers: []string{
		"https://t.me/chan1/10 video #tm_m_603\nhttps://t.me/chan2/20 video #tm_m_603\nhttps://t.me/mine/30 video #tm_m_603",
		"none",
	}}
	s := &Searcher{Account: acc, Index: &Client{Bot: "idxbot", TG: b}, Skip: func(c string) bool { return c == "mine" }}
	hits, err := s.SearchHashtag(context.Background(), "#tm_m_603")
	if err != nil || len(hits) != 2 || hits[0].MessageID != 10 || hits[1].MessageID != 20 {
		t.Fatalf("hits %+v, %v", hits, err)
	}
	// A film is asked for twice: its own tag and the tags with language letters.
	if len(b.sent) != 2 || !strings.Contains(b.sent[0], "/q #tm_m_603 kind:") || !strings.Contains(b.sent[1], "/q #tm_m_603_* kind:") {
		t.Errorf("asked %q", b.sent)
	}
	for _, r := range acc.asked {
		if r.Channel == "mine" {
			t.Error("fetched a post of an own mirror")
		}
	}
}

func TestSearcherSurvivesFailures(t *testing.T) {
	flood := errors.New("flood wait")
	// The index is down: the global result and its error come through untouched.
	acc := &account{global: []tguser.SearchHit{{ChannelID: 1, MessageID: 10}}, globErr: flood}
	s := &Searcher{Account: acc, Index: &Client{Bot: "idxbot", TG: &bot{err: errors.New("the bot did not answer")}}}
	hits, err := s.SearchHashtag(context.Background(), "#tm_tv_5")
	if len(hits) != 1 || !errors.Is(err, flood) {
		t.Errorf("index down: %v, %v", hits, err)
	}
	// The global search failed but the index found something: the title still moves on.
	acc = &account{globErr: flood, byLink: map[string]tguser.SearchHit{"chan2/20": {ChannelID: 2, MessageID: 20}}}
	s = &Searcher{Account: acc, Index: &Client{Bot: "idxbot", TG: &bot{answers: []string{"https://t.me/chan2/20 video #tm_tv_5_1_1"}}}}
	hits, err = s.SearchHashtag(context.Background(), "#tm_tv_5")
	if len(hits) != 1 || err != nil {
		t.Errorf("global down: %v, %v", hits, err)
	}
	// No index configured: behaves like the plain account.
	plain := &Searcher{Account: &account{global: []tguser.SearchHit{{MessageID: 1}}}}
	if hits, err := plain.SearchHashtag(context.Background(), "#tm_m_1"); len(hits) != 1 || err != nil {
		t.Errorf("plain: %v, %v", hits, err)
	}
	// Only title hashtags go to the index.
	b := &bot{}
	other := &Searcher{Account: &account{}, Index: &Client{Bot: "idxbot", TG: b}}
	other.SearchHashtag(context.Background(), "#something_else")
	if len(b.sent) != 0 {
		t.Errorf("asked about a foreign hashtag: %q", b.sent)
	}
}

func TestTagIn(t *testing.T) {
	for in, want := range map[string]string{
		"The Matrix (1999)\njftg:x\n#tm_m_603": "#tm_m_603",
		"Show S01E06\n#tm_tv_231751_1_6":       "#tm_tv_231751_1_6",
		"talks about #tm_m_1 then\n#tm_m_2":    "#tm_m_2",
		"no tag here":                          "",
		"#tm_tv_5 only the series id":          "",
		"#tm_m_603_en_fa":                      "",
	} {
		if got := tagIn(in); got != want {
			t.Errorf("tagIn(%q) = %q, want %q", in, got, want)
		}
	}
}

type shares struct {
	posts  []catalog.SharePost
	marked []int
}

func (s *shares) UnsharedMirrorPosts(_ context.Context, limit int) ([]catalog.SharePost, error) {
	var out []catalog.SharePost
	for _, p := range s.posts {
		done := false
		for _, m := range s.marked {
			done = done || m == p.MessageID
		}
		if !done && len(out) < limit {
			out = append(out, p)
		}
	}
	return out, nil
}
func (s *shares) MarkShared(_ context.Context, _ int64, id int) error {
	s.marked = append(s.marked, id)
	return nil
}

func TestPublisherSharesAndRetries(t *testing.T) {
	st := &shares{posts: []catalog.SharePost{
		{MirrorID: 1, MessageID: 1, DestID: 101, Username: "mirror1", Caption: "A\n#tm_m_603"},
		{MirrorID: 1, MessageID: 2, DestID: 102, Username: "mirror1", Caption: "no hashtag here"},
		{MirrorID: 1, MessageID: 3, DestID: 103, Username: "mirror1", Caption: "B\n#tm_tv_5_1_2"},
	}}
	b := &bot{err: errors.New("the bot did not answer")}
	p := &Publisher{Store: st, Index: &Client{Bot: "idxbot", TG: b}}

	if _, err := p.Pass(context.Background()); err == nil {
		t.Fatal("a silent bot should be an error")
	}
	// The post without a tag is settled at once. The others wait for the next try.
	if len(st.marked) != 1 || st.marked[0] != 2 {
		t.Fatalf("marked %v", st.marked)
	}

	b.err = nil
	b.answers = []string{"added 2, duplicate 0, rejected 0, full 0"}
	if full, err := p.Pass(context.Background()); err != nil || full {
		t.Fatalf("second pass: %v, %v", full, err)
	}
	if len(st.marked) != 3 {
		t.Errorf("marked %v", st.marked)
	}
	last := b.sent[len(b.sent)-1]
	if last != "idxbot: /create\n#tm_m_603 https://t.me/mirror1/101\n#tm_tv_5_1_2 https://t.me/mirror1/103" {
		t.Errorf("sent %q", last)
	}
	// Nothing left: no message goes out.
	n := len(b.sent)
	if _, err := p.Pass(context.Background()); err != nil || len(b.sent) != n {
		t.Errorf("idle pass sent a message: %v", err)
	}
}
