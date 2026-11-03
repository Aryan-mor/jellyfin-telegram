// Package subtitles decides how a video gets subtitles in the user's language without Jellyfin
// extracting them, because extraction makes ffmpeg read the whole file from Telegram.
//
// Subtitles are treated as metadata: the tracks of a file are identified from its first bytes,
// and online subtitles are fetched only when the file has nothing usable.
package subtitles

import (
	"context"
	"regexp"
	"strings"

	"github.com/Aryan-mor/jellyfin-telegram/internal/lang"
)

// Outcome values stored in the catalog. A file with any of them is not checked again.
const (
	OutcomeEmbedded = "embedded"  // the file has a subtitle track in the language
	OutcomeHardSub  = "hardsub"   // subtitles are burned into the picture
	OutcomeAudio    = "audio"     // the file has audio in the language
	OutcomeTitle    = "title"     // the title is written in the script of the language
	OutcomeOnline   = "online"    // an online subtitle was added
	OutcomeNotFound = "not-found" // nothing suitable exists online
	OutcomeDisabled = "disabled"  // online lookups are off or do not cover the language
	OutcomeHollow   = "hollow"    // a nearly empty track in the language was replaced
	OutcomeChannel  = "channel"   // a subtitle file from the channel was added
)

var (
	reHardSub = regexp.MustCompile(`(?i)hard[\s._-]*sub|\bhdts\b|\bhdcam\b`)
	// These words name a Persian burn-in specifically, so they say nothing to anyone else.
	rePersianHardSub = regexp.MustCompile(`هارد[\s_\x{200c}-]*ساب|چسبیده`)
)

// LooksHardSub reads the release tokens of a file name or caption. Without one, a file counts as
// not hardsubbed, because most releases are not.
func LooksHardSub(text, code string) bool {
	text = strings.NewReplacer(".", " ", "_", " ").Replace(text)
	return reHardSub.MatchString(text) || (code == "fa" && rePersianHardSub.MatchString(text))
}

// Decision is what to do about one video.
type Decision struct {
	Outcome string
	Fetch   bool
}

// Input is what a decision is based on.
type Input struct {
	Language string
	Tracks   []Track
	Sniffed  bool
	FileName string
	Caption  string
	Title    string
	Online   bool
}

// Decide applies the rule in order. Anything already readable or audible in the language stops
// the search, so a second subtitle is never stacked on top of it.
func Decide(in Input) Decision {
	switch {
	case HasTrack(in.Tracks, "subtitle", in.Language) || in.Sniffed:
		return Decision{Outcome: OutcomeEmbedded}
	case LooksHardSub(in.FileName+" "+in.Caption, in.Language):
		return Decision{Outcome: OutcomeHardSub}
	case HasTrack(in.Tracks, "audio", in.Language):
		return Decision{Outcome: OutcomeAudio}
	case lang.TitleInScript(in.Language, in.Title):
		return Decision{Outcome: OutcomeTitle}
	case !in.Online:
		return Decision{Outcome: OutcomeDisabled}
	}
	return Decision{Fetch: true}
}

// Meta is what the media server knows about a video.
type Meta struct {
	MessageID int
	IMDb      string
	Title     string
	Year      int
	Season    int
	Episode   int
	FileName  string
	Caption   string
	URL       string
	// BeginPin marks reads as worth keeping until the returned function is called. Only the
	// header is pinned, because Jellyfin reads it again. A language sample is used once.
	BeginPin func() (end func())
}

// Result is the outcome for one video. Text is set only for OutcomeOnline.
type Result struct {
	Outcome string
	Text    string
	Format  string // "srt" or "ass"
}

// LocalSource finds subtitle files the user already has.
type LocalSource interface {
	Lookup(ctx context.Context, m Meta) (*SubFile, error)
}

// Resolver runs the whole decision for one video.
type Resolver struct {
	Tools    Tools
	Language string      // normalized code of the user's language
	Provider Provider    // nil turns online lookups off
	Local    LocalSource // nil skips subtitle files in the channel
}

// FindReplacement looks for a subtitle outside the video file: first the user's own files, then
// online. It returns nil when nothing fits.
func (r *Resolver) FindReplacement(ctx context.Context, m Meta, seconds int) (*SubFile, error) {
	file, _, err := r.find(ctx, m, seconds)
	return file, err
}

func (r *Resolver) find(ctx context.Context, m Meta, seconds int) (*SubFile, string, error) {
	if r.Local != nil {
		file, err := r.Local.Lookup(ctx, m)
		if err != nil {
			return nil, "", err
		}
		if file != nil && fitsRuntime(*file, seconds) {
			return file, OutcomeChannel, nil
		}
	}
	if r.Provider == nil {
		return nil, "", nil
	}
	file, err := Find(ctx, r.Provider, Query{
		IMDb: m.IMDb, Title: m.Title, Year: m.Year, Season: m.Season, Episode: m.Episode, FileName: m.FileName,
		Seconds: seconds,
	})
	return file, OutcomeOnline, err
}

// Resolve inspects a stream and finds a subtitle if one is needed.
func (r *Resolver) Resolve(ctx context.Context, m Meta) (Result, error) {
	end := func() {}
	if m.BeginPin != nil {
		end = m.BeginPin()
	}
	info, err := r.Tools.Inspect(ctx, m.URL)
	end()
	if err != nil {
		return Result{}, err
	}
	sniffed := false
	if !HasTrack(info.Tracks, "subtitle", r.Language) {
		// A failed sniff only means the tag stays as it is.
		sniffed, _ = r.Tools.SniffLanguage(ctx, m.URL, r.Language, info.Tracks)
	}
	d := Decide(Input{
		Language: r.Language, Tracks: info.Tracks, Sniffed: sniffed,
		FileName: m.FileName, Caption: m.Caption, Title: m.Title, Online: r.Provider != nil || r.Local != nil,
	})
	if !d.Fetch {
		return Result{Outcome: d.Outcome}, nil
	}
	file, source, err := r.find(ctx, m, info.Seconds)
	if err != nil {
		return Result{}, err
	}
	if file == nil {
		return Result{Outcome: OutcomeNotFound}, nil
	}
	return Result{Outcome: source, Text: file.Text, Format: strings.TrimPrefix(file.Ext, ".")}, nil
}
