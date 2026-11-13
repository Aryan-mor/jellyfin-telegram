package subtitles

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSameQualityIgnoresUnknownAndTreats4kAs2160(t *testing.T) {
	if !SameQuality("1080p", "1080P") || !SameQuality("4k", "2160p") {
		t.Fatal("equal sizes did not match")
	}
	if SameQuality("1080p", "720p") || SameQuality("", "1080p") || SameQuality("1080p", "") {
		t.Fatal("different or unknown sizes matched")
	}
	if got := FileQuality("Film.720p.mkv", "jftg:2;q=1080p;k=m"); got != "1080p" {
		t.Fatalf("signed quality = %q", got)
	}
}

func TestFindRemoteKeepsTheMatchingQuality(t *testing.T) {
	src := &fakeRemote{hits: []RemoteHit{
		{Name: "a.srt", Caption: "Film\n#tm_m_603_en_fa\n720p", ChannelID: 1, MessageID: 2},
		{Name: "b.srt", Caption: "Film\n#tm_m_603_none_fa\njftg:2;q=1080p", ChannelID: 1, MessageID: 3},
	}}
	src.files = map[int]string{
		2: "1\n00:00:01,000 --> 00:00:02,000\nno\n",
		3: "1\n00:01:10,000 --> 00:01:12,000\nyes\n",
	}
	got, err := FindRemote(context.Background(), src, RemoteQuery{
		TMDB: 603, Lang: "fa", Quality: "1080p", Seconds: 80,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || !strings.Contains(got.Text, "yes") {
		t.Fatalf("got %#v", got)
	}
	if src.downloaded[2] {
		t.Fatal("the 720p file was downloaded")
	}
}

func TestGeminiPromptKeepsThePersianRules(t *testing.T) {
	got := geminiPrompt("1. Hello\n", "fa")
	if !strings.Contains(got, "می‌خوام") || !strings.Contains(got, "1. Hello") {
		t.Fatal("persian prompt drifted")
	}
	if !strings.Contains(geminiPrompt("1. Hello\n", "de"), "German") {
		t.Fatal("general prompt lost the language")
	}
	cues := []srtCue{{timing: "00:00:01,000 --> 00:00:02,000", text: "Hello"}}
	out := applyNumbered(cues, "1. سلام")
	if out[0].text != "سلام" || buildSRT(out) == "" {
		t.Fatalf("%#v", out)
	}
}

func TestSubDLDropsTheKeyFromTheDownloadPath(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "subtitles") {
			gotKey = r.URL.Query().Get("api_key")
			_, _ = w.Write([]byte(`{"status":true,"subtitles":[{"release_name":"Film.1080p","url":"https://dl.subdl.com/subtitle/1.zip?api_key=secret"}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := NewSubDL([]string{"secret"})
	c.http = srv.Client()
	c.api = srv.URL + "/subtitles"
	packs, err := c.SearchTMDB(context.Background(), 603, false, 0, 0, "fa")
	if err != nil {
		t.Fatal(err)
	}
	if gotKey != "secret" || len(packs) != 1 || packs[0].Tag != "/subtitle/1.zip" || strings.Contains(packs[0].Tag, "secret") {
		t.Fatalf("key %q packs %#v", gotKey, packs)
	}
}

type fakeRemote struct {
	hits       []RemoteHit
	files      map[int]string
	downloaded map[int]bool
}

func (f *fakeRemote) Search(context.Context, string) ([]RemoteHit, error) { return f.hits, nil }
func (f *fakeRemote) Next(context.Context, int64, int64, int) (RemoteHit, bool, error) {
	return RemoteHit{}, false, nil
}
func (f *fakeRemote) Download(_ context.Context, _, _ int64, messageID int) (string, []byte, error) {
	if f.downloaded == nil {
		f.downloaded = map[int]bool{}
	}
	f.downloaded[messageID] = true
	return "a.srt", []byte(f.files[messageID]), nil
}
