package subarchive

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/jellyfin"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
	"github.com/Aryan-mor/jellyfin-telegram/internal/sign"
	"github.com/Aryan-mor/jellyfin-telegram/internal/subtitles"
)

const (
	maxTries = 3
	// minCuesPerMinute decides that a track is hollow, such as an advertisement or signs only,
	// tagged as a full subtitle. Normal dialogue runs near ten cues a minute.
	minCuesPerMinute = 2
	// emptySRT stands in for a track with no cues. Jellyfin extracts again on an empty file.
	emptySRT = "1\n00:00:00,000 --> 00:00:00,500\n \n"
)

// Channel stores and returns archives in the Telegram channel.
type Channel interface {
	SendDocument(ctx context.Context, name, caption string, data []byte, replyTo int) (int, error)
	Download(ctx context.Context, messageID int, docID, size int64) ([]byte, error)
}

// Extractor runs ffmpeg over a whole stream at a limited speed.
type Extractor interface {
	ExtractTracks(ctx context.Context, url string, tracks []subtitles.ExtractTrack, dir string) error
}

// Worker handles one video at a time, so a whole library cannot flood Telegram.
type Worker struct {
	Catalog       *catalog.Catalog
	Client        *jellyfin.Client
	Prober        *jellyfin.Prober
	Channel       Channel
	Extractor     Extractor
	Resolver      *subtitles.Resolver // nil skips replacing hollow tracks
	SubtitlesRoot string              // Jellyfin's data/subtitles folder as seen from here
	StreamURL     func(messageID int) string
	Log           *slog.Logger
	// Forum claims a file before extraction and signs the caption again once the archive exists.
	// Nil keeps channel mode, where there is no claim.
	Forum Forum
}

// Forum is the supergroup claim. Claim is false when another installation holds the file.
type Forum interface {
	Claim(ctx context.Context, f media.File) (bool, error)
	// Resign marks the post ready. archiveHash names the subtitle archive, or is empty when the
	// file has none.
	Resign(ctx context.Context, f media.File, archiveHash string) error
	// ArchiveCaption is the signed caption of the archive posted for this file. Empty means the
	// plain one.
	ArchiveCaption(ctx context.Context, f media.File, archiveHash string, tracks []sign.Track) string
	FillIMDb(ctx context.Context, f media.File, imdb string) error
}

// errWait means another installation is doing this file. It is not a failed try.
var errWait = errors.New("claimed elsewhere")

// RunOnce handles the next video that needs work and reports whether it did anything.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	pending, err := w.Catalog.ExtractPending(ctx, maxTries)
	if err != nil || len(pending) == 0 {
		return false, err
	}
	userID, err := w.Client.AdminUserID(ctx)
	if err != nil {
		return false, err
	}
	index, err := w.Prober.Index(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, f := range pending {
		match, ok := index.Locate(f)
		if !ok {
			continue
		}
		if err := w.process(ctx, f, match, userID); err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			if errors.Is(err, errWait) {
				continue
			}
			w.Log.Warn("subtitle archive failed, will retry", "message", f.MessageID, "err", err)
			if terr := w.Catalog.AddExtractTry(ctx, f.MessageID); terr != nil {
				return false, terr
			}
		}
		return true, nil
	}
	return false, nil
}

