package tguser

import (
	"errors"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
)

// Pace is how fast one session may send, and when it must rest.
// These numbers are cautious guesses. Telegram publishes no official cap.
type Pace struct {
	MBPerSec        int
	RestMinutes     int
	LongEveryGB     int
	LongRestMinutes int
	DailyGB         int
	Hours           string
}

// Validate refuses a value that would be written to the settings file and stop the next start.
func (p Pace) Validate() error {
	if p.MBPerSec < 0 || p.RestMinutes < 0 || p.LongEveryGB < 0 || p.LongRestMinutes < 0 || p.DailyGB < 0 {
		return errors.New("upload limits cannot be negative")
	}
	_, _, err := config.ParseHours(p.Hours)
	return err
}

// Gate is the decision for one file at one moment.
type Gate struct {
	// Ready means the session may take the file now.
	Ready bool
	// Wait is how long until it might be ready. Zero with Ready false means it cannot take this file.
	Wait time.Duration
	// Reason is shown in the panel when the file cannot move.
	Reason string
}

// Allow decides whether this session can start a file of size bytes.
func Allow(p Pace, now time.Time, size, sentToday, sinceLong int64, restUntil time.Time, maxBytes int64) Gate {
	if maxBytes > 0 && size > maxBytes {
		return Gate{Reason: "larger than this account can send"}
	}
	if p.DailyGB > 0 && size > int64(p.DailyGB)<<30 {
		return Gate{Reason: "larger than the daily limit"}
	}
	start, end, err := config.ParseHours(p.Hours)
	if err != nil {
		start, end = 0, 0
	}
	var wait time.Duration
	if !config.WithinHours(start, end, now.Hour()) {
		wait = untilHour(now, start)
	}
	if p.DailyGB > 0 && sentToday+size > int64(p.DailyGB)<<30 {
		next := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Add(24 * time.Hour)
		if d := next.Sub(now); d > wait {
			wait = d
		}
	}
	if restUntil.After(now) {
		if d := restUntil.Sub(now); d > wait {
			wait = d
		}
	}
	_ = sinceLong
	if wait > 0 {
		return Gate{Wait: wait}
	}
	return Gate{Ready: true}
}

// RestAfter is the pause to store after a file of size bytes has been sent.
// sinceLong is the total since the previous long rest, including this file.
func RestAfter(p Pace, sinceLong int64, mul int) (rest time.Duration, resetLong bool) {
	if mul < 1 {
		mul = 1
	}
	every := int64(p.LongEveryGB) << 30
	if every > 0 && sinceLong >= every {
		return time.Duration(p.LongRestMinutes*mul) * time.Minute, true
	}
	return time.Duration(p.RestMinutes*mul) * time.Minute, false
}

func untilHour(now time.Time, hour int) time.Duration {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next.Sub(now)
}

func (m Meta) pace(def Pace) Pace {
	out := def
	if m.MBPerSec != nil {
		out.MBPerSec = *m.MBPerSec
	}
	if m.RestMinutes != nil {
		out.RestMinutes = *m.RestMinutes
	}
	if m.LongEveryGB != nil {
		out.LongEveryGB = *m.LongEveryGB
	}
	if m.LongRestMinutes != nil {
		out.LongRestMinutes = *m.LongRestMinutes
	}
	if m.DailyGB != nil {
		out.DailyGB = *m.DailyGB
	}
	if m.Hours != nil {
		out.Hours = *m.Hours
	}
	return out
}
