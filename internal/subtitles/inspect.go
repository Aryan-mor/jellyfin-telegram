package subtitles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/lang"
	"github.com/Aryan-mor/jellyfin-telegram/internal/proc"
)

const (
	// sampleBytes is what a language sniff reads. Bytes past it are never requested, so the
	// sniff cannot grow into a read of the whole file.
	sampleBytes    = 12 << 20
	maxSniffTracks = 4
	toolTimeout    = 2 * time.Minute
)

// Track is one audio or subtitle track.
type Track struct {
	Kind     string // "audio" or "subtitle"
	Index    int
	Codec    string
	Language string // normalized, empty when unknown
	Title    string
}

// Inspection is what the first bytes of a file reveal.
type Inspection struct {
	Tracks  []Track
	Seconds int // zero when unknown
}

// Tools locates ffprobe and ffmpeg.
type Tools struct {
	FFprobe string
	FFmpeg  string
}

// FindTools looks at the configured path, then PATH, then the Jellyfin install folder.
func FindTools(configured string) (Tools, error) {
	probe := configured
	if probe == "" {
		probe, _ = exec.LookPath("ffprobe")
	}
	if probe == "" {
		for _, p := range jellyfinToolDirs() {
			candidate := filepath.Join(p, exeName("ffprobe"))
			if _, err := os.Stat(candidate); err == nil {
				probe = candidate
				break
			}
		}
	}
	if probe == "" {
		return Tools{}, errors.New("ffprobe not found, set FFPROBE_PATH")
	}
	mpeg := filepath.Join(filepath.Dir(probe), exeName("ffmpeg"))
	if _, err := os.Stat(mpeg); err != nil {
		mpeg, _ = exec.LookPath("ffmpeg")
	}
	return Tools{FFprobe: probe, FFmpeg: mpeg}, nil
}

func jellyfinToolDirs() []string {
	if runtime.GOOS == "windows" {
		return []string{`C:\Program Files\Jellyfin\Server`}
	}
	return []string{"/usr/lib/jellyfin-ffmpeg", "/usr/share/jellyfin-ffmpeg"}
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// Inspect lists the tracks of a stream. ffprobe reads only the container header, plus the
// index at the end of an MP4 that keeps it there.
func (t Tools) Inspect(ctx context.Context, url string) (Inspection, error) {
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	out, err := proc.Command(ctx, t.FFprobe, "-v", "error", "-print_format", "json", "-show_streams", "-show_format",
		"-probesize", "32000000", "-analyzeduration", "10000000", url).Output()
	if err != nil {
		// The error text can hold the URL with its token, so only the kind is reported.
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return Inspection{}, fmt.Errorf("ffprobe exited with code %d", exit.ExitCode())
		}
		return Inspection{}, errors.New("ffprobe could not run")
	}
	return parseProbe(out)
}

func parseProbe(raw []byte) (Inspection, error) {
	var body struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Index     int               `json:"index"`
			CodecType string            `json:"codec_type"`
			CodecName string            `json:"codec_name"`
			Tags      map[string]string `json:"tags"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return Inspection{}, fmt.Errorf("ffprobe output: %w", err)
	}
	var out Inspection
	if d, err := strconv.ParseFloat(body.Format.Duration, 64); err == nil {
		out.Seconds = int(d)
	}
	for _, s := range body.Streams {
		if s.CodecType != "audio" && s.CodecType != "subtitle" {
			continue
		}
		code := s.Tags["language"]
		if code == "" {
			code = s.Tags["LANGUAGE"]
		}
		title := s.Tags["title"]
		if title == "" {
			title = s.Tags["TITLE"]
		}
		out.Tracks = append(out.Tracks, Track{
			Kind: s.CodecType, Index: s.Index, Codec: s.CodecName,
			Language: lang.Normalize(code), Title: title,
		})
	}
	return out, nil
}

// HasTrack reports a track of this kind in the language, by its tag or its title.
func HasTrack(tracks []Track, kind, code string) bool {
	for _, t := range tracks {
		if t.Kind == kind && (t.Language == code || lang.InTitle(code, t.Title)) {
			return true
		}
	}
	return false
}

// sniffCandidates returns text tracks whose tag may be wrong. Releases often leave the tag unset
// or leave it on the English default of the tool that made them.
func sniffCandidates(tracks []Track) []Track {
	var out []Track
	for _, t := range tracks {
		if t.Kind != "subtitle" || !isTextCodec(t.Codec) || (t.Language != "" && t.Language != "en") {
			continue
		}
		out = append(out, t)
		if len(out) == maxSniffTracks {
			break
		}
	}
	return out
}

func isTextCodec(codec string) bool {
	switch strings.ToLower(codec) {
	case "subrip", "srt", "ass", "ssa", "webvtt", "mov_text", "text":
		return true
	}
	return false
}

// SniffLanguage checks mis-tagged tracks against a sample from the start of the file. It saves
// the first 12 MiB locally and runs ffmpeg on that file, so ffmpeg cannot read further.
func (t Tools) SniffLanguage(ctx context.Context, url, code string, tracks []Track) (bool, error) {
	candidates := sniffCandidates(tracks)
	if len(candidates) == 0 || t.FFmpeg == "" {
		return false, nil
	}
	sample, err := saveSample(ctx, url)
	if err != nil {
		return false, err
	}
	defer os.Remove(sample)
	for _, c := range candidates {
		cctx, cancel := context.WithTimeout(ctx, toolTimeout)
		out, err := proc.Command(cctx, t.FFmpeg, "-v", "quiet", "-i", sample,
			"-map", fmt.Sprintf("0:%d", c.Index), "-f", "srt", "-").Output()
		cancel()
		if err == nil && lang.IsLanguage(code, string(out)) {
			return true, nil
		}
	}
	return false, nil
}

func saveSample(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", errors.New("bad stream url")
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", sampleBytes-1))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", errors.New("cannot read sample")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusPartialContent && res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("cannot read sample: HTTP %d", res.StatusCode)
	}
	f, err := os.CreateTemp("", "jftg-sample-*.bin")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, io.LimitReader(res.Body, sampleBytes))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
