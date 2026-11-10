package setup

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckFolderCreatesNothing(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "a", "b")
	f, err := CheckFolder(target)
	if err != nil {
		t.Fatal(err)
	}
	if f.Exists || f.Path != target || f.Free == 0 {
		t.Fatalf("%+v", f)
	}
	if _, err := os.Stat(filepath.Join(base, "a")); !os.IsNotExist(err) {
		t.Error("checking made a folder")
	}
}

func TestCheckFolderRefusesWhatCannotWork(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "   ", "relative/path", file, filepath.Join(file, "inside")} {
		if _, err := CheckFolder(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestPrepareFolderMakesIt(t *testing.T) {
	target := filepath.Join(t.TempDir(), "x", "y")
	f, err := PrepareFolder(target)
	if err != nil || !f.Exists {
		t.Fatalf("%+v %v", f, err)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("not made: %v", err)
	}
}

func TestOneFolderFillsEveryPlace(t *testing.T) {
	root := t.TempDir()
	env := LayoutIn(root).Env()
	if env["DATA_DIR"] != filepath.Join(root, "data") || env["LIBRARY_DIR"] != filepath.Join(root, "library") {
		t.Fatalf("%v", env)
	}
}

func TestSavingAFolderWritesTheSettings(t *testing.T) {
	dir := t.TempDir()
	w, err := New(filepath.Join(dir, ".env"), nil)
	if err != nil {
		t.Fatal(err)
	}
	w.DefaultFolder = filepath.Join(dir, "default")
	chosen := filepath.Join(dir, "chosen")
	env, err := w.useFolder(chosen)
	if err != nil {
		t.Fatal(err)
	}
	if env["DATA_DIR"] != filepath.Join(chosen, "data") || env["LIBRARY_DIR"] != filepath.Join(chosen, "library") {
		t.Fatalf("%v", env)
	}
	for _, sub := range []string{"data", "library"} {
		if info, err := os.Stat(filepath.Join(chosen, sub)); err != nil || !info.IsDir() {
			t.Errorf("%s not made: %v", sub, err)
		}
	}
	// The place already in use changes nothing.
	same, err := w.useFolder(w.DefaultFolder)
	if err != nil || same != nil {
		t.Errorf("default folder: %v %v", same, err)
	}
}

func TestFolderEndpointsNeedTheCode(t *testing.T) {
	w, _ := New(filepath.Join(t.TempDir(), ".env"), nil)
	w.DefaultFolder = t.TempDir()
	h := w.Handler()
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(m, "/setup/folder", strings.NewReader(`{"path":"x"}`)))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s without code: %d", m, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/setup/folder?code="+w.Code(), strings.NewReader(`{"path":"relative"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("relative path: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/setup/folder?code="+w.Code(), nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"default"`) {
		t.Errorf("default: %d %s", rec.Code, rec.Body.String())
	}
}

func TestStatusCarriesTheDetailAndTheLog(t *testing.T) {
	w, _ := New(filepath.Join(t.TempDir(), ".env"), nil)
	w.SetStatus(Status{Stage: "Downloading Seerr", Percent: 60, Detail: "50.0 MB of 100.0 MB · 4.0 MB/s"})
	w.Logf("Downloading Seerr: 50%%")
	w.SetStatus(Status{Stage: "Starting Seerr", Percent: 88})
	w.SetStatus(Status{Stage: "Could not start", Error: "port in use"})

	rec := httptest.NewRecorder()
	w.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/setup/status?code="+w.Code(), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"stage":"Could not start"`, `"log":[`, "Downloading Seerr", "Downloading Seerr: 50%", "Starting Seerr", "Error: port in use"} {
		if !strings.Contains(body, want) {
			t.Errorf("status has no %q:\n%s", want, body)
		}
	}
}

func TestTheLogKeepsOnlyTheNewestLines(t *testing.T) {
	w, _ := New(filepath.Join(t.TempDir(), ".env"), nil)
	for i := 0; i < maxLog+50; i++ {
		w.Logf("line %d", i)
	}
	lines := w.LogLines()
	if len(lines) != maxLog || !strings.HasSuffix(lines[len(lines)-1], "line 349") || !strings.HasSuffix(lines[0], "line 50") {
		t.Errorf("%d lines, first %q, last %q", len(lines), lines[0], lines[len(lines)-1])
	}
}
