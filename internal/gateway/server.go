package gateway

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
)

// FileLookup returns catalog entries.
type FileLookup interface {
	Get(ctx context.Context, messageID int) (media.File, error)
}

// Options configures the HTTP handler.
type Options struct {
	Token     string
	ChannelID int64
	Readahead int
	// BackgroundBytesPerSecond limits requests marked with BackgroundHeader.
	BackgroundBytesPerSecond int64
}

// BackgroundHeader marks a request as background work. It matches the header the subtitle
// extraction sends.
const BackgroundHeader = "X-Jftg-Background"

// Handler returns the gateway routes. Request paths hold the secret token, so nothing
// here logs a path.
func Handler(e *Engine, files FileLookup, o Options, log *slog.Logger) http.Handler {
	s := &server{engine: e, files: files, opts: o, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /s/{token}/{channel}/{message}", s.stream)
	return mux
}

type server struct {
	engine *Engine
	files  FileLookup
	opts   Options
	log    *slog.Logger
}

func (s *server) stream(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.PathValue("token")), []byte(s.opts.Token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	channel, err1 := strconv.ParseInt(r.PathValue("channel"), 10, 64)
	messageID, err2 := strconv.Atoi(r.PathValue("message"))
	if err1 != nil || err2 != nil || channel != s.opts.ChannelID {
		http.NotFound(w, r)
		return
	}
	file, err := s.files.Get(r.Context(), messageID)
	if errors.Is(err, catalog.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.log.Error("catalog lookup failed", "message", messageID, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Fetch the first block before sending headers, so a Telegram failure becomes a
	// clean 502 and not a truncated 200.
	if r.Method == http.MethodGet {
		if start, ok := rangeStart(r.Header.Get("Range"), file.Size); ok {
			if _, err := s.engine.Block(r.Context(), file, start/media.BlockSize); err != nil {
				if r.Context().Err() == nil {
					s.log.Error("cannot read from telegram", "message", messageID, "err", err)
					http.Error(w, "cannot read from telegram", http.StatusBadGateway)
				}
				return
			}
		}
	}

	contentType := file.MIME
	if !strings.Contains(contentType, "/") {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("ETag", fmt.Sprintf(`"%d-%d"`, file.DocID, file.Size))
	reader := newReader(r.Context(), s.engine, file, s.opts.Readahead)
	reader.background = r.Header.Get(BackgroundHeader) != "" && s.opts.BackgroundBytesPerSecond > 0
	if reader.background {
		reader.pacer = newBackgroundPacer(s.opts.BackgroundBytesPerSecond, s.engine.busy)
	} else {
		reader.pacer = s.engine.limit(r, file)
	}
	http.ServeContent(w, r, "", file.Date, reader)
}

// rangeStart returns the first requested byte for the common single-range form.
func rangeStart(header string, size int64) (int64, bool) {
	if size <= 0 {
		return 0, false
	}
	if header == "" {
		return 0, true
	}
	spec, ok := strings.CutPrefix(header, "bytes=")
	if !ok || strings.Contains(spec, ",") {
		return 0, false
	}
	first, _, ok := strings.Cut(spec, "-")
	if !ok || first == "" {
		return 0, false
	}
	start, err := strconv.ParseInt(first, 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, false
	}
	return start, true
}
