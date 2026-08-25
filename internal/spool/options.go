package spool

import "time"

// RotationOpts controls when a track seals its active segment and opens
// a new one.
type RotationOpts struct {
	// MaxBytes is the uncompressed byte cap. When the active segment
	// crosses this, it seals and a new one opens. Zero = no byte cap.
	MaxBytes int64

	// MaxAge is the time the active segment may stay open. Zero = no
	// age cap. Either trigger alone is enough; whichever fires first
	// rotates.
	MaxAge time.Duration
}

// Defaults sized for the design note: 64 MiB / 60s for cloud connectors.
const (
	DefaultMaxBytes = 64 * 1024 * 1024
	DefaultMaxAge   = 60 * time.Second
)

func (r RotationOpts) withDefaults() RotationOpts {
	if r.MaxBytes <= 0 {
		r.MaxBytes = DefaultMaxBytes
	}
	if r.MaxAge <= 0 {
		r.MaxAge = DefaultMaxAge
	}
	return r
}

// RetryOpts controls the per-segment retry schedule applied when
// Connector.Upload returns a Retryable error.
type RetryOpts struct {
	// BaseBackoff is the first-attempt sleep ceiling.
	BaseBackoff time.Duration

	// MaxBackoff caps the per-attempt sleep ceiling.
	MaxBackoff time.Duration

	// Multiplier scales BaseBackoff between attempts. 2.0 = doubling.
	Multiplier float64

	// MaxAttempts is the count of Upload calls before the segment is
	// moved to deadletter. Includes the first attempt.
	MaxAttempts int
}

// Defaults pinned to the design note: base 1s, mult 2x, cap 60s, max 8.
const (
	DefaultBaseBackoff = 1 * time.Second
	DefaultMaxBackoff  = 60 * time.Second
	DefaultMultiplier  = 2.0
	DefaultMaxAttempts = 8
)

func (r RetryOpts) withDefaults() RetryOpts {
	if r.BaseBackoff <= 0 {
		r.BaseBackoff = DefaultBaseBackoff
	}
	if r.MaxBackoff <= 0 {
		r.MaxBackoff = DefaultMaxBackoff
	}
	if r.Multiplier <= 1.0 {
		r.Multiplier = DefaultMultiplier
	}
	if r.MaxAttempts <= 0 {
		r.MaxAttempts = DefaultMaxAttempts
	}
	return r
}

// BreakerOpts configures the per-destination circuit breaker that sits
// above per-segment retry. When the breaker opens, the track stops
// uploading entirely; sealed segments accumulate on disk until the
// breaker probes and recovers.
type BreakerOpts struct {
	// FailuresToOpen is the consecutive-failure count that flips the
	// breaker from Closed to Open.
	FailuresToOpen int

	// HalfOpenAfter is how long Open waits before allowing one probe.
	HalfOpenAfter time.Duration
}

// Defaults for [BreakerOpts]. Pinned to the design note: 5 consecutive
// failures opens the breaker; a 30s cooldown elapses before one probe
// is permitted.
const (
	DefaultFailuresToOpen = 5
	DefaultHalfOpenAfter  = 30 * time.Second
)

func (b BreakerOpts) withDefaults() BreakerOpts {
	if b.FailuresToOpen <= 0 {
		b.FailuresToOpen = DefaultFailuresToOpen
	}
	if b.HalfOpenAfter <= 0 {
		b.HalfOpenAfter = DefaultHalfOpenAfter
	}
	return b
}
