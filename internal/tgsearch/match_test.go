package tgsearch

import (
	"testing"

	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

func spider() Query {
	return Query{
		TMDB: 969681, Movie: true, Year: 2026, IMDb: "tt22084616",
		Title: "Spider-Man: Brand New Day", AltTitle: "مرد عنکبوتی: روز کاملاً جدید",
	}
}

func TestMatchTitleRejectsAShorterFranchiseName(t *testing.T) {
	q := spider()
	hit := tguser.SearchHit{Name: "Spider-Man.2026.1080p.mkv", Caption: "Spider-Man (2026)"}
	if ok, why := MatchTitle(q, hit); ok {
		t.Fatal(why)
	}
}

func TestMatchTitleAcceptsTheFullTitleYearAndIMDb(t *testing.T) {
	q := spider()
	dotted := tguser.SearchHit{Name: "Spider-Man.Brand.New.Day.2026.1080p.mkv"}
	if ok, why := MatchTitle(q, dotted); !ok || why != "title" {
		t.Fatalf("dotted %v %s", ok, why)
	}
	off := tguser.SearchHit{Name: "Spider-Man.Brand.New.Day.2025.1080p.mkv"}
	if ok, _ := MatchTitle(q, off); !ok {
		t.Fatal("year within 1 should match")
	}
	far := tguser.SearchHit{Name: "Spider-Man.Brand.New.Day.2020.1080p.mkv"}
	if ok, _ := MatchTitle(q, far); ok {
		t.Fatal("year 2020 matched")
	}
	imdb := tguser.SearchHit{Caption: "grab https://www.imdb.com/title/tt22084616"}
	if ok, why := MatchTitle(q, imdb); !ok || why != "imdb" {
		t.Fatalf("imdb %v %s", ok, why)
	}
	sample := tguser.SearchHit{Name: "Spider-Man.Brand.New.Day.2026.sample.mkv", Caption: "tt22084616"}
	if ok, _ := MatchTitle(q, sample); ok {
		t.Fatal("sample matched")
	}
	trailer := tguser.SearchHit{Caption: "Spider-Man: Brand New Day (2026) trailer"}
	if ok, _ := MatchTitle(q, trailer); ok {
		t.Fatal("trailer matched")
	}
}

func TestSearchTextsPreferThePartAfterTheColon(t *testing.T) {
	got := searchTexts(spider())
	if len(got) < 3 || got[0] != "tt22084616" || got[1] != "Spider-Man: Brand New Day" {
		t.Fatal(got)
	}
	if !contains(got, "Brand New Day") {
		t.Fatal(got)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
