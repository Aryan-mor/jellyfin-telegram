// Package indexer keeps the catalog and the .strm tree in step with the channel.
package indexer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/library"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
	"github.com/Aryan-mor/jellyfin-telegram/internal/naming"
)

// errSkip means the file is not a film or an episode and was not identified, so it stays out of
// the library. The message id still advances, and a later edit is not replayed.
var errSkip = errors.New("unidentified")

const (
	batchSize = 100
	// FullWindow is how many empty ids a full walk tolerates before it stops. Bots
	// cannot read channel history, so ids are probed in order and a long run of
	// deleted messages looks the same as the end of the channel.
	FullWindow = 2000
	// IncrementalWindow is the empty run tolerated by a routine check for new messages.
	IncrementalWindow = batchSize
	callTimeout       = 5 * time.Minute
	defaultPace       = 150 * time.Millisecond
)

// Source reads messages from the channel.
type Source interface {
	Messages(ctx context.Context, ids []int) ([]media.Message, error)
	Thumbnail(ctx context.Context, messageID int) ([]byte, error)
	DeleteMessages(ctx context.Context, ids []int) error
}

// ArchivePrefix starts the name of a subtitle archive this service made. The indexer only needs
// to tell those apart from files the user posted, which it must never delete.
const ArchivePrefix = "jftg-subs-"

// BlockRemover drops cached data of a document.
type BlockRemover interface {
	RemoveDoc(doc int64) error
}

// Identifier finds the TMDB id of a movie from its title. ok is false when the title is unknown
// or ambiguous.
type Identifier interface {
	Movie(ctx context.Context, title string) (tmdbID int, ok bool, err error)
}

// Indexer syncs the channel into the catalog and library.
type Indexer struct {
	src    Source
	cat    *catalog.Catalog
	lib    *library.Library
	blocks BlockRemover
	log    *slog.Logger
	pace   time.Duration

	ident Identifier
	mu    sync.Mutex
	known map[string]int // title to TMDB id, 0 for a title that did not resolve

	// personal files every video and photo under Home/, with no title lookup. It is the
	// Home Videos and Photos channel, which nothing fills except a person.
	personal bool
}

// Downloader reads a whole small file, used for photos in the personal library.
type Downloader interface {
	Download(ctx context.Context, messageID int, docID, size int64) ([]byte, error)
}

// SetPersonal puts this indexer on the personal channel: every video and photo is kept, and
// nothing is matched to a film.
func (x *Indexer) SetPersonal() { x.personal = true }

// New returns an Indexer.
func New(src Source, cat *catalog.Catalog, lib *library.Library, blocks BlockRemover, log *slog.Logger) *Indexer {
	return &Indexer{src: src, cat: cat, lib: lib, blocks: blocks, log: log, pace: defaultPace}
}

// SetIdentifier turns on title lookup for movies that come without a year or id. Without it such
// movies get local metadata from their caption.
func (x *Indexer) SetIdentifier(i Identifier) { x.ident = i }

// Sync walks ids after the last seen message and stops after window empty ids.
// It returns how many videos were added.
func (x *Indexer) Sync(ctx context.Context, window int) (int, error) {
	last, err := x.cat.LastMessageID(ctx)
	if err != nil {
		return 0, err
	}
	added, empty := 0, 0
	for next := last + 1; empty < window; next += batchSize {
		msgs, err := x.lookup(ctx, idRange(next, batchSize))
		if err != nil {
			return added, err
		}
		seen := false
		for _, m := range msgs {
			if !m.Exists {
				continue
			}
			seen = true
			last = max(last, m.ID)
			if m.Attachment != nil {
				if err := x.cat.PutAttachment(ctx, *m.Attachment); err != nil {
					return added, err
				}
			}
			if m.File == nil {
				continue
			}
			if err := x.add(ctx, *m.File); err != nil {
				if errors.Is(err, errSkip) {
					continue
				}
				return added, fmt.Errorf("add message %d: %w", m.ID, err)
			}
			added++
		}
		if seen {
			empty = 0
			if err := x.cat.SetLastMessageID(ctx, last); err != nil {
				return added, err
			}
		} else {
			empty += batchSize
		}
		if err := sleep(ctx, x.pace); err != nil {
			return added, err
		}
	}
	return added, nil
}

