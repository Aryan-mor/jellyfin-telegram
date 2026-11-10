package managed

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ulikunitz/xz"
)

// Paths locates an installed server.
type Paths struct {
	Server  string
	WebDir  string
	FFmpeg  string
	FFprobe string
}

// Progress reports a download: its name, and bytes done of the total, which may be unknown. The
// unpacking that follows is reported with the name prefixed by UnpackPrefix.
type Progress func(name string, done, total int64)

// UnpackPrefix starts the name of a Progress report that is about unpacking, not downloading.
const UnpackPrefix = "Unpacking "

// Install makes sure a release is present below root and returns where it is. A finished install
// is marked, so an interrupted one starts over instead of running a partial copy.
func Install(ctx context.Context, root string, r Release, client *http.Client, progress Progress) (Paths, error) {
	dir := filepath.Join(root, r.Version)
	paths := pathsFor(dir, r)
	marker := filepath.Join(dir, ".installed")
	if _, err := os.Stat(marker); err == nil {
		return paths, nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return Paths{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Paths{}, err
	}
	if err := fetch(ctx, r.Server, "Jellyfin", dir, client, progress); err != nil {
		return Paths{}, err
	}
	if r.FFmpeg != nil {
		target := filepath.Join(dir, "ffmpeg")
		if err := os.MkdirAll(target, 0o755); err != nil {
			return Paths{}, err
		}
		if err := fetch(ctx, *r.FFmpeg, "ffmpeg", target, client, progress); err != nil {
			return Paths{}, err
		}
	}
	if _, err := os.Stat(paths.Server); err != nil {
		return Paths{}, fmt.Errorf("the download holds no server at %s", paths.Server)
	}
	if err := os.WriteFile(marker, []byte(r.Version+"\n"), 0o644); err != nil {
		return Paths{}, err
	}
	return paths, nil
}

func pathsFor(dir string, r Release) Paths {
	exe := func(name string) string {
		if runtime.GOOS == "windows" {
			return name + ".exe"
		}
		return name
	}
	server := filepath.Join(dir, "jellyfin")
	tools := server
	if r.FFmpeg != nil {
		tools = filepath.Join(dir, "ffmpeg")
	}
	return Paths{
		Server:  filepath.Join(server, exe("jellyfin")),
		WebDir:  filepath.Join(server, "jellyfin-web"),
		FFmpeg:  filepath.Join(tools, exe("ffmpeg")),
		FFprobe: filepath.Join(tools, exe("ffprobe")),
	}
}

func fetch(ctx context.Context, a Artifact, name, into string, client *http.Client, progress Progress) error {
	tmp, err := os.CreateTemp(into, ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", name, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", name, res.StatusCode)
	}
	hash := sha256.New()
	counter := &countingWriter{name: name, total: res.ContentLength, progress: progress}
	if _, err := io.Copy(io.MultiWriter(tmp, hash, counter), res.Body); err != nil {
		return fmt.Errorf("download %s: %w", name, err)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != a.SHA256 {
		return fmt.Errorf("download %s: checksum mismatch, refusing to run it (got %s)", name, got)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	unpack := func(done, total int64) {
		if progress != nil {
			progress(UnpackPrefix+name, done, total)
		}
	}
	unpack(0, 1)
	if err := extract(a.URL, tmp, into, unpack); err != nil {
		return err
	}
	unpack(1, 1)
	return nil
}

// countingReader reports how much of a file an unpacker has read.
type countingReader struct {
	r      io.Reader
	done   int64
	total  int64
	report func(done, total int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.done += int64(n)
	c.report(c.done, c.total)
	return n, err
}

type countingWriter struct {
	name     string
	done     int64
	total    int64
	progress Progress
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.done += int64(len(p))
	if c.progress != nil {
		c.progress(c.name, c.done, c.total)
	}
	return len(p), nil
}

// extract unpacks an archive. report is told how far it is: the bytes of a tar archive read so
// far, or the bytes of the files written for a zip.
func extract(name string, f *os.File, into string, report func(done, total int64)) error {
	base := path.Base(name)
	st, err := f.Stat()
	if err != nil {
		return err
	}
	counted := &countingReader{r: f, total: st.Size(), report: report}
	switch {
	case strings.HasSuffix(base, ".zip"):
		return extractZip(f, st.Size(), into, report)
	case strings.HasSuffix(base, ".tar.gz"):
		gz, err := gzip.NewReader(counted)
		if err != nil {
			return err
		}
		defer gz.Close()
		return extractTar(gz, into)
	case strings.HasSuffix(base, ".tar.xz"):
		xr, err := xz.NewReader(counted)
		if err != nil {
			return err
		}
		return extractTar(xr, into)
	}
	return fmt.Errorf("unknown archive type: %s", base)
}

// target resolves an archive entry and refuses one that would land outside the folder.
func target(into, name string) (string, error) {
	local := filepath.FromSlash(strings.TrimPrefix(name, "./"))
	if local == "" || local == "." {
		return into, nil
	}
	if !filepath.IsLocal(local) {
		return "", fmt.Errorf("archive entry escapes its folder: %q", name)
	}
	return filepath.Join(into, local), nil
}

func extractZip(r io.ReaderAt, size int64, into string, report func(done, total int64)) error {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return err
	}
	var total, done int64
	for _, f := range zr.File {
		total += int64(f.UncompressedSize64)
	}
	for _, f := range zr.File {
		dest, err := target(into, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeFile(dest, rc, 0o755)
		rc.Close()
		if err != nil {
			return err
		}
		done += int64(f.UncompressedSize64)
		report(done, total)
	}
	return nil
}

func extractTar(r io.Reader, into string) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		dest, err := target(into, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(dest, tr, os.FileMode(h.Mode)&0o777|0o600); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// A link must stay inside the install, or it could redirect later writes.
			if !filepath.IsLocal(filepath.Join(filepath.Dir(strings.TrimPrefix(h.Name, "./")), h.Linkname)) {
				return fmt.Errorf("archive link escapes its folder: %q", h.Name)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(h.Linkname, dest); err != nil {
				return err
			}
		}
	}
}

func writeFile(dest string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
