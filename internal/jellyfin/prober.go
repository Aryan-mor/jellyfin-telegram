package jellyfin

import (
	"context"
	"log/slog"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
	"github.com/Aryan-mor/jellyfin-telegram/internal/naming"
	"github.com/Aryan-mor/jellyfin-telegram/internal/subtitles"
)

// ProbeWindow marks a message as being probed, so the gateway pins what is read. The
// returned function reports how many blocks were read during the window.
type ProbeWindow interface {
	BeginProbe(messageID int) (end func() int)
}

// SubtitleResolver decides what a video needs. A nil resolver skips subtitles.
type SubtitleResolver interface {
	Resolve(ctx context.Context, m subtitles.Meta) (subtitles.Result, error)
}

// Prober prepares new files in Jellyfin without any player attached: it adds a subtitle if one
// is needed, then makes Jellyfin read the media details.
//
// The order matters. Jellyfin caches the listing of a folder the first time PlaybackInfo reads
// it and never refreshes that cache, so a subtitle added after the first PlaybackInfo stays
// invisible until Jellyfin restarts.
type Prober struct {
	client    *Client
	cat       *catalog.Catalog
	window    ProbeWindow
	log       *slog.Logger
	ScanWait  time.Duration
	Poll      time.Duration
	Subtitles SubtitleResolver
	// SubtitleLanguage is the three-letter code uploaded subtitles are labelled with.
	SubtitleLanguage string
	StreamURL        func(messageID int) string
}

// Replacements returns the resolver when one is set, for finding replacement subtitles.
func (p *Prober) Replacements() *subtitles.Resolver {
	r, _ := p.Subtitles.(*subtitles.Resolver)
	return r
}

// NewProber returns a Prober.
func NewProber(c *Client, cat *catalog.Catalog, w ProbeWindow, log *slog.Logger) *Prober {
	return &Prober{client: c, cat: cat, window: w, log: log, ScanWait: 3 * time.Minute, Poll: 5 * time.Second}
}

func (p *Prober) pending(ctx context.Context) ([]media.File, error) {
	if p.Subtitles == nil {
		return p.cat.Unprobed(ctx)
	}
	return p.cat.Pending(ctx)
}

