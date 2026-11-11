package subtitles

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Aryan-mor/jellyfin-telegram/internal/proc"
)

// BackgroundHeader marks a request as background work, so the gateway limits its speed and pauses
// it while anything is playing.
const BackgroundHeader = "X-Jftg-Background"

// ExtractTrack is one track to save.
type ExtractTrack struct {
	Position int // the stream's place in the file, as ffmpeg numbers it
	Codec    string
	Ext      string
}

// ExtractTracks saves every track in one pass over the file, named <position>.<ext> in dir.
// It reads the whole video. ExtractSparse is the path that does not: this remains for a
// container whose index does not point at the subtitle bytes.
func (t Tools) ExtractTracks(ctx context.Context, url string, tracks []ExtractTrack, dir string) error {
	if t.FFmpeg == "" {
		return errors.New("ffmpeg not found")
	}
	args := []string{"-nostdin", "-v", "error", "-y", "-headers", BackgroundHeader + ": 1\r\n", "-i", url}
	for _, tr := range tracks {
		codec := "srt"
		if canCopy(tr.Codec) {
			codec = "copy"
		}
		args = append(args, "-map", "0:"+strconv.Itoa(tr.Position), "-an", "-vn", "-c:s", codec,
			filepath.Join(dir, strconv.Itoa(tr.Position)+"."+tr.Ext))
	}
	if err := proc.Command(ctx, t.FFmpeg, args...).Run(); err != nil {
		// The error text can hold the URL with its token, so only the kind is reported.
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return fmt.Errorf("ffmpeg exited with code %d", exit.ExitCode())
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("ffmpeg could not run")
	}
	return nil
}

func canCopy(codec string) bool {
	switch strings.ToLower(codec) {
	case "ass", "ssa", "srt", "subrip", "pgssub":
		return true
	}
	return false
}
