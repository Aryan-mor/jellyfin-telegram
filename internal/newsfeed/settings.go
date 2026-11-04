package newsfeed

import (
	"encoding/json"

	"github.com/Aryan-mor/jellyfin-telegram/internal/tgsource"
)

const settingsKey = "newsfeed_settings"

// Settings are the rules for news channels only. Direct file channels keep their own copy.
type Settings struct {
	Enabled      bool   `json:"enabled"`
	UserID       int64  `json:"user_id"`
	LookBackDays int    `json:"look_back_days"`
	EveryMinutes int    `json:"every_minutes"`
	MinMinutes   int    `json:"min_minutes"`
	MinMB        int    `json:"min_mb"`
	MaxGB        int    `json:"max_gb"`
	SkipWords    string `json:"skip_words"`
	OnlyWords    string `json:"only_words"`
	NeedTitle    bool   `json:"need_title"`
	MaxPerDay    int    `json:"max_per_day"`
	PauseSeconds int    `json:"pause_seconds"`
}

// Defaults match the direct-file rules, stored separately so each tab can diverge.
func Defaults() Settings {
	d := tgsource.Defaults()
	return Settings{
		LookBackDays: d.LookBackDays, EveryMinutes: d.EveryMinutes, MinMinutes: d.MinMinutes,
		MinMB: d.MinMB, MaxGB: d.MaxGB, SkipWords: d.SkipWords, OnlyWords: d.OnlyWords,
		NeedTitle: d.NeedTitle, MaxPerDay: d.MaxPerDay, PauseSeconds: d.PauseSeconds,
	}
}

// Validate refuses values that make no sense.
func (s Settings) Validate() error {
	return s.rules().Validate()
}

func (s Settings) rules() tgsource.Settings {
	return tgsource.Settings{
		LookBackDays: s.LookBackDays, EveryMinutes: s.EveryMinutes, MinMinutes: s.MinMinutes,
		MinMB: s.MinMB, MaxGB: s.MaxGB, SkipWords: s.SkipWords, OnlyWords: s.OnlyWords,
		NeedTitle: s.NeedTitle, MaxPerDay: s.MaxPerDay, PauseSeconds: s.PauseSeconds,
	}
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
