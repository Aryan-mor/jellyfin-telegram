package naming

import (
	"strings"
	"testing"

	"github.com/Aryan-mor/jellyfin-telegram/internal/sign"
)

func roles(tracks []sign.AudioTrack) string {
	var out []string
	for _, t := range tracks {
		out = append(out, t.Lang+"."+t.Role)
	}
	return strings.Join(out, ",")
}

func TestAudioTracksUseTheOriginalLanguage(t *testing.T) {
	in := []AudioIn{{Lang: "fa", Index: 1}, {Lang: "en", Index: 2}}
	if got := roles(AudioTracks(in, "en", "Inception.2010.mkv")); got != "fa.dub,en.orig" {
		t.Fatalf("got %s", got)
	}
	// The original language arrives in whatever form the source used.
	if got := roles(AudioTracks(in, "eng", "")); got != "fa.dub,en.orig" {
		t.Fatalf("iso3: %s", got)
	}
}

func TestAudioTracksTagsBeatLanguage(t *testing.T) {
	in := []AudioIn{{Lang: "en", Title: "Persian Dubbed", Index: 1}, {Lang: "ja", Title: "Original", Index: 2}}
	if got := roles(AudioTracks(in, "", "")); got != "en.dub,ja.orig" {
		t.Fatalf("got %s", got)
	}
	in = []AudioIn{{Lang: "fa", Title: "دوبله فارسی", Index: 1}}
	if got := roles(AudioTracks(in, "en", "")); got != "fa.dub" {
		t.Fatalf("persian tag: %s", got)
	}
}

func TestAudioTracksWithoutEvidenceHaveNoRole(t *testing.T) {
	in := []AudioIn{{Lang: "fa", Index: 1}, {Lang: "en", Index: 2}}
	if got := roles(AudioTracks(in, "", "Movie.1080p.mkv")); got != "fa.,en." {
		t.Fatalf("guessed a role: %s", got)
	}
	// A file called dual audio says nothing about which of two tracks is the dub.
	if got := roles(AudioTracks(in, "", "Movie.Dual.Audio.mkv")); got != "fa.,en." {
		t.Fatalf("guessed from dual audio: %s", got)
	}
	// One track in a file called dubbed is the dub.
	if got := roles(AudioTracks(in[:1], "", "Movie.Dubbed.mkv")); got != "fa.dub" {
		t.Fatalf("single dubbed: %s", got)
	}
	if got := roles(AudioTracks([]AudioIn{{Index: 1}}, "en", "")); got != "und." {
		t.Fatalf("unknown language: %s", got)
	}
}

func TestCaptionShowsAudio(t *testing.T) {
	tracks := []sign.AudioTrack{{Lang: "fa", Role: sign.RoleDub}, {Lang: "en", Role: sign.RoleOriginal}, {Lang: "ja"}}
	got := Caption(Head{Title: "Inception", Year: 2010, TMDB: 27205, Label: "1080p", Audio: ShowAudio(tracks),
		Soft: []string{"fa (srt)", "en (srt)"}, Hard: "none"})
	want := "Inception (2010)\n1080p\n🔊 fa dub · en · ja\n💬 fa (srt), en (srt)"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(Caption(Head{Title: "x"}), "Audio") {
		t.Fatal("an audio line was written for a file that was not inspected")
	}
	many := make([]sign.AudioTrack, 8)
	for i := range many {
		many[i] = sign.AudioTrack{Lang: "en"}
	}
	if shown := ShowAudio(many); len(shown) != 7 || shown[6] != "+2" {
		t.Fatalf("shown: %v", shown)
	}
}