// Reconcile re-reads every known message, then removes deleted files and picks up
// edits. A file keeps its library path when its name or caption changes, so Jellyfin
// keeps its item and watch state. It returns how many files were removed.
func (x *Indexer) Reconcile(ctx context.Context) (int, error) {
	all, err := x.cat.All(ctx)
	if err != nil {
		return 0, err
	}
	var gone []media.File
	var changed []media.File
	for start := 0; start < len(all); start += batchSize {
		batch := all[start:min(start+batchSize, len(all))]
		ids := make([]int, len(batch))
		for i, f := range batch {
			ids[i] = f.MessageID
		}
		msgs, err := x.lookup(ctx, ids)
		if err != nil {
			return 0, err
		}
		byID := make(map[int]media.Message, len(msgs))
		for _, m := range msgs {
			byID[m.ID] = m
		}
		for _, f := range batch {
			m := byID[f.MessageID]
			if m.File == nil {
				gone = append(gone, f)
				continue
			}
			if m.File.DocID != f.DocID || m.File.Caption != f.Caption || m.File.FileName != f.FileName {
				next := *m.File
				next.RelPath = f.RelPath
				changed = append(changed, next)
				if m.File.DocID != f.DocID {
					if err := x.blocks.RemoveDoc(f.DocID); err != nil {
						x.log.Warn("cannot drop cached blocks", "message", f.MessageID, "err", err)
					}
				}
			}
		}
		if err := sleep(ctx, x.pace); err != nil {
			return 0, err
		}
	}

	// Every file vanishing at once points at a wrong channel or a Telegram fault, not
	// at a real mass deletion, so leave the library alone.
	if len(all) >= 2 && len(gone) == len(all) {
		x.log.Error("every known message looks deleted, leaving the library untouched", "files", len(all))
		return 0, nil
	}
	for _, f := range changed {
		if err := x.cat.Put(ctx, f); err != nil {
			return 0, err
		}
		x.refreshSidecars(ctx, f)
	}
	for _, f := range gone {
		if err := x.remove(ctx, f); err != nil {
			return 0, fmt.Errorf("remove message %d: %w", f.MessageID, err)
		}
		x.log.Info("file removed", "message", f.MessageID, "path", f.RelPath)
	}
	if err := x.reconcileAttachments(ctx, gone); err != nil {
		return 0, err
	}
	return len(gone), nil
}

