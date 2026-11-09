package tgsearch

import (
	"context"
	"errors"
	"testing"

	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

func TestChoosePrefersASignedVideoAndSkipsTheLibrary(t *testing.T) {
	q := Query{TMDB: 603, Movie: true}
	hits := []tguser.SearchHit{
		{ChannelID: 9, MessageID: 1, Caption: "plain #tm_m_603", Size: 90, Video: true},
		{ChannelID: 7, MessageID: 2, Caption: "other #tm_m_6030", Size: 500, Video: true},
		{ChannelID: 1, MessageID: 3, Caption: "ours #tm_m_603\njftg:2;s=1", Size: 10, Video: true},
		{ChannelID: 4, MessageID: 4, Caption: "signed #tm_m_603\njftg:2;s=1", Size: 20, Video: true},
		{ChannelID: 5, MessageID: 5, Caption: "bigger #tm_m_603\njftg:2;s=1", Size: 80, Video: false, NoForward: true},
	}
	hit, tag, ok := Choose(q, hits, nil, 1)
	if !ok || tag != "#tm_m_603" || hit.MessageID != 4 {
		t.Fatalf("got %+v %q %v", hit, tag, ok)
	}
}

func TestChooseAcceptsALanguageSuffixAndNotAnIdPrefix(t *testing.T) {
	hit := tguser.SearchHit{ChannelID: 2, MessageID: 6, Caption: "ep\n#tm_tv_231751_1_6_en_fa", Video: true, Size: 10}
	_, tag, ok := Choose(Query{TMDB: 231751}, []tguser.SearchHit{hit}, nil, 0)
	if !ok || tag != "#tm_tv_231751_1_6_en_fa" {
		t.Fatalf("got %q %v", tag, ok)
	}
	if _, _, ok := Choose(Query{TMDB: 2317}, []tguser.SearchHit{hit}, nil, 0); ok {
		t.Fatal("a shorter series id matched")
	}
	movie := tguser.SearchHit{ChannelID: 2, MessageID: 1, Caption: "film\n#tm_m_603_en", Video: true, Size: 10}
	_, tag, ok = Choose(Query{TMDB: 603, Movie: true}, []tguser.SearchHit{movie}, nil, 0)
	if !ok || tag != "#tm_m_603_en" {
		t.Fatalf("movie %q %v", tag, ok)
	}
	if _, _, ok := Choose(Query{TMDB: 603, Movie: true}, []tguser.SearchHit{{ChannelID: 2, MessageID: 2, Caption: "#tm_m_6030_en", Video: true}}, nil, 0); ok {
		t.Fatal("a longer film id matched")
	}
}

func TestChooseTakesEachEpisodeOnce(t *testing.T) {
	q := Query{TMDB: 1396}
	hits := []tguser.SearchHit{
		{ChannelID: 2, MessageID: 8, Caption: "a #tm_tv_1396_1_2", Video: true, Size: 10},
		{ChannelID: 2, MessageID: 9, Caption: "b #tm_tv_1396_1_3", Video: true, Size: 10},
		{ChannelID: 3, MessageID: 1, Caption: "no #tm_tv_13960_1_2", Video: true, Size: 99},
	}
	_, first, ok := Choose(q, hits, nil, 0)
	if !ok || first != "#tm_tv_1396_1_2" {
		t.Fatal(first, ok)
	}
	_, second, ok := Choose(q, hits, map[string]bool{first: true}, 0)
	if !ok || second != "#tm_tv_1396_1_3" {
		t.Fatal(second, ok)
	}
}

type fakeTG struct {
	hits  []tguser.SearchHit
	ready map[string]bool
	got   []int
}

func (f *fakeTG) SearchHashtag(context.Context, string) ([]tguser.SearchHit, error) {
	return f.hits, nil
}
func (f *fakeTG) InReady(_ context.Context, tag string) (bool, error) {
	return f.ready[tag], nil
}
func (f *fakeTG) Copy(_ context.Context, _ tguser.SourceRef, ids []int) error {
	f.got = append(f.got, ids...)
	return nil
}

type oneWant struct{}

func (oneWant) Queries(context.Context) ([]Query, error) {
	return []Query{{TMDB: 603, Movie: true}}, nil
}

func TestPassCopiesTheFoundFile(t *testing.T) {
	tg := &fakeTG{hits: []tguser.SearchHit{{ChannelID: 4, AccessHash: 8, MessageID: 15, Caption: "The Matrix\n#tm_m_603", Video: true, Size: 30}}}
	s := &Service{TG: tg, Want: oneWant{}, On: func() bool { return true }}
	s.pass(context.Background())
	if len(tg.got) != 1 || tg.got[0] != 15 {
		t.Fatalf("copied %v", tg.got)
	}
	s.pass(context.Background())
	if len(tg.got) != 1 {
		t.Fatal("copied again")
	}
}

func TestPassStopsWhenTheSwitchIsOff(t *testing.T) {
	tg := &fakeTG{hits: []tguser.SearchHit{{ChannelID: 4, MessageID: 15, Caption: "#tm_m_603", Video: true}}}
	s := &Service{TG: tg, Want: oneWant{}, On: func() bool { return false }}
	s.pass(context.Background())
	if tg.got != nil {
		t.Fatal(tg.got)
	}
}

func TestPassStopsAfterASearchError(t *testing.T) {
	tg := &errTG{}
	s := &Service{TG: tg, Want: oneWant{}, On: func() bool { return true }}
	s.pass(context.Background())
}

type errTG struct{ fakeTG }

func (e *errTG) SearchHashtag(context.Context, string) ([]tguser.SearchHit, error) {
	return nil, errors.New("flood")
}
