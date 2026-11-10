package managed

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func serverExeName() string {
	if runtime.GOOS == "windows" {
		return "jellyfin.exe"
	}
	return "jellyfin"
}

func zipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func tarGz(t *testing.T, headers []tar.Header, contents map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	w := tar.NewWriter(gz)
	for _, h := range headers {
		h := h
		h.Size = int64(len(contents[h.Name]))
		if err := w.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(contents[h.Name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestEveryReleaseHasAPinnedHash(t *testing.T) {
	for platform, r := range releases {
		for _, a := range []*Artifact{&r.Server, r.FFmpeg} {
			if a == nil {
				continue
			}
			if len(a.SHA256) != 64 || !strings.HasPrefix(a.URL, "https://") {
				t.Errorf("%s: bad artifact %+v", platform, a)
			}
			if strings.Contains(a.URL, "latest-stable") {
				t.Errorf("%s: %s can change under a pinned hash", platform, a.URL)
			}
		}
	}
	if _, err := ReleaseFor("plan9", "mips"); err == nil {
		t.Error("an unknown platform was accepted")
	}
}

func TestInstallVerifiesAndIsIdempotent(t *testing.T) {
	archive := zipBytes(t, map[string]string{"jellyfin/" + serverExeName(): "binary", "jellyfin/jellyfin-web/index.html": "web"})
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	root := t.TempDir()
	rel := Release{Version: "1.0", Server: Artifact{URL: srv.URL + "/a.zip", SHA256: sum(archive)}}
	var seen int64
	var unpacked, unpackTotal int64
	paths, err := Install(context.Background(), root, rel, srv.Client(), func(name string, done, total int64) {
		if strings.HasPrefix(name, UnpackPrefix) {
			unpacked, unpackTotal = done, total
			return
		}
		seen = done
	})
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(paths.Server); err != nil || string(b) != "binary" {
		t.Fatalf("server %q, %v", b, err)
	}
	if seen != int64(len(archive)) {
		t.Errorf("progress reported %d of %d bytes", seen, len(archive))
	}
	if unpackTotal == 0 || unpacked != unpackTotal {
		t.Errorf("unpacking ended at %d of %d", unpacked, unpackTotal)
	}
	if _, err := Install(context.Background(), root, rel, srv.Client(), nil); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("downloaded %d times", calls)
	}
}

func TestInstallRefusesAChangedFile(t *testing.T) {
	archive := zipBytes(t, map[string]string{"jellyfin/" + serverExeName(): "tampered"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) }))
	defer srv.Close()
	root := t.TempDir()
	rel := Release{Version: "1.0", Server: Artifact{URL: srv.URL + "/a.zip", SHA256: strings.Repeat("0", 64)}}
	_, err := Install(context.Background(), root, rel, srv.Client(), nil)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "1.0", "jellyfin")); err == nil {
		t.Error("a file that failed its checksum was extracted")
	}
}

func TestInterruptedInstallStartsOver(t *testing.T) {
	archive := zipBytes(t, map[string]string{"jellyfin/" + serverExeName(): "binary"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) }))
	defer srv.Close()
	root := t.TempDir()
	leftover := filepath.Join(root, "1.0", "jellyfin", "partial.dll")
	if err := os.MkdirAll(filepath.Dir(leftover), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leftover, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rel := Release{Version: "1.0", Server: Artifact{URL: srv.URL + "/a.zip", SHA256: sum(archive)}}
	if _, err := Install(context.Background(), root, rel, srv.Client(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(leftover); err == nil {
		t.Error("a partial install was kept")
	}
}

func TestArchivesCannotEscapeTheirFolder(t *testing.T) {
	dir := t.TempDir()
	evil := zipBytes(t, map[string]string{"../evil.txt": "x"})
	if err := extractZip(bytes.NewReader(evil), int64(len(evil)), dir, func(int64, int64) {}); err == nil {
		t.Error("zip entry with .. was accepted")
	}
	name := "../evil.txt"
	data := tarGz(t, []tar.Header{{Name: name, Typeflag: tar.TypeReg, Mode: 0o644}}, map[string]string{name: "x"})
	if err := runTarGz(data, dir); err == nil {
		t.Error("tar entry with .. was accepted")
	}
	link := tarGz(t, []tar.Header{{Name: "a/link", Typeflag: tar.TypeSymlink, Linkname: "../../outside", Mode: 0o777}}, nil)
	if err := runTarGz(link, dir); err == nil {
		t.Error("link leaving the folder was accepted")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil.txt")); err == nil {
		t.Error("a file landed outside the folder")
	}
}

func runTarGz(data []byte, dir string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gz.Close()
	return extractTar(gz, dir)
}

func TestTarKeepsExecutableBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes do not apply")
	}
	dir := t.TempDir()
	name := "jellyfin/jellyfin"
	data := tarGz(t, []tar.Header{{Name: name, Typeflag: tar.TypeReg, Mode: 0o755}}, map[string]string{name: "x"})
	if err := runTarGz(data, dir); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "jellyfin", "jellyfin"))
	if err != nil || st.Mode()&0o100 == 0 {
		t.Errorf("mode %v, %v", st.Mode(), err)
	}
}

