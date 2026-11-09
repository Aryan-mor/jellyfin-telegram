// Package pace picks random burst sizes and pauses, so copying into Telegram channels does not
// run on a fixed beat that looks like a machine and trips flood limits.
package pace

import (
	"math/rand/v2"
	"time"
)

const (
	// MinBurst and MaxBurst bound how many posts go out back to back.
	// A burst of dozens of forwards is what made Telegram answer FLOOD_WAIT of many minutes.
	MinBurst = 4
	MaxBurst = 8
	// MinPause and MaxPause bound the wait after a burst.
	MinPause = 20 * time.Second
	MaxPause = 3 * time.Minute
)

// Burst returns a random number of posts to send before pausing.
func Burst() int {
	return MinBurst + rand.IntN(MaxBurst-MinBurst+1)
}

// Pause returns a random wait between MinPause and max. A max at or below MinPause gives max.
func Pause(max time.Duration) time.Duration {
	if max <= MinPause {
		return max
	}
	return MinPause + time.Duration(rand.Int64N(int64(max-MinPause)+1))
}
