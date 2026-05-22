package spool

import (
	"math/rand/v2"
	"time"
)

// fullJitter returns a sleep in [0, current). Per the design note,
// full jitter is the right choice when many pods recover from the
// same outage — it spreads retries uniformly across the interval.
//
// randSource is a package-level seam tests substitute for deterministic
// behaviour. rand/v2 is intentional — full-jitter doesn't need a
// cryptographic source, only spread to avoid thundering-herd retries.
var randSource = rand.Int64N //nolint:gosec // G404: non-crypto by design

func fullJitter(current time.Duration) time.Duration {
	if current <= 0 {
		return 0
	}
	n := int64(current)
	if n <= 1 {
		return 0
	}
	return time.Duration(randSource(n))
}

// nextBackoff applies the multiplier and caps at max. The current value
// is provided by the caller so backoff state can live on the segment
// (each segment has its own retry schedule).
func nextBackoff(current time.Duration, multiplier float64, maxBackoff time.Duration) time.Duration {
	next := time.Duration(float64(current) * multiplier)
	if next > maxBackoff {
		return maxBackoff
	}
	if next < current {
		// Guard against extreme multipliers wrapping the int64.
		return maxBackoff
	}
	return next
}