// fakeJellyfin records the wizard calls in order.
type fakeJellyfin struct {
	done   bool
	calls  []string
	bodies map[string]map[string]any
}

func (f *fakeJellyfin) handler(t *testing.T) http.Handler {
	t.Helper()
	f.bodies = map[string]map[string]any{}
	note := func(r *http.Request) {
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) == nil {
			f.bodies[r.URL.Path] = body
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/System/Info/Public", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"StartupWizardCompleted": f.done})
	})
	mux.HandleFunc("/Startup/", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		if r.URL.Path == "/Startup/Complete" {
			f.done = true
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/Users/AuthenticateByName", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		_ = json.NewEncoder(w).Encode(map[string]any{"AccessToken": "session"})
	})
	mux.HandleFunc("/Auth/Keys", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			note(r)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Items": []map[string]string{{"AccessToken": "the-key", "AppName": "jellyfin-telegram"}}})
	})
	mux.HandleFunc("/Library/VirtualFolders", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			note(r)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/Users/Me", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": "u1", "Configuration": map[string]any{"PlayDefaultAudioTrack": true}})
	})
	mux.HandleFunc("/Users/u1/Configuration", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func TestBootstrapRunsTheWizardAndRemembers(t *testing.T) {
	jf := &fakeJellyfin{}
	srv := httptest.NewServer(jf.handler(t))
	defer srv.Close()
	state := filepath.Join(t.TempDir(), "sub", "credentials.json")
	b := &Bootstrap{URL: srv.URL, StateFile: state, MoviesDir: "/m", ShowsDir: "/s", Language: "de"}
	creds, err := b.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if creds.User != adminUser || len(creds.Password) < 16 || creds.APIKey != "the-key" {
		t.Fatalf("creds = %+v", creds)
	}
	joined := strings.Join(jf.calls, "|")
	for _, want := range []string{"POST /Startup/User", "POST /Startup/Complete", "POST /Auth/Keys", "POST /Library/VirtualFolders", "POST /Users/u1/Configuration"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s in %s", want, joined)
		}
	}
	if got := jf.bodies["/Startup/RemoteAccess"]["EnableAutomaticPortMapping"]; got != false {
		t.Errorf("automatic port mapping = %v, must stay off", got)
	}
	if got := jf.bodies["/Users/u1/Configuration"]["SubtitleLanguagePreference"]; got != "deu" {
		t.Errorf("subtitle language = %v", got)
	}
	if got := jf.bodies["/Users/u1/Configuration"]["PlayDefaultAudioTrack"]; got != true {
		t.Error("an existing user setting was lost")
	}
	if got := jf.bodies["/Library/VirtualFolders"]["LibraryOptions"].(map[string]any)["SaveSubtitlesWithMedia"]; got != false {
		t.Errorf("SaveSubtitlesWithMedia = %v", got)
	}

	// A later start reuses everything and repeats nothing from the wizard.
	jf.calls = nil
	again, err := b.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if again != creds {
		t.Errorf("credentials changed: %+v", again)
	}
	if strings.Contains(strings.Join(jf.calls, "|"), "/Startup/") {
		t.Errorf("the wizard ran twice: %v", jf.calls)
	}
}

func TestCredentialsFileIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes do not apply")
	}
	state := filepath.Join(t.TempDir(), "credentials.json")
	if err := (&Bootstrap{StateFile: state}).save(Credentials{Password: "x"}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(state)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, %v", st.Mode(), err)
	}
}

func TestStopStaleIgnoresAnUnrelatedProcess(t *testing.T) {
	// This test's own process does not run the given file, so it must survive.
	pidFile := filepath.Join(t.TempDir(), "p")
	if err := os.WriteFile(pidFile, []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if runsFile(os.Getpid(), filepath.Join(t.TempDir(), "not-this.exe")) {
		t.Error("an unrelated process was taken for the server")
	}
}
