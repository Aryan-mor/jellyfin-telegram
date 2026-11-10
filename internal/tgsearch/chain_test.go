package tgsearch

import (
	"context"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

type chainTG struct {
	fakeTG
	channel []tguser.SearchHit
	posts   []tguser.TextPost
	queries []string
}

func (c *chainTG) SearchChannel(_ context.Context, _ tguser.SourceRef, query string, _ int) ([]tguser.SearchHit, error) {
	c.queries = append(c.queries, "channel:"+query)
	return c.channel, nil
}

func (c *chainTG) SearchPosts(_ context.Context, _ tguser.SourceRef, query string, _ int) ([]tguser.TextPost, error) {
	c.queries = append(c.queries, "news:"+query)
	return c.posts, nil
}

type memLists struct {
	sources []catalog.Source
	news    []catalog.Source
}

func (m memLists) Sources(context.Context) ([]catalog.Source, error)      { return m.sources, nil }
func (m memLists) NewsChannels(context.Context) ([]catalog.Source, error) { return m.news, nil }

type fakeNews struct {
	posts    []tguser.Post
	opened   []string
	finished []int
}

func (f *fakeNews) OpenStart(_ context.Context, _ catalog.Source, hit tguser.StartHit) ([]tguser.Post, error) {
	f.opened = append(f.opened, hit.URL)
	return f.posts, nil
}

func (f *fakeNews) FinishStart(_ context.Context, _ catalog.Source, _ tguser.StartHit, posts []tguser.Post) error {
	f.finished = append(f.finished, len(posts))
	return nil
}

func film() Query {
	return Query{TMDB: 969681, Movie: true, Year: 2026, Title: "Spider-Man: Brand New Day", IMDb: "tt22084616"}
}

func TestPassOrderSkipsLaterStepsAfterACopy(t *testing.T) {
	tg := &chainTG{fakeTG: fakeTG{hits: []tguser.SearchHit{{
		ChannelID: 4, MessageID: 15, Caption: "Spider-Man: Brand New Day\n#tm_m_969681", Video: true, Size: 30,
	}}}}
	news := &fakeNews{}
	s := &Service{
		TG: tg, Want: listWant{film()}, Lists: memLists{
			sources: []catalog.Source{{ID: 1, ChannelID: 42, Enabled: true}},
			news:    []catalog.Source{{ID: 2, ChannelID: 77, Enabled: true}},
		},
		News: news, On: func() bool { return true }, Limits: Limits{QueryCooldown: -1},
	}
	s.pass(context.Background())
	if len(tg.got) != 1 || len(tg.queries) != 0 || len(news.opened) != 0 {
		t.Fatalf("copied %v queries %v opened %v", tg.got, tg.queries, news.opened)
	}
}

func TestPassSearchesDirectOnlyAfterAnEmptyHashtag(t *testing.T) {
	tg := &chainTG{channel: []tguser.SearchHit{{
		ChannelID: 42, AccessHash: 9, MessageID: 8,
		Name: "Spider-Man.Brand.New.Day.2026.1080p.mkv", Size: 800 << 20, Duration: 8000, Video: true,
	}}}
	news := &fakeNews{}
	s := &Service{
		TG: tg, Want: listWant{film()}, Lists: memLists{
			sources: []catalog.Source{{ID: 1, ChannelID: 42, Enabled: true}},
			news:    []catalog.Source{{ID: 2, ChannelID: 77, Enabled: true}},
		},
		News: news, On: func() bool { return true }, Limits: Limits{QueryCooldown: -1, QueryGap: time.Millisecond},
		Sleep: func(context.Context, time.Duration) error { return nil },
	}
	s.pass(context.Background())
	if len(tg.got) != 1 || tg.got[0] != 8 {
		t.Fatalf("copied %v", tg.got)
	}
	if len(news.opened) != 0 {
		t.Fatal("news ran after a direct copy")
	}
	if len(tg.queries) == 0 || tg.queries[0] != "channel:tt22084616" {
		t.Fatal(tg.queries)
	}
}

func TestPassSearchesNewsWhenDirectHasNothing(t *testing.T) {
	tg := &chainTG{posts: []tguser.TextPost{{
		ChannelID: 77, MessageID: 3,
		Text:  "Spider-Man: Brand New Day (2026)",
		Links: []string{"https://t.me/getbot?start=abc"},
	}}}
	news := &fakeNews{posts: []tguser.Post{{
		ID: 4, Name: "Spider-Man.Brand.New.Day.2026.mkv", Size: 900 << 20, Duration: 8000 * time.Second,
	}}}
	s := &Service{
		TG: tg, Want: listWant{film()}, Lists: memLists{
			sources: []catalog.Source{{ID: 1, ChannelID: 42, Enabled: true}},
			news:    []catalog.Source{{ID: 2, ChannelID: 77, Enabled: true}},
		},
		News: news, On: func() bool { return true }, Limits: Limits{QueryCooldown: -1},
		Sleep: func(context.Context, time.Duration) error { return nil },
	}
	s.pass(context.Background())
	if len(tg.got) != 0 {
		t.Fatal("news path copied from the channel")
	}
	if len(news.opened) != 1 || news.finished[0] != 1 {
		t.Fatalf("opened %v finished %v", news.opened, news.finished)
	}
	if !containsQuery(tg.queries, "channel:tt22084616") || !containsQuery(tg.queries, "news:tt22084616") {
		t.Fatal(tg.queries)
	}
}

func TestSeriesStayOnTheHashtagStep(t *testing.T) {
	tg := &chainTG{}
	s := &Service{
		TG: tg, Want: listWant{{TMDB: 1396}}, Lists: memLists{
			sources: []catalog.Source{{ID: 1, ChannelID: 42, Enabled: true}},
		},
		On: func() bool { return true }, Limits: Limits{QueryCooldown: -1},
	}
	s.pass(context.Background())
	if len(tg.queries) != 0 {
		t.Fatal(tg.queries)
	}
}

func containsQuery(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
