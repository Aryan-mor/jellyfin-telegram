// Package intake turns the raw files that people post in the library channel into signed posts.
//
// The state of a file is written in its own caption, on the first line, so anyone who opens the
// channel sees it and every installation can read it. A bot that takes a file for review edits
// that line and signs a claim under it. Nothing else is posted.
//
//	raw file      review -> indexed (its signed post exists) or rejected
//	signed post   subtitles -> ready
//
// Mirrors copy only posts that are ready.
package intake

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/fileid"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
	"github.com/Aryan-mor/jellyfin-telegram/internal/naming"
	"github.com/Aryan-mor/jellyfin-telegram/internal/sign"
)

const (
	batchSize = 100
	cursorKey = "forum.cursor"
	// maxTries is how many failed passes one file gets before it is rejected with the error.
	// A pass runs every minute.
	maxTries = 10
	// maxPerPass is how many files one pass takes up. A pass runs every minute, which keeps the
	// pace of edits and reposts well under Telegram's limits.
	maxPerPass = 5
	// hintLookback is how far behind the cursor a TMDB reply is still read. A reply is not a
	// file, so the pass that first saw it moved on, and later passes have to look back for it.
	hintLookback = batchSize
	// reparseKey is set once the rejected files have been given one more pass after the
	// parser learned to read E009 and a Persian caption. reparseAtKey is how far that walk got.
	reparseKey   = "inbox.reparse_episode"
	reparseAtKey = "inbox.reparse_episode_at"
	// claimFor is how long a claim is held before another installation may take the file.
	claimFor = 2 * time.Hour
)

// transient marks an error that says nothing about the file: Telegram or Jellyfin did not answer.
// It never counts towards maxTries.
type transient struct{ err error }

func (t transient) Error() string { return t.err.Error() }
func (t transient) Unwrap() error { return t.err }

// Telegram is the bot pool, narrowed to what the pipeline posts and reads.
type Telegram interface {
	Messages(ctx context.Context, ids []int) ([]media.Message, error)
	ReadyBot() (int64, error)
	Repost(ctx context.Context, botID int64, sourceID int, caption string) (int, error)
	EditCaption(ctx context.Context, botID int64, messageID int, caption string) error
	DeleteMessages(ctx context.Context, ids []int) error
}

// Probe is what the head of a file reveals.
type Probe struct {
	Tracks  []sign.Track
	Seconds int
	// Audio is every audio stream of the file.
	Audio []naming.AudioIn
}

// Service runs the inbox pipeline until ctx ends.
type Service struct {
	Cat   *catalog.Catalog
	TG    Telegram
	Key   *sign.Key
	Trust *sign.Trust
	Check *sign.Checker
	// Bots are this installation's bot user ids.
	Bots []int64
	// Identify fills a TMDB id when the file name has none. episode selects a series search.
	// Nil leaves that file waiting.
	Identify func(ctx context.Context, title string, episode bool) (tmdb int, imdb string, ok bool, err error)
	// Lang is the preferred subtitle language, shown first in the readable caption.
	Lang string
	// OriginalLanguage returns the language a film or series was made in, such as "en". It tells
	// the original audio track from a dub. Nil, or an empty answer, leaves that to tags in the names.
	OriginalLanguage func(ctx context.Context, tmdb int, episode bool) (string, error)
	// Inspect reads the running time and the soft subtitle tracks from the head of the file. Nil
	// records none.
	Inspect func(ctx context.Context, messageID int) (Probe, error)
	// Fingerprint reads the first and last blocks of the file for its content hashes. Nil leaves
	// them out of the line.
	Fingerprint func(ctx context.Context, f media.File) (fileid.Sample, error)
	// Subtitles is true when this installation prepares subtitles. A post then starts as
	// "subtitles" and becomes "ready" when that is done. Otherwise it is ready at once.
	Subtitles bool
	// DeleteOriginals is the fallback when the catalog has no switch yet.
	DeleteOriginals bool
	// Window is how many empty ids end a walk. Zero uses one batch, which is what the indexer's
	// routine check uses too.
	Window int
	Now    func() time.Time
	Log    *slog.Logger

	mu    sync.Mutex
	tries map[int]int
}

