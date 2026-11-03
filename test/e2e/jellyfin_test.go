//go:build e2e

// Package e2e runs the whole service against a real Jellyfin in Docker. Only Telegram is
// replaced, by a fake that serves local files and counts every read.
//
// Run with: go test -tags e2e -v -timeout 15m ./test/e2e
// Requires docker and ffmpeg. Set E2E_JELLYFIN_IMAGE to test another Jellyfin version.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/blockstore"
	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/gateway"
	"github.com/Aryan-mor/jellyfin-telegram/internal/indexer"
	"github.com/Aryan-mor/jellyfin-telegram/internal/jellyfin"
	"github.com/Aryan-mor/jellyfin-telegram/internal/library"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
	"github.com/Aryan-mor/jellyfin-telegram/internal/subarchive"
	"github.com/Aryan-mor/jellyfin-telegram/internal/subtitles"
)

const (
	channelID  = int64(4242)
	gwToken    = "e2e-token"
	jfPort     = "18197"
	jfBase     = "http://127.0.0.1:" + jfPort
	authHeader = `MediaBrowser Client="e2e", Device="e2e", DeviceId="e2e", Version="1"`
)

// fakeTelegram serves local files as if they were channel documents.
type fakeTelegram struct {
	files map[int]media.File
	paths map[int]string
	reads atomic.Int64
	thumb []byte

	mu          sync.Mutex
	attachments map[int]media.Attachment
	stored      map[int][]byte
	nextID      int
}

func (f *fakeTelegram) SendDocument(_ context.Context, name, _ string, data []byte, replyTo int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	id := f.nextID
	f.attachments[id] = media.Attachment{MessageID: id, DocID: int64(id) * 11, Size: int64(len(data)), FileName: name, ReplyTo: replyTo}
	f.stored[id] = data
	return id, nil
}

func (f *fakeTelegram) Download(_ context.Context, messageID int, _, _ int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stored[messageID], nil
}

func (f *fakeTelegram) DeleteMessages(context.Context, []int) error { return nil }

func (f *fakeTelegram) Messages(_ context.Context, ids []int) ([]media.Message, error) {
	out := make([]media.Message, 0, len(ids))
	for _, id := range ids {
		f.mu.Lock()
		att, hasAtt := f.attachments[id]
		f.mu.Unlock()
		if hasAtt {
			c := att
			out = append(out, media.Message{ID: id, Exists: true, Attachment: &c})
		} else if file, ok := f.files[id]; ok {
			c := file
			out = append(out, media.Message{ID: id, Exists: true, File: &c})
		} else {
			out = append(out, media.Message{ID: id})
		}
	}
	return out, nil
}

func (f *fakeTelegram) Thumbnail(context.Context, int) ([]byte, error) { return f.thumb, nil }

func (f *fakeTelegram) FetchBlock(_ context.Context, messageID int, _, block, size int64) ([]byte, error) {
	f.reads.Add(1)
	file, err := os.Open(f.paths[messageID])
	if err != nil {
		return nil, err
	}
	defer file.Close()
	buf := make([]byte, min(media.BlockSize, size-block*media.BlockSize))
	if _, err := file.ReadAt(buf, block*media.BlockSize); err != nil {
		return nil, err
	}
	return buf, nil
}