// Run handles every file with work left. With refresh set it first starts a library scan and
// waits for the new items to appear.
func (p *Prober) Run(ctx context.Context, refresh bool) error {
	pending, err := p.pending(ctx)
	if err != nil || len(pending) == 0 {
		return err
	}
	userID, err := p.client.AdminUserID(ctx)
	if err != nil {
		return err
	}
	if refresh {
		if err := p.client.RefreshLibrary(ctx); err != nil {
			return err
		}
	}

	deadline := time.Now().Add(p.ScanWait)
	done := map[int]bool{}
	for {
		items, err := p.client.Items(ctx, userID)
		if err != nil {
			return err
		}
		index := indexSources(items)
		for i := range pending {
			f := &pending[i]
			if done[f.MessageID] {
				continue
			}
			match, ok := index.find(*f)
			if !ok {
				continue
			}
			if p.handle(ctx, f, match, userID) {
				done[f.MessageID] = true
			}
		}
		if len(done) == len(pending) {
			return nil
		}
		if !refresh || time.Now().After(deadline) {
			p.log.Warn("files are missing from Jellyfin, check that its libraries point at the library folder",
				"missing", len(pending)-len(done))
			return nil
		}
		select {
		case <-time.After(p.Poll):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// handle reports whether no work is left for this file in this run.
func (p *Prober) handle(ctx context.Context, f *media.File, m match, userID string) bool {
	// Only a path match proves this is the current item. An old item may share the stream URL.
	if p.Subtitles != nil && f.SubtitleOutcome == "" && (m.byPath || !f.ProbedAt.IsZero()) {
		p.resolveSubtitle(ctx, f, m, userID)
	}
	if !f.ProbedAt.IsZero() {
		return true
	}
	return p.probeOne(ctx, *f, m.sourceID, userID)
}

// resolveSubtitle never blocks a probe: a failure leaves the outcome empty for the next run.
func (p *Prober) resolveSubtitle(ctx context.Context, f *media.File, m match, userID string) {
	meta := p.meta(ctx, *f, m.item, userID)
	meta.BeginPin = func() func() {
		end := p.window.BeginProbe(f.MessageID)
		return func() { end() }
	}
	res, err := p.Subtitles.Resolve(ctx, meta)
	if err != nil {
		p.log.Warn("subtitle check failed", "message", f.MessageID, "err", err)
		return
	}
	if res.Text != "" {
		if err := p.client.UploadSubtitle(ctx, m.sourceID, p.SubtitleLanguage, res.Format, res.Text); err != nil {
			p.log.Warn("subtitle upload failed", "message", f.MessageID, "err", err)
			return
		}
	}
	if err := p.cat.SetSubtitleOutcome(ctx, f.MessageID, res.Outcome); err != nil {
		p.log.Warn("cannot record subtitle outcome", "message", f.MessageID, "err", err)
		return
	}
	f.SubtitleOutcome = res.Outcome
	p.log.Info("subtitle checked", "message", f.MessageID, "outcome", res.Outcome)
}

// Meta describes a file for subtitle lookups.
func (p *Prober) Meta(ctx context.Context, f media.File, it Item, userID string) subtitles.Meta {
	return p.meta(ctx, f, it, userID)
}

// Located ties a file to its place in Jellyfin.
type Located struct {
	SourceID string
	Item     Item
}

// LibraryIndex finds files among the items Jellyfin knows.
type LibraryIndex struct{ sources sourceIndex }

// Index reads the current library.
func (p *Prober) Index(ctx context.Context, userID string) (LibraryIndex, error) {
	items, err := p.client.Items(ctx, userID)
	if err != nil {
		return LibraryIndex{}, err
	}
	return LibraryIndex{indexSources(items)}, nil
}

// Locate returns the place of a file. Only a path match counts, because an old item can share
// the stream URL.
func (l LibraryIndex) Locate(f media.File) (Located, bool) {
	m, ok := l.sources.find(f)
	if !ok || !m.byPath {
		return Located{}, false
	}
	return Located{SourceID: m.sourceID, Item: m.item}, true
}

func (p *Prober) meta(ctx context.Context, f media.File, it Item, userID string) subtitles.Meta {
	m := subtitles.Meta{
		MessageID: f.MessageID,
		IMDb:      it.IMDb(), Title: it.Name, Year: it.Year,
		FileName: f.FileName, Caption: f.Caption,
	}
	if p.StreamURL != nil {
		m.URL = p.StreamURL(f.MessageID)
	}
	if it.Type == "Episode" {
		m.Title, m.Season, m.Episode, m.IMDb = it.SeriesName, it.Season, it.Episode, ""
		if it.SeriesID != "" {
			if series, err := p.client.Item(ctx, it.SeriesID, userID); err == nil {
				m.IMDb, m.Year = series.IMDb(), series.Year
			}
		}
	}
	// Jellyfin translates titles into the metadata language, so its title says nothing about the
	// language of the film and may not match anything online. The name in the channel does both.
	if parsed := naming.Parse(f.FileName, f.Caption); parsed.Title != "" {
		m.Title = parsed.Title
		if parsed.Year > 0 {
			m.Year = parsed.Year
		}
	}
	return m
}

// probeOne reports whether the file was really probed. Jellyfin can answer from an old
// item that has the same stream URL but no file behind it any more. That answer looks
// complete while nothing reads the video, so a probe only counts if blocks were read.
func (p *Prober) probeOne(ctx context.Context, f media.File, itemID, userID string) bool {
	end := p.window.BeginProbe(f.MessageID)
	res, err := p.client.Probe(ctx, itemID, userID)
	blocks := end()
	if err != nil {
		p.log.Warn("probe failed", "message", f.MessageID, "err", err)
		return false
	}
	if blocks == 0 {
		p.log.Debug("probe read no data, waiting for the library scan", "message", f.MessageID)
		return false
	}
	if res.Streams == 0 {
		p.log.Warn("probe found no streams", "message", f.MessageID)
		// The file was opened and has nothing to play. Leave it unprobed for a later sync,
		// but do not sit in this run asking again every few seconds.
		return blocks > 0
	}
	if err := p.cat.MarkProbed(ctx, f.MessageID, time.Now()); err != nil {
		p.log.Warn("cannot record probe", "message", f.MessageID, "err", err)
		return false
	}
	minutes := res.RunTimeTicks / 600_000_000
	if err := p.cat.SetDuration(ctx, f.MessageID, int(res.RunTimeTicks/10_000_000)); err != nil {
		p.log.Warn("cannot record duration", "message", f.MessageID, "err", err)
	}
	p.log.Info("file probed", "message", f.MessageID, "streams", res.Streams, "minutes", minutes, "blocks", blocks)
	return true
}

// match ties a playable source to the library item that holds it.
type match struct {
	sourceID string
	item     Item
	byPath   bool
}

type indexedSource struct {
	Source
	item Item
}

type sourceIndex []indexedSource

func indexSources(items []Item) sourceIndex {
	var out sourceIndex
	for _, it := range items {
		if len(it.Sources) == 0 {
			out = append(out, indexedSource{Source{ID: it.ID, Path: it.Path}, it})
			continue
		}
		for _, s := range it.Sources {
			out = append(out, indexedSource{s, it})
		}
	}
	return out
}

// find matches a file by its .strm path, then by its stream URL. A probed source shows the URL
// instead of the path, but its item still holds the path, so that is checked too. The path is
// tried first because an old item can still hold the same URL. Library paths differ between
// Jellyfin and this service under Docker, so only the part below the library root is compared.
func (s sourceIndex) find(f media.File) (match, bool) {
	suffix := "/" + f.RelPath
	hasPath := func(p string) bool { return strings.HasSuffix(strings.ReplaceAll(p, `\`, "/"), suffix) }
	for _, src := range s {
		if hasPath(src.Path) || (src.ID == src.item.ID && hasPath(src.item.Path)) {
			return match{sourceID: src.ID, item: src.item, byPath: true}, true
		}
	}
	urlSuffix := "/" + strconv.FormatInt(f.ChannelID, 10) + "/" + strconv.Itoa(f.MessageID)
	for _, src := range s {
		if strings.HasSuffix(src.Path, urlSuffix) {
			// Another version of an episode or film is a source of the same item, and once probed it
			// shows its URL instead of its path. It counts as a path match when the item is in the
			// file's own folder. An old item that only shares the URL lies somewhere else.
			return match{sourceID: src.ID, item: src.item, byPath: sameFolder(src.item.Path, f.RelPath)}, true
		}
	}
	return match{}, false
}

// sameFolder reports that an item path from Jellyfin lies in the folder of a library-relative path.
// Library roots differ between Jellyfin and this service, so only the part below the root is compared.
func sameFolder(itemPath, rel string) bool {
	if itemPath == "" || rel == "" {
		return false
	}
	dir := path.Dir(rel)
	if dir == "." {
		return false
	}
	return strings.HasSuffix(path.Dir(strings.ReplaceAll(itemPath, `\`, "/")), "/"+dir)
}

// RefreshMetadata makes Jellyfin read these files again. It is for a title whose folder gained a
// TMDB id: Jellyfin keeps an earlier local lock until the item is refreshed with replacing on.
func (p *Prober) RefreshMetadata(ctx context.Context, files []media.File) error {
	if len(files) == 0 {
		return nil
	}
	userID, err := p.client.AdminUserID(ctx)
	if err != nil {
		return err
	}
	idx, err := p.Index(ctx, userID)
	if err != nil {
		return err
	}
	for _, f := range files {
		at, ok := idx.Locate(f)
		if !ok {
			continue // not scanned yet, so the first read will already use the id
		}
		if err := p.client.RefreshMetadata(ctx, at.Item.ID, userID); err != nil {
			p.log.Warn("cannot refresh metadata", "message", f.MessageID, "err", err)
		}
	}
	return nil
}
