package main

import "testing"

func TestMirrorIndexTags(t *testing.T) {
	got := mirrorIndexTags([]string{"video", "movie", "series", "music", "movie"})
	want := "direct-file,movie,music,series"
	if joinTags(got) != want {
		t.Fatalf("%v", got)
	}
	if len(mirrorIndexTags(nil)) != 0 {
		t.Fatal("empty")
	}
}

func TestIntroducedRoundTrip(t *testing.T) {
	raw := formatIntroduced(map[string]string{"b": "news", "a": "direct-file,movie"})
	back := parseIntroduced(raw)
	if back["a"] != "direct-file,movie" || back["b"] != "news" {
		t.Fatalf("%q %+v", raw, back)
	}
}

func joinTags(tags []string) string {
	out := ""
	for i, t := range tags {
		if i > 0 {
			out += ","
		}
		out += t
	}
	return out
}