// Run walks the inbox until ctx ends.
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		if err := s.Pass(ctx); err != nil && ctx.Err() == nil {
			s.log().Warn("intake", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Service) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Pass reads new messages, then takes up the files that are still open.
func (s *Service) Pass(ctx context.Context) error {
	if err := s.scan(ctx); err != nil {
		return err
	}
	if err := s.caughtReplies(ctx); err != nil {
		return err
	}
	if err := s.reconsider(ctx); err != nil {
		return err
	}
	open, err := s.Cat.OpenInbox(ctx)
	if err != nil {
		return err
	}
	worked := 0
	for _, id := range open {
		if worked >= maxPerPass {
			break
		}
		did, err := s.one(ctx, id)
		if did {
			worked++
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// One file that cannot be handled must not hold up the ones behind it.
			s.failed(ctx, id, err)
		}
	}
	return nil
}

// failed counts a failed try. After maxTries the file is rejected with the error, so it stops
// coming first in the queue.
func (s *Service) failed(ctx context.Context, id int, err error) {
	var soft transient
	if errors.As(err, &soft) {
		s.log().Warn("inbox file waits", "message", id, "err", err)
		return
	}
	s.mu.Lock()
	if s.tries == nil {
		s.tries = map[int]int{}
	}
	s.tries[id]++
	n := s.tries[id]
	s.mu.Unlock()
	s.log().Warn("inbox file failed", "message", id, "try", n, "err", err)
	if n < maxTries {
		return
	}
	msgs, merr := s.TG.Messages(ctx, []int{id})
	if merr != nil || len(msgs) == 0 || msgs[0].File == nil {
		return
	}
	if rerr := s.reject(ctx, msgs[0].File, fmt.Sprintf("failed %d times: %v", n, err)); rerr != nil {
		s.log().Warn("cannot reject a failed file", "message", id, "err", rerr)
		return
	}
	s.mu.Lock()
	delete(s.tries, id)
	s.mu.Unlock()
}

func (s *Service) scan(ctx context.Context) error {
	raw, err := s.Cat.StateValue(ctx, cursorKey)
	if err != nil {
		return err
	}
	cursor, _ := strconv.Atoi(raw)
	window := s.Window
	if window <= 0 {
		window = batchSize
	}
	empty := 0
	high := cursor
	for next := cursor + 1; empty < window; next += batchSize {
		msgs, err := s.TG.Messages(ctx, idRange(next, batchSize))
		if err != nil {
			return err
		}
		seen := false
		for _, m := range msgs {
			if !m.Exists {
				continue
			}
			seen = true
			if m.ID > high {
				high = m.ID
			}
			if err := s.observe(ctx, m); err != nil {
				return err
			}
		}
		if seen {
			empty = 0
		} else {
			empty += batchSize
		}
	}
	if high == cursor {
		return nil
	}
	return s.Cat.SetStateValue(ctx, cursorKey, strconv.Itoa(high))
}

// isRaw is a video that a person posted and that no bot has finished with: it has no library line
// and its status line is not "indexed" or "rejected".
func isRaw(f *media.File) bool {
	if f == nil {
		return false
	}
	if _, signed := sign.ParseCaption(f.Caption); signed {
		return false
	}
	switch h, _ := sign.SplitHeader(f.Caption); h.Status {
	case sign.StatusIndexed, sign.StatusRejected:
		return false
	}
	return naming.Section(f.FileName, f.Caption, f.MIME) != naming.SectionMusic
}

func (s *Service) observe(ctx context.Context, m media.Message) error {
	if m.File == nil {
		return s.hint(ctx, m)
	}
	if _, signed := sign.ParseCaption(m.File.Caption); signed {
		// Only a verified post closes an inbox file, so a stranger cannot make files vanish from the queue.
		ok, err := s.allow(ctx, *m.File)
		if err != nil {
			s.log().Warn("cannot check a signed post", "message", m.ID, "err", err)
			return nil
		}
		if !ok {
			return nil
		}
		closed, err := s.Cat.CloseInboxDoc(ctx, m.File.DocID)
		for _, id := range closed {
			s.markSource(ctx, id, sign.StatusIndexed, "")
		}
		return err
	}
	if !isRaw(m.File) {
		return nil
	}
	state, _, err := s.Cat.InboxState(ctx, m.ID)
	if err != nil {
		return err
	}
	if state != "" {
		return nil
	}
	return s.Cat.SetInbox(ctx, m.ID, "open", 0, m.File.DocID)
}

func (s *Service) allow(ctx context.Context, f media.File) (bool, error) {
	if s.Check == nil {
		return true, nil
	}
	return s.Check.Allow(ctx, f)
}

// one takes up an open file. did is true when it changed something in the channel.
func (s *Service) one(ctx context.Context, id int) (did bool, err error) {
	msgs, err := s.TG.Messages(ctx, []int{id})
	if err != nil {
		return false, transient{err}
	}
	if len(msgs) == 0 || !msgs[0].Exists || msgs[0].File == nil {
		s.log().Info("inbox file is gone", "message", id)
		return false, s.Cat.SetInbox(ctx, id, "gone", 0, 0)
	}
	file := msgs[0].File
	if !isRaw(file) {
		// Another installation finished it, or it is a signed post now.
		h, _ := sign.SplitHeader(file.Caption)
		state := "ready"
		if h.Status == sign.StatusRejected {
			state = "rejected"
		}
		return false, s.Cat.SetInbox(ctx, id, state, 0, file.DocID)
	}
	// Without a title lookup only a file that already names its TMDB id can go on. The others wait
	// instead of being rejected for something that is not their fault.
	if s.Identify == nil && naming.Parse(file.FileName, file.Caption).TMDBID == 0 {
		return false, nil
	}
	// A document that is already in the library needs no claim and no work.
	if known, err := s.Cat.DocPublished(ctx, file.DocID); err != nil {
		return false, err
	} else if known {
		s.log().Info("inbox file already in the library", "message", id)
		s.markSource(ctx, id, sign.StatusIndexed, "")
		return true, s.finishOriginal(ctx, id, 0, "duplicate")
	}
	won, claimed, err := s.take(ctx, file, "review")
	if err != nil || !won {
		return claimed, err
	}
	meta, ok, err := s.describe(ctx, file)
	if err != nil {
		return true, err
	}
	if !ok {
		return true, s.reject(ctx, file, "unidentified")
	}
	bot, err := s.TG.ReadyBot()
	if err != nil {
		return true, transient{err}
	}
	meta.DocID = file.DocID
	meta.Size = file.Size
	meta.Subs = "no"
	status := sign.StatusReady
	if s.Subtitles {
		status = sign.StatusSubtitles
	}
	meta.Status = status
	readyID, err := s.TG.Repost(ctx, bot, id, s.caption(ctx, file, meta))
	if err != nil {
		return true, err
	}
	s.log().Info("published", "inbox", id, "ready", readyID, "tmdb", meta.TMDB, "status", status)
	s.markSource(ctx, id, sign.StatusIndexed, "")
	return true, s.finishOriginal(ctx, id, readyID, "ready")
}

// reject marks the raw file and stops.
func (s *Service) reject(ctx context.Context, file *media.File, why string) error {
	s.log().Info("rejected", "message", file.MessageID, "why", why)
	s.markSource(ctx, file.MessageID, sign.StatusRejected, why)
	return s.Cat.SetInbox(ctx, file.MessageID, "rejected", 0, file.DocID)
}

// markSource writes a final status on a raw file, above what the person wrote. A file whose
// caption cannot be edited keeps the state in the catalog only.
func (s *Service) markSource(ctx context.Context, id int, status, why string) {
	msgs, err := s.TG.Messages(ctx, []int{id})
	if err != nil || len(msgs) == 0 || msgs[0].File == nil {
		return
	}
	if _, signed := sign.ParseCaption(msgs[0].File.Caption); signed {
		return
	}
	caption := sign.WithHeader(status, why, "", sign.StripHeader(msgs[0].File.Caption))
	if err := s.write(ctx, id, caption); err != nil {
		s.log().Warn("cannot write the status on a file", "message", id, "status", status, "err", err)
	}
}

func (s *Service) finishOriginal(ctx context.Context, id, readyID int, state string) error {
	if s.deleteOriginals(ctx) {
		if err := s.TG.DeleteMessages(ctx, []int{id}); err != nil {
			s.log().Warn("cannot delete the inbox original", "message", id, "err", err)
		}
	}
	return s.Cat.SetInbox(ctx, id, state, readyID, 0)
}

func (s *Service) deleteOriginals(ctx context.Context) bool {
	raw, err := s.Cat.StateValue(ctx, "inbox.delete_originals")
	if err != nil || raw == "" {
		return s.DeleteOriginals
	}
	return raw == "true" || raw == "1"
}

// describe builds the signed fields. ok is false when no TMDB id can be found.
func (s *Service) describe(ctx context.Context, file *media.File) (sign.Meta, bool, error) {
	p := naming.Parse(file.FileName, file.Caption)
	imdb := ""
	if p.TMDBID == 0 && p.Title != "" && s.Identify != nil {
		id, got, ok, err := s.Identify(ctx, p.Title, p.Episode)
		if err != nil {
			return sign.Meta{}, false, transient{err}
		}
		if ok {
			p.TMDBID = id
			imdb = got
		}
	}
	if p.TMDBID <= 0 {
		return sign.Meta{}, false, nil
	}
	var soft []sign.Track
	var audioIn []naming.AudioIn
	seconds := 0
	if s.Inspect != nil {
		probe, err := s.Inspect(ctx, file.MessageID)
		if err != nil {
			s.log().Warn("cannot read subtitle tracks", "message", file.MessageID, "err", err)
		} else {
			soft, seconds, audioIn = probe.Tracks, probe.Seconds, probe.Audio
		}
	}
	audio := s.audioOf(ctx, audioIn, p, file)
	hash, osdb := "", ""
	if s.Fingerprint != nil {
		sample, err := s.Fingerprint(ctx, *file)
		if err != nil {
			return sign.Meta{}, false, transient{err}
		}
		hash, osdb = sample.Hash(), sample.OSDb()
	}
	kind := "m"
	if p.Episode {
		kind = "e"
	}
	return sign.Meta{
		Kind: kind, TMDB: p.TMDBID, IMDb: imdb, Quality: p.Label,
		Season: p.Season, Episode: p.Number, Soft: soft, Audio: audio, Duration: seconds,
		Hash: hash, OSDb: osdb,
		Hard: naming.HardSub(file.FileName, file.Caption),
	}, true, nil
}

// audioOf names the audio tracks and says which are dubs. Not knowing the original language is not
// an error: the roles then come from tags in the names, or stay empty.
func (s *Service) audioOf(ctx context.Context, in []naming.AudioIn, p naming.Parsed, file *media.File) []sign.AudioTrack {
	if len(in) == 0 {
		return nil
	}
	original := ""
	if s.OriginalLanguage != nil {
		got, err := s.OriginalLanguage(ctx, p.TMDBID, p.Episode)
		if err != nil {
			s.log().Debug("cannot read the original language", "tmdb", p.TMDBID, "err", err)
		}
		original = got
	}
	return naming.AudioTracks(in, original, file.FileName+" "+sign.StripHeader(file.Caption))
}

// caption is the whole post: the status line, the readable head, the signed machine line, and the
// hashtag last. The line is fitted so that all of it stays inside Telegram's caption length.
func (s *Service) caption(ctx context.Context, file *media.File, meta sign.Meta) string {
	h := s.headOf(file, meta)
	if h.Source == "" {
		h.Source = s.originLine(ctx, file.DocID)
	}
	return s.compose(meta.Status, naming.Caption(h), h.LookupTag(), "", meta)
}

// originLine is the source line for a document copied from another channel, or empty when this
// file was not copied.
func (s *Service) originLine(ctx context.Context, docID int64) string {
	if docID == 0 || s.Cat == nil {
		return ""
	}
	src, ok, err := s.Cat.CopyOrigin(ctx, docID)
	if err != nil {
		s.log().Warn("cannot read the source of a file", "doc", docID, "err", err)
		return ""
	}
	if !ok {
		return ""
	}
	return naming.SourceLine(src.Title, src.Username)
}

// compose lays out a library post. claimLine is empty unless somebody is working on it.
func (s *Service) compose(status, head, tag, claimLine string, meta sign.Meta) string {
	top := sign.WithHeader(status, "", claimLine, head)
	reserve := top
	if tag != "" {
		reserve += "\n" + tag
	}
	line := s.Key.Post(reserve, meta)
	out := top + "\n" + line
	if tag != "" {
		out += "\n" + tag
	}
	return out
}

func (s *Service) headOf(file *media.File, m sign.Meta) naming.Head {
	p := naming.Parse(file.FileName, file.Caption)
	audio, sub := naming.TrackLangs(m.Audio, m.Soft, s.Lang)
	return naming.Head{
		Title: p.Title, Year: p.Year, Episode: m.Kind == "e",
		Season: m.Season, Number: m.Episode, TMDB: m.TMDB, IMDb: m.IMDb,
		Label: m.Quality, Soft: naming.ShowTracks(m.Soft, s.Lang), Audio: naming.ShowAudio(m.Audio), Hard: m.Hard,
		Source: naming.SourceIn(file.Caption), AudioLang: audio, SubLang: sub,
	}
}

// claimOn reads who holds a message for this operation: mine, other, or none of them.
func (s *Service) claimOn(f *media.File, op string) (mine, other bool) {
	h, _ := sign.SplitHeader(f.Caption)
	if h.Claim == nil {
		return false, false
	}
	edited := f.Edited
	if edited.IsZero() {
		edited = f.Date
	}
	if !h.Claim.Live(op, f.MessageID, edited, s.now()) {
		return false, false
	}
	if h.Claim.KeyID() == s.Key.KeyID() {
		return true, false
	}
	return false, true
}

// take claims a message by editing its caption, and reports whether this installation holds the
// claim. claimed is true when this call wrote one. A claim written now does not win yet: the next
// pass reads the caption again, and whichever claim is still there is the one that counts. Two
// installations that edit at the same moment therefore agree, because the caption holds one.
func (s *Service) take(ctx context.Context, f *media.File, op string) (won, claimed bool, err error) {
	mine, other := s.claimOn(f, op)
	if other {
		return false, false, nil
	}
	if mine {
		return true, false, nil
	}
	status := sign.StatusReview
	if op == "subs" {
		status = sign.StatusSubtitles
	}
	line := s.Key.SignClaim(op, f.MessageID, int(claimFor/time.Second))
	caption := sign.WithHeader(status, "", line, sign.StripHeader(f.Caption))
	if err := s.write(ctx, f.MessageID, caption); err != nil {
		return false, false, err
	}
	return false, true, nil
}

// write sets a caption. A channel post does not say which bot sent it, so the bots are tried in
// turn. The catalog row, if there is one, follows.
func (s *Service) write(ctx context.Context, messageID int, caption string) error {
	var last error
	for _, bot := range s.Bots {
		if last = s.TG.EditCaption(ctx, bot, messageID, caption); last == nil {
			_ = s.Cat.SetCaption(ctx, messageID, caption)
			return nil
		}
	}
	if last == nil {
		last = errors.New("no bot is configured")
	}
	return fmt.Errorf("edit caption: %w", last)
}

// fresh reads a message again, because a row of the catalog can be an hour old.
func (s *Service) fresh(ctx context.Context, id int) (*media.File, error) {
	msgs, err := s.TG.Messages(ctx, []int{id})
	if err != nil {
		return nil, transient{err}
	}
	if len(msgs) == 0 || !msgs[0].Exists || msgs[0].File == nil {
		return nil, nil
	}
	return msgs[0].File, nil
}

// Claim reports whether this installation should prepare the subtitles of a signed post. It
// writes the claim in the post's caption, under the status line.
func (s *Service) Claim(ctx context.Context, f media.File) (bool, error) {
	cur, err := s.fresh(ctx, f.MessageID)
	if err != nil || cur == nil {
		return false, err
	}
	if h, _ := sign.SplitHeader(cur.Caption); h.Status == sign.StatusReady {
		return true, nil // finished already, nobody else needs the file
	}
	won, _, err := s.take(ctx, cur, "subs")
	return won, err
}

// Resign marks a signed post ready: the subtitles are done. The machine line is signed again with
// this installation's key, and the claim is dropped. A post another installation signed is
// finished the same way, because the subtitles are what this installation holds.
func (s *Service) Resign(ctx context.Context, f media.File, archiveHash string) error {
	return s.edit(ctx, f.MessageID, true, func(meta sign.Meta) (sign.Meta, bool) {
		subs := "no"
		if archiveHash != "" {
			subs = "yes"
		}
		if meta.Status == sign.StatusReady && meta.Subs == subs && meta.ArchiveHash == archiveHash {
			return meta, false
		}
		meta.Status = sign.StatusReady
		meta.Subs = subs
		meta.ArchiveHash = archiveHash
		return meta, true
	})
}

// ArchiveCaption is the caption of the subtitle archive posted for a signed post: the title, a
// signed line that names the video and the archive, and the same hashtag as the video, so one search
// finds both. It returns an empty string when the post cannot be read.
func (s *Service) ArchiveCaption(ctx context.Context, f media.File, archiveHash string, tracks []sign.Track) string {
	file, err := s.fresh(ctx, f.MessageID)
	if err != nil || file == nil {
		file = &f
	}
	_, body := sign.SplitHeader(file.Caption)
	meta, ok := sign.ParseCaption(body)
	if !ok {
		return ""
	}
	h := s.headOf(file, meta)
	title := h.Title
	if title == "" {
		title = "Untitled"
	}
	line := s.Key.SignSubs(sign.Subs{DocID: f.DocID, VideoHash: meta.Hash, ArchiveHash: archiveHash, Tracks: tracks})
	out := "\U0001F4AC Subtitles: " + title + "\n" + line
	if tag := h.LookupTag(); tag != "" {
		out += "\n" + tag
	}
	return out
}

// FillIMDb writes an IMDb id Jellyfin learned after probing, and signs the caption again.
func (s *Service) FillIMDb(ctx context.Context, f media.File, imdb string) error {
	if imdb == "" {
		return nil
	}
	return s.edit(ctx, f.MessageID, false, func(meta sign.Meta) (sign.Meta, bool) {
		// An episode's item carries its own IMDb id, and the signed id is the series'.
		if meta.IMDb != "" || meta.Kind == "e" {
			return meta, false
		}
		meta.IMDb = imdb
		return meta, true
	})
}

// edit re-signs the machine line of a signed post. Unless anyKey is set, a post that carries
// another installation's key is left alone.
func (s *Service) edit(ctx context.Context, messageID int, anyKey bool, change func(sign.Meta) (sign.Meta, bool)) error {
	file, err := s.fresh(ctx, messageID)
	if err != nil || file == nil {
		return err
	}
	h, body := sign.SplitHeader(file.Caption)
	meta, ok := sign.ParseCaption(body)
	if !ok || (!anyKey && meta.KeyID != s.Key.KeyID()) {
		return nil
	}
	next, do := change(meta)
	if !do {
		return nil
	}
	hd := s.headOf(file, next)
	status, claimLine := h.Status, h.ClaimLine
	if next.Status == sign.StatusReady {
		status, claimLine = sign.StatusReady, ""
	}
	room := sign.WithHeader(status, "", claimLine, naming.Caption(hd)) + "\n" + hd.LookupTag()
	line := s.Key.Post(room, next)
	return s.write(ctx, messageID, sign.WithHeader(status, "", claimLine, sign.ReplaceLine(body, line)))
}

// reconsider gives each rejected file one more look. A file the parser can now place as an
// episode is put back in the queue. One that still cannot be placed stays rejected.
func (s *Service) reconsider(ctx context.Context) error {
	done, err := s.Cat.StateValue(ctx, reparseKey)
	if err != nil || done == "1" {
		return err
	}
	raw, err := s.Cat.StateValue(ctx, reparseAtKey)
	if err != nil {
		return err
	}
	at, _ := strconv.Atoi(raw)
	ids, err := s.Cat.InboxAfter(ctx, "rejected", at, maxPerPass)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return s.Cat.SetStateValue(ctx, reparseKey, "1")
	}
	for _, id := range ids {
		if err := s.Cat.SetStateValue(ctx, reparseAtKey, strconv.Itoa(id)); err != nil {
			return err
		}
		if err := s.reopenEpisode(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// reopenEpisode clears a rejection when the file name or caption now says which episode it is.
func (s *Service) reopenEpisode(ctx context.Context, id int) error {
	msgs, err := s.TG.Messages(ctx, []int{id})
	if err != nil || len(msgs) == 0 || !msgs[0].Exists || msgs[0].File == nil {
		return err
	}
	file := msgs[0].File
	if h, _ := sign.SplitHeader(file.Caption); h.Status != sign.StatusRejected {
		return nil
	}
	p := naming.Parse(file.FileName, file.Caption)
	// A uuid with an episode number copied into the caption is still not a title.
	if !p.Episode || p.Title == "" || naming.WeakTitle(p.Title) {
		return nil
	}
	if err := s.write(ctx, id, sign.StripHeader(file.Caption)); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.tries, id)
	s.mu.Unlock()
	s.log().Info("reopened a rejected episode", "message", id, "title", p.Title, "season", p.Season, "episode", p.Number)
	return s.Cat.SetInbox(ctx, id, "open", 0, file.DocID)
}

// caughtReplies reads the messages just behind the cursor. A TMDB reply is not a video, so the
// pass that first walked over it left it there. Looking back is what makes that reply count.
func (s *Service) caughtReplies(ctx context.Context) error {
	raw, err := s.Cat.StateValue(ctx, cursorKey)
	if err != nil {
		return err
	}
	cursor, _ := strconv.Atoi(raw)
	if cursor < 1 {
		return nil
	}
	start := cursor - hintLookback + 1
	if start < 1 {
		start = 1
	}
	msgs, err := s.TG.Messages(ctx, idRange(start, cursor-start+1))
	if err != nil {
		return err
	}
	for _, m := range msgs {
		if !m.Exists || m.File != nil {
			continue
		}
		if err := s.hint(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

// hint reopens a rejected file when a reply names its TMDB film, or one episode of a series.
// Whoever can post in the library channel can do this, the same as putting a tmdbid in a file name.
func (s *Service) hint(ctx context.Context, m media.Message) error {
	if m.ReplyTo == 0 || !naming.HintReady(m.Text) {
		return nil
	}
	state, _, err := s.Cat.InboxState(ctx, m.ReplyTo)
	if err != nil {
		return err
	}
	// The file is already in the library, so the reply has done its job.
	if state == "ready" || state == "duplicate" {
		s.dropHint(ctx, m.ID)
		return nil
	}
	if state != "rejected" {
		return nil
	}
	msgs, err := s.TG.Messages(ctx, []int{m.ReplyTo})
	if err != nil {
		return err
	}
	if len(msgs) == 0 || !msgs[0].Exists || msgs[0].File == nil {
		return nil
	}
	file := msgs[0].File
	body := sign.StripHeader(file.Caption)
	line := strings.TrimSpace(m.Text)
	// The link is already on the file and it is still rejected, so applying it again would loop.
	if line == "" || strings.Contains(body, line) {
		return nil
	}
	if body != "" {
		body += "\n"
	}
	body += line
	if err := s.write(ctx, file.MessageID, body); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.tries, file.MessageID)
	s.mu.Unlock()
	s.log().Info("reopened from a tmdb reply", "message", file.MessageID, "reply", m.ID, "tmdb", naming.Parse("", line).TMDBID)
	return s.Cat.SetInbox(ctx, file.MessageID, "open", 0, file.DocID)
}

// dropHint removes a TMDB reply once the file it points at has been accepted.
func (s *Service) dropHint(ctx context.Context, id int) {
	if err := s.TG.DeleteMessages(ctx, []int{id}); err != nil {
		s.log().Warn("cannot delete a tmdb reply", "message", id, "err", err)
		return
	}
	s.log().Info("deleted tmdb reply", "message", id)
}

func idRange(start, n int) []int {
	ids := make([]int, n)
	for i := range ids {
		ids[i] = start + i
	}
	return ids
}
