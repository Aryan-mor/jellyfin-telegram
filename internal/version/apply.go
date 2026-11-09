package version

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const maxUpdateBytes = 80 << 20

// Install downloads the newest release for this channel and this computer, and
// replaces the running program. The caller starts that new program.
func Install(ctx context.Context) error {
	if Version == "dev" {
		return errors.New("this build cannot install an update")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	rel, err := Fetch(ctx)
	if err != nil {
		return err
	}
	latest := strings.TrimPrefix(rel.Tag, "v")
	if !Newer(latest, Version) {
		return errors.New("this is already the newest " + Channel)
	}
	asset, ok := PickAsset(rel.Assets, runtime.GOOS, runtime.GOARCH)
	if !ok {
		return fmt.Errorf("this release has no build for %s %s", runtime.GOOS, runtime.GOARCH)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return installFromURL(ctx, exe, asset)
}

// ArchiveName is the release file for one OS and architecture.
func ArchiveName(goos, goarch string) (string, bool) {
	switch {
	case goos == "windows" && goarch == "amd64":
		return "jellyfin-telegram-windows-amd64.zip", true
	case goos == "linux" && (goarch == "amd64" || goarch == "arm64"):
		return "jellyfin-telegram-linux-" + goarch + ".tar.gz", true
	default:
		return "", false
	}
}

// PickAsset finds the archive this computer can install.
func PickAsset(assets []Asset, goos, goarch string) (Asset, bool) {
	name, ok := ArchiveName(goos, goarch)
	if !ok {
		return Asset{}, false
	}
	for _, asset := range assets {
		if asset.Name == name && asset.URL != "" {
			return asset, true
		}
	}
	return Asset{}, false
}

func allowedDownload(raw string) error {
	if !strings.HasPrefix(raw, "https://github.com/"+Repo+"/releases/download/") {
		return errors.New("refusing that download address")
	}
	return nil
}

func installFromURL(ctx context.Context, exe string, asset Asset) error {
	if err := allowedDownload(asset.URL); err != nil {
		return err
	}
	body, err := download(ctx, asset.URL)
	if err != nil {
		return err
	}
	defer body.Close()
	tmp, err := os.CreateTemp(filepath.Dir(exe), ".update-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	written, err := io.Copy(tmp, io.LimitReader(body, maxUpdateBytes+1))
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if written > maxUpdateBytes {
		return errors.New("the download is too large")
	}
	return installArchive(exe, tmpName, asset.Name)
}

var downloadClient = &http.Client{
	Timeout: 3 * time.Minute,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		host := req.URL.Hostname()
		if host == "github.com" || strings.HasSuffix(host, ".githubusercontent.com") {
			return nil
		}
		return fmt.Errorf("refusing a redirect to %s", host)
	},
}

func download(ctx context.Context, raw string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "jellyfin-telegram")
	res, err := downloadClient.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("download answered %d", res.StatusCode)
	}
	return res.Body, nil
}

func installArchive(exe, archivePath, archiveName string) error {
	want := "jellyfin-telegram"
	if strings.Contains(archiveName, "windows") {
		want = "jellyfin-telegram.exe"
	}
	staged := exe + ".new"
	var err error
	switch {
	case strings.HasSuffix(archiveName, ".zip"):
		err = extractZip(archivePath, staged, want)
	case strings.HasSuffix(archiveName, ".tar.gz"):
		err = extractTarGz(archivePath, staged, want)
	default:
		err = fmt.Errorf("unknown release file %s", archiveName)
	}
	if err != nil {
		_ = os.Remove(staged)
		return err
	}
	if err := swapBinary(exe, staged); err != nil {
		_ = os.Remove(staged)
		return err
	}
	return nil
}

func safeMember(name, want string) bool {
	name = strings.ReplaceAll(name, "\\", "/")
	return name == want
}

func extractZip(archivePath, dest, want string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, file := range r.File {
		if !safeMember(file.Name, want) || file.FileInfo().IsDir() {
			continue
		}
		if file.UncompressedSize64 > maxUpdateBytes {
			return errors.New("the downloaded program is too large")
		}
		rc, err := file.Open()
		if err != nil {
			return err
		}
		err = writeFileLimited(dest, rc)
		rc.Close()
		return err
	}
	return errors.New("the download has no program file")
}

func extractTarGz(archivePath, dest, want string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("the download has no program file")
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || !safeMember(hdr.Name, want) {
			continue
		}
		if hdr.Size < 0 || hdr.Size > maxUpdateBytes {
			return errors.New("the downloaded program is too large")
		}
		return writeFileLimited(dest, tr)
	}
}

func writeFileLimited(dest string, r io.Reader) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.LimitReader(r, maxUpdateBytes+1))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(dest)
		return err
	}
	info, err := os.Stat(dest)
	if err != nil {
		return err
	}
	if info.Size() > maxUpdateBytes {
		_ = os.Remove(dest)
		return errors.New("the downloaded program is too large")
	}
	return nil
}

func swapBinary(exe, staged string) error {
	previous := exe + ".previous"
	if err := os.Remove(previous); err != nil && !os.IsNotExist(err) {
		previous = exe + ".previous-" + strconv.FormatInt(time.Now().Unix(), 10)
	}
	if err := os.Rename(exe, previous); err != nil {
		return fmt.Errorf("could not move the current program aside: %w", err)
	}
	if err := os.Rename(staged, exe); err != nil {
		if back := os.Rename(previous, exe); back != nil {
			return fmt.Errorf("install failed (%v) and the old program could not be put back (%v)", err, back)
		}
		return fmt.Errorf("could not put the new program in place: %w", err)
	}
	return nil
}