func TestFullFlowAgainstRealJellyfin(t *testing.T) {
	for _, tool := range []string{"docker", "ffmpeg"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	dir := t.TempDir()
	mkv := filepath.Join(dir, "a.mkv")
	mp4 := filepath.Join(dir, "b.mp4")
	srtPath := filepath.Join(dir, "embedded.srt")
	if err := os.WriteFile(srtPath, []byte("1\n00:00:02,000 --> 00:00:05,000\nEmbedded subtitle line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, "ffmpeg", "-y", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24:duration=60",
		"-f", "lavfi", "-i", "anoisesrc=d=60:c=pink:r=48000", "-f", "lavfi", "-i", "sine=f=440:d=60", "-i", srtPath,
		"-map", "0", "-map", "1", "-map", "2", "-map", "3", "-c:s", "srt", "-metadata:s:s:0", "language=eng",
		"-c:v", "libx264", "-preset", "ultrafast",
		"-b:v", "6M", "-minrate", "6M", "-maxrate", "6M", "-bufsize", "2M", "-x264-params", "nal-hrd=cbr",
		"-c:a", "aac", "-metadata:s:a:0", "language=eng", "-metadata:s:a:1", "language=fas", mkv)
	run(t, "ffmpeg", "-y", "-v", "error", "-i", mkv, "-map", "0:v", "-map", "0:a", "-c", "copy", mp4)
	thumbPath := filepath.Join(dir, "t.jpg")
	run(t, "ffmpeg", "-y", "-v", "error", "-f", "lavfi", "-i", "color=c=red:s=90x90", "-frames:v", "1", thumbPath)
	thumb, err := os.ReadFile(thumbPath)
	if err != nil {
		t.Fatal(err)
	}

	tg := &fakeTelegram{
		files: map[int]media.File{}, paths: map[int]string{}, thumb: thumb,
		attachments: map[int]media.Attachment{}, stored: map[int][]byte{}, nextID: 5,
	}
	add := func(id int, path, name, caption string, hasThumb bool) {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		tg.files[id] = media.File{
			MessageID: id, ChannelID: channelID, DocID: int64(id) * 1000, Size: st.Size(),
			MIME: "video/x-matroska", FileName: name, Caption: caption, HasThumb: hasThumb,
			Date: time.Unix(1700000000, 0).UTC(),
		}
		tg.paths[id] = path
	}
	add(1, mkv, "Zzz Quiet Harbor.2021.1080p.mkv", "", false)
	add(2, mp4, "Zzz Quiet Harbor.2021.720p.mp4", "", false)
	add(3, mp4, "Zzz Lantern Road.S01E02.mp4", "", false)
	add(5, mkv, "Family Trip.mkv", "Our summer holiday", true)

	cat, err := catalog.Open(filepath.Join(dir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	store, err := blockstore.Open(filepath.Join(dir, "cache"), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	engine := gateway.NewEngine(store, tg, log)

	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: gateway.Handler(engine, cat, gateway.Options{Token: gwToken, ChannelID: channelID, Readahead: 8, BackgroundBytesPerSecond: 500 << 20}, log)}
	go func() { _ = srv.Serve(listener) }()
	defer srv.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	libRoot := filepath.Join(dir, "library")
	lib := library.New(libRoot, fmt.Sprintf("http://host.docker.internal:%d", port), gwToken, channelID)
	if err := lib.Prepare(); err != nil {
		t.Fatal(err)
	}
	idx := indexer.New(tg, cat, lib, store, log)
	added, err := idx.Sync(ctx, indexer.FullWindow)
	if err != nil || added != 4 {
		t.Fatalf("sync added %d, %v", added, err)
	}
	if got := tg.reads.Load(); got != 0 {
		t.Errorf("indexing read %d video blocks", got)
	}

	subsRoot := filepath.Join(dir, "jellyfin-subtitles")
	if err := os.MkdirAll(subsRoot, 0o777); err != nil {
		t.Fatal(err)
	}
	apiKey := startJellyfin(t, ctx, libRoot, subsRoot)
	jf := &jfClient{key: apiKey}

	jf.post(t, ctx, "/Library/VirtualFolders?name=Movies&collectionType=movies&refreshLibrary=false",
		libraryOptions("/media/Movies"))
	jf.post(t, ctx, "/Library/VirtualFolders?name=Shows&collectionType=tvshows&refreshLibrary=false",
		libraryOptions("/media/Shows"))

	t.Log("probing through the service, with no player attached")
	client := jellyfin.New(jfBase, apiKey)
	userID, err := client.AdminUserID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prober := jellyfin.NewProber(client, cat, engine, log)
	prober.Poll = 2 * time.Second
	if err := prober.Run(ctx, true); err != nil {
		t.Fatal(err)
	}
	pending, err := cat.Unprobed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		for _, f := range pending {
			t.Errorf("not probed: message %d at %s", f.MessageID, f.RelPath)
		}
		dumpItems(t, ctx, client, userID)
		t.FailNow()
	}

	var totalSize int64
	for _, f := range tg.files {
		totalSize += f.Size
	}
	probeReads := tg.reads.Load()
	t.Logf("probing 4 files read %d MiB out of %d MiB", probeReads, totalSize>>20)
	if probeReads*media.BlockSize >= totalSize/2 {
		t.Errorf("probing read too much: %d blocks", probeReads)
	}

	items, err := client.Items(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]jfItem{}
	for _, it := range jf.items(t, ctx, userID) {
		byName[it.Name] = it
	}
	movie, ok := byName["Family Trip"]
	if !ok {
		t.Fatalf("local title from the NFO not found, items: %v", names(byName))
	}
	if movie.Overview != "Our summer holiday" {
		t.Errorf("overview = %q", movie.Overview)
	}
	if _, ok := movie.ImageTags["Primary"]; !ok {
		t.Error("telegram thumbnail was not used as the poster")
	}
	if movie.RunTimeTicks == 0 || len(movie.MediaStreams) != 4 {
		t.Errorf("media details missing: ticks %d, streams %d", movie.RunTimeTicks, len(movie.MediaStreams))
	}

	versions := 0
	for _, it := range items {
		if len(it.Sources) == 2 {
			versions++
		}
	}
	if versions != 1 {
		t.Errorf("1080p and 720p were not grouped as one title with two versions: %d", versions)
	}

	t.Log("repeat playback info: pinned blocks must answer the probe")
	before := tg.reads.Load()
	for _, it := range items {
		for _, s := range it.Sources {
			res, err := client.Probe(ctx, s.ID, userID)
			if err != nil {
				t.Fatal(err)
			}
			if res.Streams == 0 {
				t.Errorf("source %s lost its streams", s.ID)
			}
		}
	}
	if got := tg.reads.Load() - before; got != 0 {
		t.Errorf("repeat probes made %d Telegram reads", got)
	}

	t.Log("seek through Jellyfin must return the exact bytes")
	want, err := os.ReadFile(mkv)
	if err != nil {
		t.Fatal(err)
	}
	from := int64(len(want)) / 2
	got := jf.rangeRead(t, ctx, fmt.Sprintf("/Videos/%s/stream?static=true&mediaSourceId=%s", movie.ID, movie.ID), from, from+2*media.BlockSize-1)
	if !bytes.Equal(got, want[from:from+2*media.BlockSize]) {
		t.Error("bytes through Jellyfin differ from the source file")
	}

	t.Log("rescan must keep the probed details")
	before = tg.reads.Load()
	if err := client.RefreshLibrary(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Second)
	if got := tg.reads.Load() - before; got != 0 {
		t.Errorf("rescan made %d Telegram reads", got)
	}
	again := jf.items(t, ctx, userID)
	for _, it := range again {
		if it.Name == "Family Trip" && len(it.MediaStreams) != 4 {
			t.Errorf("streams lost after rescan: %d", len(it.MediaStreams))
		}
	}

	t.Log("embedded subtitles: extract once, keep in the channel, fill the cache")
	tools, err := subtitles.FindTools("")
	if err != nil {
		t.Fatal(err)
	}
	worker := &subarchive.Worker{
		Catalog: cat, Client: client, Prober: prober, Channel: tg, Extractor: tools, SubtitlesRoot: subsRoot, Log: log,
		StreamURL: func(id int) string {
			return fmt.Sprintf("http://127.0.0.1:%d/s/%s/%d/%d", port, gwToken, channelID, id)
		},
	}
	runWorker := func() {
		t.Helper()
		for range 20 {
			did, err := worker.RunOnce(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !did {
				return
			}
		}
		t.Fatal("the worker never finished")
	}
	runWorker()
	if _, err := idx.Sync(ctx, indexer.IncrementalWindow); err != nil {
		t.Fatal(err)
	}
	stored := func() map[int]string {
		out := map[int]string{}
		all, err := cat.All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range all {
			out[f.MessageID] = f.ExtractState
		}
		return out
	}
	states := stored()
	if states[1] != media.ExtractDone || states[5] != media.ExtractDone {
		t.Errorf("mkv files not archived: %v", states)
	}
	if states[3] != media.ExtractNone {
		t.Errorf("a file without subtitles got state %q", states[3])
	}
	if len(tg.stored) == 0 {
		t.Error("nothing was stored in the channel")
	}

	subtitleText := func() string {
		t.Helper()
		var info struct {
			MediaSources []struct {
				ID           string `json:"Id"`
				MediaStreams []struct {
					Type  string
					Index int
				}
			}
		}
		jf.get(t, ctx, fmt.Sprintf("/Items/%s/PlaybackInfo?userId=%s", movie.ID, userID), &info)
		src := info.MediaSources[0]
		for _, m := range src.MediaStreams {
			if m.Type == "Subtitle" {
				res := jf.request(t, ctx, http.MethodGet, fmt.Sprintf("/Videos/%s/%s/Subtitles/%d/0/Stream.vtt", movie.ID, src.ID, m.Index), "")
				defer res.Body.Close()
				b, err := io.ReadAll(res.Body)
				if err != nil {
					t.Fatal(err)
				}
				return string(b)
			}
		}
		t.Fatal("no subtitle track")
		return ""
	}
	before = tg.reads.Load()
	if text := subtitleText(); !strings.Contains(text, "Embedded subtitle line") {
		t.Errorf("subtitle text = %q", text)
	}
	if got := tg.reads.Load() - before; got != 0 {
		t.Errorf("requesting a kept subtitle made %d video reads", got)
	}

	t.Log("losing every cache must not read any video again")
	if err := os.RemoveAll(subsRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(subsRoot, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := worker.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	if got := stored()[5]; got != "" {
		t.Errorf("a lost cache was not noticed: state %q", got)
	}
	before = tg.reads.Load()
	runWorker()
	if got := tg.reads.Load() - before; got > 3 {
		t.Errorf("restoring from the channel made %d video reads", got)
	}
	if text := subtitleText(); !strings.Contains(text, "Embedded subtitle line") {
		t.Errorf("restored subtitle text = %q", text)
	}
	if got := tg.reads.Load() - before; got > 3 {
		t.Errorf("a restored subtitle made %d video reads", got)
	}

	t.Log("a new gateway address must not make Jellyfin delete extracted subtitles")
	second, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(second) }()
	newLib := library.New(libRoot, fmt.Sprintf("http://host.docker.internal:%d", second.Addr().(*net.TCPAddr).Port), gwToken, channelID)
	if err := indexer.New(tg, cat, newLib, store, log).Rewrite(ctx); err != nil {
		t.Fatal(err)
	}
	before = tg.reads.Load()
	for range 3 {
		if text := subtitleText(); !strings.Contains(text, "Embedded subtitle line") {
			t.Errorf("subtitle text after the address change = %q", text)
		}
	}
	if got := tg.reads.Load() - before; got > 3 {
		t.Errorf("the address change caused %d video reads, so the subtitles were extracted again", got)
	}
	if err := worker.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	if got := stored()[5]; got != media.ExtractDone {
		t.Errorf("the address change lost the subtitle cache: state %q", got)
	}

	t.Log("deleting a channel message removes it from the library")
	delete(tg.files, 5)
	removed, err := idx.Reconcile(ctx)
	if err != nil || removed != 1 {
		t.Fatalf("removed %d, %v", removed, err)
	}
	if _, err := os.Stat(filepath.Join(libRoot, "Movies", "Family Trip")); !os.IsNotExist(err) {
		t.Error("deleted title still in the library")
	}
}

func libraryOptions(path string) string {
	return fmt.Sprintf(`{"LibraryOptions":{"EnableRealtimeMonitor":false,"PathInfos":[{"Path":%q}]}}`, path)
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", name, err, out)
	}
}

// startJellyfin runs a throwaway container, completes setup and returns an API key.
func startJellyfin(t *testing.T, ctx context.Context, libRoot, subsRoot string) string {
	t.Helper()
	image := os.Getenv("E2E_JELLYFIN_IMAGE")
	if image == "" {
		image = "jellyfin/jellyfin:latest"
	}
	name := fmt.Sprintf("jftg-e2e-%d", time.Now().Unix())
	run(t, "docker", "run", "-d", "--rm", "--name", name, "-p", jfPort+":8096",
		"--add-host=host.docker.internal:host-gateway", "-v", libRoot+":/media:ro",
		"-v", subsRoot+":/config/data/subtitles", image)
	t.Cleanup(func() {
		if t.Failed() {
			if out, err := exec.Command("docker", "logs", "--tail", "40", name).CombinedOutput(); err == nil {
				t.Logf("jellyfin logs:\n%s", out)
			}
		}
		_ = exec.Command("docker", "rm", "-f", "-v", name).Run()
	})

	setup := &jfClient{header: authHeader}
	deadline := time.Now().Add(3 * time.Minute)
	for {
		if res, err := http.Get(jfBase + "/System/Info/Public"); err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("jellyfin did not start")
		}
		time.Sleep(time.Second)
	}
	// The wizard endpoints answer 503 for a short while after the server is up.
	setup.retry(t, ctx, "/Startup/Configuration", `{"UICulture":"en-US","MetadataCountryCode":"US","PreferredMetadataLanguage":"en"}`)
	setup.get(t, ctx, "/Startup/User", nil)
	setup.post(t, ctx, "/Startup/User", `{"Name":"e2e","Password":"e2e-password"}`)
	setup.post(t, ctx, "/Startup/Complete", "")

	var auth struct {
		AccessToken string
	}
	setup.postJSON(t, ctx, "/Users/AuthenticateByName", `{"Username":"e2e","Pw":"e2e-password"}`, &auth)
	admin := &jfClient{key: auth.AccessToken}
	admin.post(t, ctx, "/Auth/Keys?app=e2e", "")
	var keys struct {
		Items []struct{ AccessToken string }
	}
	admin.get(t, ctx, "/Auth/Keys", &keys)
	if len(keys.Items) == 0 {
		t.Fatal("no API key created")
	}
	return keys.Items[0].AccessToken
}

type jfItem struct {
	ID           string `json:"Id"`
	Name         string
	Overview     string
	RunTimeTicks int64
	ImageTags    map[string]string
	MediaStreams []json.RawMessage
}

type jfClient struct {
	key    string
	header string
}

func (c *jfClient) request(t *testing.T, ctx context.Context, method, path, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, jfBase+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	h := c.header
	if h == "" {
		h = fmt.Sprintf(`%s, Token=%q`, authHeader, c.key)
	}
	req.Header.Set("Authorization", h)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (c *jfClient) post(t *testing.T, ctx context.Context, path, body string) {
	t.Helper()
	res := c.request(t, ctx, http.MethodPost, path, body)
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("POST %s: %d %s", path, res.StatusCode, b)
	}
}

func (c *jfClient) retry(t *testing.T, ctx context.Context, path, body string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		res := c.request(t, ctx, http.MethodPost, path, body)
		res.Body.Close()
		if res.StatusCode < 300 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("POST %s: %d", path, res.StatusCode)
		}
		time.Sleep(time.Second)
	}
}

func (c *jfClient) postJSON(t *testing.T, ctx context.Context, path, body string, out any) {
	t.Helper()
	res := c.request(t, ctx, http.MethodPost, path, body)
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		t.Fatalf("POST %s: %d", path, res.StatusCode)
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

func (c *jfClient) get(t *testing.T, ctx context.Context, path string, out any) {
	t.Helper()
	res := c.request(t, ctx, http.MethodGet, path, "")
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		t.Fatalf("GET %s: %d", path, res.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
}

func (c *jfClient) items(t *testing.T, ctx context.Context, userID string) []jfItem {
	t.Helper()
	q := url.Values{
		"userId": {userID}, "recursive": {"true"}, "includeItemTypes": {"Movie,Episode"},
		"fields": {"Overview,MediaStreams"},
	}
	var out struct{ Items []jfItem }
	c.get(t, ctx, "/Items?"+q.Encode(), &out)
	return out.Items
}

func (c *jfClient) rangeRead(t *testing.T, ctx context.Context, path string, from, to int64) []byte {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jfBase+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", fmt.Sprintf(`%s, Token=%q`, authHeader, c.key))
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, to))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusPartialContent {
		t.Fatalf("range read: status %d", res.StatusCode)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func dumpItems(t *testing.T, ctx context.Context, c *jellyfin.Client, userID string) {
	t.Helper()
	items, err := c.Items(ctx, userID)
	if err != nil {
		t.Log(err)
		return
	}
	for _, it := range items {
		t.Logf("item %s path=%s sources=%+v", it.ID, it.Path, it.Sources)
	}
}

func names(m map[string]jfItem) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
