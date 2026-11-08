// Package tgsource copies films and series from other Telegram channels into the library channel.
// A signed-in account reads each source channel and asks Telegram to forward the videos, so the
// bytes never pass through this computer. The indexer then finds them like any other post.
package tgsource

import (
	"encoding/json"
	"errors"
	"path"
	"strings"

	"github.com/Aryan-mor/jellyfin-telegram/internal/naming"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// Settings are the rules for what is copied and how fast. Zero in a limit means no limit.
type Settings struct {
	// Enabled is off until a person turns it on.
	Enabled bool `json:"enabled"`
	// LookBackDays is how far into each channel's past the first pass reaches. Zero means all of it.
	LookBackDays int `json:"look_back_days"`
	// EveryMinutes is how often the channels are checked for new posts.
	EveryMinutes int `json:"every_minutes"`
	// MinMinutes and MinMB leave out trailers, clips and samples.
	MinMinutes int `json:"min_minutes"`
	MinMB      int `json:"min_mb"`
	MaxGB      int `json:"max_gb"`
	// SkipWords and OnlyWords are comma separated and match the file name or caption, any case.
	SkipWords string `json:"skip_words"`
	OnlyWords string `json:"only_words"`
	// NeedTitle keeps only files whose name shows a year or a season and episode.
	NeedTitle bool `json:"need_title"`
	// MaxPerDay caps how many videos are copied per day across every channel.
	MaxPerDay int `json:"max_per_day"`
	// PauseSeconds is the longest rest between bursts of 20 to 50 videos. Each rest is a random
	// time between 10 seconds and this. Zero means no rest.
	PauseSeconds int `json:"pause_seconds"`
}

// Defaults are cautious: a week back, no short clips, and a steady pace.
func Defaults() Settings {
	return Settings{
		LookBackDays: 7, EveryMinutes: 30, MinMinutes: 15, MinMB: 100,
		SkipWords: "trailer,teaser,sample,promo", MaxPerDay: 500, PauseSeconds: 60,
	}
}

// Validate refuses values that make no sense.
func (s Settings) Validate() error {
	for _, n := range []int{s.LookBackDays, s.MinMinutes, s.MinMB, s.MaxGB, s.MaxPerDay, s.PauseSeconds} {
		if n < 0 {
			return errors.New("numbers cannot be negative")
		}
	}
	if s.EveryMinutes < 5 {
		return errors.New("check at most every 5 minutes")
	}
	if s.MaxGB > 0 && s.MinMB > 0 && int64(s.MaxGB)*1024 < int64(s.MinMB) {
		return errors.New("the largest size is below the smallest size")
	}
	return nil
}

func decodeSettings(raw string) Settings {
	s := Defaults()
	if raw == "" {
		return s
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil || s.Validate() != nil {
		return Defaults()
	}
	return s
}

var videoExt = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".mov": true, ".m4v": true, ".ts": true,
	".webm": true, ".wmv": true, ".mpg": true, ".mpeg": true, ".m2ts": true, ".flv": true,
}

func words(csv string) []string {
	var out []string
	for _, w := range strings.Split(csv, ",") {
		if w = strings.ToLower(strings.TrimSpace(w)); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// Wanted says whether a post looks like a film or an episode worth keeping, and if not, why.
func (s Settings) Wanted(p tguser.Post) (bool, string) {
	if p.DocID == 0 {
		return false, "no document"
	}
	if p.Round || p.Animated {
		return false, "a short clip"
	}
	isVideo := strings.HasPrefix(strings.ToLower(p.MIME), "video/") || videoExt[strings.ToLower(path.Ext(p.Name))]
	if !isVideo {
		return false, "not a video"
	}
	if s.MinMB > 0 && p.Size < int64(s.MinMB)<<20 {
		return false, "smaller than the minimum size"
	}
	if s.MaxGB > 0 && p.Size > int64(s.MaxGB)<<30 {
		return false, "larger than the maximum size"
	}
	if s.MinMinutes > 0 && p.Duration > 0 && p.Duration.Minutes() < float64(s.MinMinutes) {
		return false, "shorter than the minimum length"
	}
	text := strings.ToLower(p.Name + "\n" + p.Caption)
	for _, w := range words(s.SkipWords) {
		if strings.Contains(text, w) {
			return false, "contains \"" + w + "\""
		}
	}
	if only := words(s.OnlyWords); len(only) > 0 {
		hit := false
		for _, w := range only {
			if strings.Contains(text, w) {
				hit = true
				break
			}
		}
		if !hit {
			return false, "has none of the required words"
		}
	}
	if s.NeedTitle && !naming.Parse(p.Name, p.Caption).Matchable() {
		return false, "no year or episode number in the name"
	}
	return true, ""
}
