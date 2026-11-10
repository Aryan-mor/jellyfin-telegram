package setup

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowseListsOnlyFolders(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"beta", "Alpha", ".hidden", "$Recycle"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Browse(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(root, "Alpha"), filepath.Join(root, "beta")}
	if strings.Join(got.Dirs, "|") != strings.Join(want, "|") {
		t.Fatalf("dirs = %v, want %v", got.Dirs, want)
	}
	if got.Path != root || got.Parent != filepath.Dir(root) {
		t.Errorf("path %q parent %q", got.Path, got.Parent)
	}
}

func TestBrowseTopHasSomewhereToGo(t *testing.T) {
	got, err := Browse("")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Dirs) == 0 {
		t.Fatalf("nothing to pick: %+v", got)
	}
}

func TestBrowseRefusesWhatIsNotAFolder(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"relative", file, filepath.Join(t.TempDir(), "missing")} {
		if _, err := Browse(bad); err == nil {
			t.Errorf("listed %q", bad)
		}
	}
}

func TestBrowseAtTheTopOfADriveHasNoParent(t *testing.T) {
	root := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	got, err := Browse(root)
	if err != nil {
		t.Skip("cannot read the drive root here:", err)
	}
	if got.Parent != "" {
		t.Errorf("parent of %q = %q", root, got.Parent)
	}
}

func TestBrowseEndpointKeepsOutsiders(t *testing.T) {
	w, _ := New(filepath.Join(t.TempDir(), ".env"), nil)
	h := w.Handler()
	code := "?code=" + w.Code()

	local := httptest.NewRequest(http.MethodGet, "/setup/folder/browse"+code+"&path="+t.TempDir(), nil)
	local.RemoteAddr = "127.0.0.1:5555"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, local)
	if rec.Code != http.StatusOK {
		t.Fatalf("local: %d %s", rec.Code, rec.Body.String())
	}

	for name, mutate := range map[string]func(*http.Request){
		"another computer": func(r *http.Request) { r.RemoteAddr = "192.168.1.20:5555" },
		"a tunnel":         func(r *http.Request) { r.Header.Set("X-Forwarded-For", "8.8.8.8") },
	} {
		r := httptest.NewRequest(http.MethodGet, "/setup/folder/browse"+code, nil)
		r.RemoteAddr = "127.0.0.1:5555"
		mutate(r)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}

	noCode := httptest.NewRequest(http.MethodGet, "/setup/folder/browse", nil)
	noCode.RemoteAddr = "127.0.0.1:5555"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, noCode)
	if rec.Code != http.StatusForbidden {
		t.Errorf("without the code: %d", rec.Code)
	}
}