// Verify puts back archived subtitles that Jellyfin's cache has lost. It reads only the archive.
func (w *Worker) Verify(ctx context.Context) error {
	done, err := w.Catalog.ExtractDone(ctx)
	if err != nil || len(done) == 0 {
		return err
	}
	userID, err := w.Client.AdminUserID(ctx)
	if err != nil {
		return err
	}
	index, err := w.Prober.Index(ctx, userID)
	if err != nil {
		return err
	}
	for _, f := range done {
		match, ok := index.Locate(f)
		if !ok {
			continue
		}
		info, err := w.Client.SubtitleStreams(ctx, match.SourceID, userID)
		if err != nil {
			return err
		}
		if !w.cacheComplete(match.SourceID, info) {
			w.Log.Info("subtitle cache lost, restoring from the channel", "message", f.MessageID)
			if err := w.Catalog.SetExtractState(ctx, f.MessageID, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *Worker) process(ctx context.Context, f media.File, m jellyfin.Located, userID string) error {
	if w.Forum != nil && m.Item.IMDb() != "" {
		if err := w.Forum.FillIMDb(ctx, f, m.Item.IMDb()); err != nil {
			return err
		}
	}
	info, err := w.Client.SubtitleStreams(ctx, m.SourceID, userID)
	if err != nil {
		return err
	}
	if len(info.Extractable) == 0 {
		return w.finish(ctx, f, "", media.ExtractNone)
	}

	// The cheapest source comes first: an archive in the channel needs no video read at all.
	if done, err := w.restore(ctx, f, m, userID, info); err != nil || done {
		return err
	}

	entries, err := w.loadCached(m.SourceID, info)
	if err != nil {
		return err
	}
	if entries == nil {
		if w.Forum != nil {
			won, err := w.Forum.Claim(ctx, f)
			if err != nil {
				return err
			}
			if !won {
				return errWait
			}
		}
		// Only this path reads the whole video.
		w.Log.Info("extracting subtitles", "message", f.MessageID, "tracks", len(info.Extractable), "mib", f.Size>>20)
		if entries, err = w.extract(ctx, f, info); err != nil {
			return err
		}
	}
	w.replaceHollow(ctx, f, m, userID, info, entries)
	if err := w.install(m.SourceID, info, entries); err != nil {
		return err
	}

	// The cache is filled first, so a failed upload costs no second read: the next try finds the
	// tracks in the cache.
	data, err := Build(entries)
	if err != nil {
		return err
	}
	name := Name(f.MessageID, f.DocID)
	hash := sign.ArchiveHash(data)
	caption := w.archiveCaption(ctx, f, hash, entries)
	sent, err := w.Channel.SendDocument(ctx, name, caption, data, f.MessageID)
	if err != nil {
		return fmt.Errorf("send archive: %w", err)
	}
	err = w.Catalog.PutAttachment(ctx, media.Attachment{
		MessageID: sent, FileName: name, ReplyTo: f.MessageID, Size: int64(len(data)),
	})
	if err != nil {
		return err
	}
	w.Log.Info("subtitles archived", "message", f.MessageID, "tracks", len(entries), "archive", sent)
	return w.finish(ctx, f, hash, media.ExtractDone)
}

// restore installs the archive that is already in the channel, if there is a good one. It reports
// whether the file is done. An archive whose bytes are not the ones the signed post names is
// ignored, so a wrong or tampered archive from another installation costs nothing but a warning.
func (w *Worker) restore(ctx context.Context, f media.File, m jellyfin.Located, userID string, info jellyfin.SubtitleInfo) (bool, error) {
	att, ok, err := w.findArchive(ctx, f)
	if err != nil || !ok {
		return false, err
	}
	data, err := w.Channel.Download(ctx, att.MessageID, att.DocID, att.Size)
	if err != nil {
		return false, fmt.Errorf("download archive: %w", err)
	}
	if meta, signed := sign.ParseCaption(f.Caption); signed && meta.ArchiveHash != "" && sign.ArchiveHash(data) != meta.ArchiveHash {
		w.Log.Warn("the archive in the channel is not the one its post names, ignoring it", "message", f.MessageID, "archive", att.MessageID)
		return false, nil
	}
	entries, err := Read(data)
	if err != nil {
		return false, err
	}
	w.replaceHollow(ctx, f, m, userID, info, entries)
	if err := w.install(m.SourceID, info, entries); err != nil {
		return false, err
	}
	w.Log.Info("subtitles restored from the channel", "message", f.MessageID, "tracks", len(entries))
	return true, w.finish(ctx, f, sign.ArchiveHash(data), media.ExtractDone)
}

// archiveCaption is the caption of the archive: signed in the shared library, plain otherwise.
func (w *Worker) archiveCaption(ctx context.Context, f media.File, hash string, entries []Entry) string {
	plain := fmt.Sprintf("Subtitles of message %d, kept by jellyfin-telegram.", f.MessageID)
	if w.Forum == nil {
		return plain
	}
	tracks := make([]sign.Track, 0, len(entries))
	for _, e := range entries {
		lang := e.Language
		if lang == "" {
			lang = "und"
		}
		tracks = append(tracks, sign.Track{Lang: lang, Codec: e.Ext, Index: e.Position, HasIndex: true})
	}
	if c := w.Forum.ArchiveCaption(ctx, f, hash, tracks); c != "" {
		return c
	}
	return plain
}

// finish marks the file as handled and, in the shared library, marks its post ready.
func (w *Worker) finish(ctx context.Context, f media.File, archiveHash, state string) error {
	if w.Forum != nil {
		if err := w.Forum.Resign(ctx, f, archiveHash); err != nil {
			return err
		}
	}
	return w.Catalog.SetExtractState(ctx, f.MessageID, state)
}

func (w *Worker) findArchive(ctx context.Context, f media.File) (media.Attachment, bool, error) {
	all, err := w.Catalog.Attachments(ctx)
	if err != nil {
		return media.Attachment{}, false, err
	}
	want := Name(f.MessageID, f.DocID)
	// The name ends with the document id of the video, which a forward or a copy keeps, so the
	// archive is found even when the video has another message id in this channel.
	ending := "-" + strconv.FormatInt(f.DocID, 10) + ".zip"
	var other media.Attachment
	found := false
	for _, a := range all {
		// DocID is empty on a row written right after sending, until the next sync fills it.
		if a.DocID == 0 {
			continue
		}
		if a.FileName == want {
			return a, true, nil
		}
		if !found && IsArchiveName(a.FileName) && strings.HasSuffix(a.FileName, ending) {
			other, found = a, true
		}
	}
	return other, found, nil
}

func (w *Worker) extract(ctx context.Context, f media.File, info jellyfin.SubtitleInfo) ([]Entry, error) {
	dir, err := os.MkdirTemp("", "jftg-extract-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	tracks := make([]subtitles.ExtractTrack, 0, len(info.Extractable))
	for _, s := range info.Extractable {
		tracks = append(tracks, subtitles.ExtractTrack{Position: s.Position, Codec: s.Codec, Ext: s.Ext()})
	}
	if err := w.Extractor.ExtractTracks(ctx, w.StreamURL(f.MessageID), tracks, dir); err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(tracks))
	for _, s := range info.Extractable {
		data, err := os.ReadFile(filepath.Join(dir, strconv.Itoa(s.Position)+"."+s.Ext()))
		if err != nil {
			return nil, fmt.Errorf("track %d: %w", s.Position, err)
		}
		if len(data) == 0 {
			data = placeholder(s.Ext())
		}
		entries = append(entries, Entry{Position: s.Position, Ext: s.Ext(), Language: s.Language, Data: data})
	}
	return entries, nil
}

func placeholder(ext string) []byte {
	if ext == "srt" {
		return []byte(emptySRT)
	}
	// Jellyfin only needs the file to be non-empty to skip extraction.
	return []byte{0}
}

// replaceHollow swaps a hollow track in the user's language for a real subtitle. A failure only
// means the track stays as it was.
func (w *Worker) replaceHollow(ctx context.Context, f media.File, m jellyfin.Located, userID string, info jellyfin.SubtitleInfo, entries []Entry) {
	if w.Resolver == nil || w.Resolver.Language == "" {
		return
	}
	minutes := float64(f.DurationSeconds) / 60
	target := -1
	for i, e := range entries {
		if e.Language != w.Resolver.Language {
			continue
		}
		if float64(countCues(e)) >= minCuesPerMinute*minutes {
			return // A real track in the language exists.
		}
		if target < 0 && e.Ext == "srt" {
			target = i
		}
	}
	if target < 0 {
		return
	}
	meta := w.Prober.Meta(ctx, f, m.Item, userID)
	file, err := w.Resolver.FindReplacement(ctx, meta, f.DurationSeconds)
	if err != nil {
		w.Log.Warn("no replacement for a hollow track", "message", f.MessageID, "err", err)
		return
	}
	if file == nil || file.Ext != ".srt" {
		w.Log.Info("hollow track has no replacement", "message", f.MessageID)
		return
	}
	entries[target].Data = []byte(file.Text)
	w.Log.Info("hollow track replaced", "message", f.MessageID, "cues", strings.Count(file.Text, "-->"))
	if err := w.Catalog.SetSubtitleOutcome(ctx, f.MessageID, subtitles.OutcomeHollow); err != nil {
		w.Log.Warn("cannot record subtitle outcome", "message", f.MessageID, "err", err)
	}
}

func countCues(e Entry) int {
	if e.Ext == "srt" {
		return strings.Count(string(e.Data), "-->")
	}
	return strings.Count(string(e.Data), "Dialogue:")
}

func (w *Worker) folder(sourceID string) (string, error) {
	id := sourceID
	if len(id) == 32 {
		id = id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
	}
	if len(id) != 36 || strings.ContainsAny(id, `/\.`) {
		return "", fmt.Errorf("unexpected media source id %q", sourceID)
	}
	return filepath.Join(w.SubtitlesRoot, id[:2], id), nil
}

func (w *Worker) cacheComplete(sourceID string, info jellyfin.SubtitleInfo) bool {
	dir, err := w.folder(sourceID)
	if err != nil {
		return false
	}
	for _, s := range info.Extractable {
		st, err := os.Stat(filepath.Join(dir, strconv.Itoa(s.Index)+"."+s.Ext()))
		if err != nil || st.Size() == 0 {
			return false
		}
	}
	return true
}

// loadCached returns tracks Jellyfin already extracted, or nil unless it has all of them.
func (w *Worker) loadCached(sourceID string, info jellyfin.SubtitleInfo) ([]Entry, error) {
	if !w.cacheComplete(sourceID, info) {
		return nil, nil
	}
	dir, err := w.folder(sourceID)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(info.Extractable))
	for _, s := range info.Extractable {
		data, err := os.ReadFile(filepath.Join(dir, strconv.Itoa(s.Index)+"."+s.Ext()))
		if err != nil {
			return nil, err
		}
		entries = append(entries, Entry{Position: s.Position, Ext: s.Ext(), Language: s.Language, Data: data})
	}
	return entries, nil
}

// install writes every track where Jellyfin looks for it. All tracks are needed: Jellyfin extracts
// any that are missing whenever one embedded track is requested.
func (w *Worker) install(sourceID string, info jellyfin.SubtitleInfo, entries []Entry) error {
	byPosition := make(map[int]Entry, len(entries))
	for _, e := range entries {
		byPosition[e.Position] = e
	}
	dir, err := w.folder(sourceID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, s := range info.Extractable {
		e, ok := byPosition[s.Position]
		if !ok {
			return errors.New("archive does not cover every track of this file")
		}
		if err := writeFileAtomic(filepath.Join(dir, strconv.Itoa(s.Index)+"."+s.Ext()), e.Data); err != nil {
			return err
		}
	}
	return nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