// reconcileAttachments drops subtitle files deleted from the channel. A video that lost its archive
// gets a new one. A deleted video's own archive is deleted too, because nothing can use it.
func (x *Indexer) reconcileAttachments(ctx context.Context, goneVideos []media.File) error {
	all, err := x.cat.Attachments(ctx)
	if err != nil || len(all) == 0 {
		return err
	}
	removed := make(map[int]bool, len(goneVideos))
	for _, f := range goneVideos {
		removed[f.MessageID] = true
	}
	var orphans []int
	for start := 0; start < len(all); start += batchSize {
		batch := all[start:min(start+batchSize, len(all))]
		ids := make([]int, len(batch))
		for i, a := range batch {
			ids[i] = a.MessageID
		}
		msgs, err := x.lookup(ctx, ids)
		if err != nil {
			return err
		}
		exists := make(map[int]bool, len(msgs))
		for _, m := range msgs {
			if m.Attachment != nil {
				exists[m.ID] = true
			}
		}
		for _, a := range batch {
			isArchive := strings.HasPrefix(a.FileName, ArchivePrefix)
			switch {
			case !exists[a.MessageID]:
				if err := x.cat.DeleteAttachment(ctx, a.MessageID); err != nil {
					return err
				}
				if isArchive && a.ReplyTo != 0 {
					if err := x.cat.SetExtractState(ctx, a.ReplyTo, ""); err != nil {
						return err
					}
				}
			case isArchive && removed[a.ReplyTo]:
				orphans = append(orphans, a.MessageID)
			}
		}
		if err := sleep(ctx, x.pace); err != nil {
			return err
		}
	}
	if len(orphans) == 0 {
		return nil
	}
	dctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if err := x.src.DeleteMessages(dctx, orphans); err != nil {
		x.log.Warn("cannot delete old subtitle archives", "err", err)
		return nil
	}
	for _, id := range orphans {
		if err := x.cat.DeleteAttachment(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// Rewrite restores missing .strm files and applies a changed gateway URL or token.
func (x *Indexer) Rewrite(ctx context.Context) error {
	all, err := x.cat.All(ctx)
	if err != nil {
		return err
	}
	for _, f := range all {
		if strings.HasPrefix(f.RelPath, "Music/") || !strings.HasSuffix(f.RelPath, ".strm") {
			continue
		}
		if _, err := x.lib.WriteStrm(f.RelPath, f.MessageID); err != nil {
			return fmt.Errorf("write %s: %w", f.RelPath, err)
		}
	}
	return nil
}

func (x *Indexer) add(ctx context.Context, f media.File) error {
	if x.personal {
		return x.addHome(ctx, f)
	}
	if naming.Section(f.FileName, f.Caption, f.MIME) == naming.SectionMusic {
		return x.addMusic(ctx, f)
	}
	p := naming.Parse(f.FileName, f.Caption)
	fromTitle := 0
	if !p.Matchable() {
		id, ok, err := x.identify(ctx, p)
		if err != nil {
			return err
		}
		if !ok {
			x.log.Info("skipped unidentified", "message", f.MessageID, "file", f.FileName, "title", p.Title)
			return errSkip
		}
		// The folder name stays without the id, so a later correction does not move the item.
		fromTitle = id
	}
	rel := p.RelPath(f.MessageID, "")
	taken, err := x.cat.PathTaken(ctx, rel, f.MessageID)
	if err != nil {
		return err
	}
	if taken {
		rel = p.RelPath(f.MessageID, "tg"+strconv.Itoa(f.MessageID))
	}
	f.RelPath = rel
	if err := x.cat.Put(ctx, f); err != nil {
		return err
	}
	if _, err := x.lib.WriteStrm(rel, f.MessageID); err != nil {
		return err
	}
	if fromTitle > 0 {
		if err := x.lib.WriteTMDBSidecar(f.RelPath, fromTitle); err != nil {
			x.log.Warn("cannot write TMDB id", "message", f.MessageID, "err", err)
		} else {
			x.log.Info("title identified", "message", f.MessageID, "title", p.Title, "tmdb", fromTitle)
		}
	}
	x.log.Info("file added", "message", f.MessageID, "path", rel)
	return nil
}

// addHome keeps a personal video or photo. A photo is stored itself, because Jellyfin reads an
// image file and not a stream pointer. A video is a .strm aimed at the personal channel.
func (x *Indexer) addHome(ctx context.Context, f media.File) error {
	if isImage(f) {
		return x.addHomeImage(ctx, f)
	}
	rel := homePath(f, ".strm")
	taken, err := x.cat.PathTaken(ctx, rel, f.MessageID)
	if err != nil {
		return err
	}
	if taken {
		rel = homePath(f, " tg"+strconv.Itoa(f.MessageID)+".strm")
	}
	f.RelPath = rel
	if err := x.cat.Put(ctx, f); err != nil {
		return err
	}
	if _, err := x.lib.WriteStrm(rel, f.MessageID); err != nil {
		return err
	}
	x.log.Info("home video added", "message", f.MessageID, "path", rel)
	return nil
}

func (x *Indexer) addHomeImage(ctx context.Context, f media.File) error {
	dl, ok := x.src.(Downloader)
	if !ok {
		return fmt.Errorf("cannot save photo %d", f.MessageID)
	}
	data, err := dl.Download(ctx, f.MessageID, f.DocID, f.Size)
	if err != nil {
		return err
	}
	ext := strings.ToLower(path.Ext(f.FileName))
	if ext == "" {
		ext = ".jpg"
	}
	rel := homePath(f, ext)
	f.RelPath = rel
	if err := x.cat.Put(ctx, f); err != nil {
		return err
	}
	if err := x.lib.WriteBytes(rel, data); err != nil {
		return err
	}
	x.log.Info("home photo added", "message", f.MessageID, "path", rel)
	return nil
}

func homePath(f media.File, tail string) string {
	stem := strings.TrimSuffix(f.FileName, path.Ext(f.FileName))
	stem = naming.Sanitize(stem)
	if stem == "" {
		stem = "Item"
	}
	if !strings.HasPrefix(tail, " ") && !strings.HasPrefix(tail, ".") {
		tail = " " + tail
	}
	return path.Join("Home", stem+" tg"+strconv.Itoa(f.MessageID)+tail)
}

func isImage(f media.File) bool {
	if strings.HasPrefix(strings.ToLower(f.MIME), "image/") {
		return true
	}
	switch strings.ToLower(path.Ext(f.FileName)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".heic", ".heif", ".tif", ".tiff", ".bmp":
		return true
	}
	return false
}

// addMusic keeps a song in the catalog so a mirror can copy it, and out of the film library.
func (x *Indexer) addMusic(ctx context.Context, f media.File) error {
	stem := strings.TrimSpace(strings.TrimSuffix(f.FileName, path.Ext(f.FileName)))
	if stem == "" {
		stem = "Audio"
	}
	f.RelPath = path.Join("Music", naming.Sanitize(stem)+" tg"+strconv.Itoa(f.MessageID)+".strm")
	if err := x.cat.Put(ctx, f); err != nil {
		return err
	}
	if err := x.cat.MarkProbed(ctx, f.MessageID, time.Now()); err != nil {
		return err
	}
	if err := x.cat.SetSubtitleOutcome(ctx, f.MessageID, "music"); err != nil {
		return err
	}
	if err := x.cat.SetExtractState(ctx, f.MessageID, media.ExtractNone); err != nil {
		return err
	}
	x.log.Info("music added", "message", f.MessageID, "path", f.RelPath)
	return nil
}

// refreshSidecars follows an edited name or caption. A title that gained a year or id drops its
// local metadata. A title that still cannot be identified leaves the library.
func (x *Indexer) refreshSidecars(ctx context.Context, f media.File) {
	if x.personal || strings.HasPrefix(f.RelPath, "Music/") || strings.HasPrefix(f.RelPath, "Home/") {
		return
	}
	p := naming.Parse(f.FileName, f.Caption)
	if p.Episode {
		return
	}
	if p.Matchable() {
		if err := x.lib.RemoveUnmatchedSidecars(f.RelPath); err != nil {
			x.log.Warn("cannot remove local metadata", "message", f.MessageID, "err", err)
		}
		return
	}
	id, ok, err := x.identify(ctx, p)
	if err != nil {
		x.log.Warn("cannot look up title", "message", f.MessageID, "err", err)
		return
	}
	if ok {
		if err := x.lib.WriteTMDBSidecar(f.RelPath, id); err != nil {
			x.log.Warn("cannot write TMDB id", "message", f.MessageID, "err", err)
		}
		return
	}
	if err := x.remove(ctx, f); err != nil {
		x.log.Warn("cannot drop unidentified file", "message", f.MessageID, "err", err)
		return
	}
	x.log.Info("removed unidentified", "message", f.MessageID, "title", p.Title)
}

func (x *Indexer) identify(ctx context.Context, p naming.Parsed) (int, bool, error) {
	if x.ident == nil || p.Episode || p.Title == "" {
		return 0, false, nil
	}
	x.mu.Lock()
	id, seen := x.known[p.Title]
	x.mu.Unlock()
	if seen {
		return id, id > 0, nil
	}
	tctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	id, ok, err := x.ident.Movie(tctx, p.Title)
	if err != nil {
		x.log.Warn("cannot look up title", "title", p.Title, "err", err)
		return 0, false, err // not cached, so the next pass tries again
	}
	if !ok {
		id = 0
	}
	x.mu.Lock()
	if x.known == nil {
		x.known = map[string]int{}
	}
	x.known[p.Title] = id
	x.mu.Unlock()
	return id, id > 0, nil
}

// Identify looks up movies already in the catalog without a year or id. A title that names one
// TMDB entry is linked, and the folder name stays so watch state stays. A title that does not
// is removed, so it never sits in the film list. It returns the files that gained a TMDB id.
func (x *Indexer) Identify(ctx context.Context) ([]media.File, error) {
	if x.personal {
		return nil, nil
	}
	all, err := x.cat.All(ctx)
	if err != nil {
		return nil, err
	}
	var changed []media.File
	for _, f := range all {
		p := naming.Parse(f.FileName, f.Caption)
		if strings.HasPrefix(f.RelPath, "Music/") || strings.HasPrefix(f.RelPath, "Home/") || p.Matchable() || p.Episode || x.lib.HasTMDBSidecar(f.RelPath) {
			continue
		}
		if x.ident != nil {
			id, ok, err := x.identify(ctx, p)
			if err != nil {
				continue
			}
			if ok {
				if err := x.lib.WriteTMDBSidecar(f.RelPath, id); err != nil {
					return changed, err
				}
				x.log.Info("title identified", "message", f.MessageID, "title", p.Title, "tmdb", id)
				changed = append(changed, f)
				continue
			}
		}
		if err := x.remove(ctx, f); err != nil {
			return changed, err
		}
		x.log.Info("removed unidentified", "message", f.MessageID, "title", p.Title, "file", f.FileName)
	}
	return changed, nil
}

func (x *Indexer) remove(ctx context.Context, f media.File) error {
	if err := x.lib.Remove(f.RelPath); err != nil {
		return err
	}
	if err := x.cat.Delete(ctx, f.MessageID); err != nil {
		return err
	}
	return x.blocks.RemoveDoc(f.DocID)
}

func (x *Indexer) lookup(ctx context.Context, ids []int) ([]media.Message, error) {
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return x.src.Messages(cctx, ids)
}

func idRange(start, n int) []int {
	ids := make([]int, n)
	for i := range ids {
		ids[i] = start + i
	}
	return ids
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
