package spool

import (
	"sync"
	"time"
)

// BreakerState is the per-track circuit-breaker FSM. Backed by an int so a
// snapshot can be carried on TrackStats.BreakerState and observed as the
// gateway.spool.breaker.state gauge (0=closed, 1=half_open, 2=open).
// Distinct from the resilience middleware's gateway.cb.state, which
// watches upstream providers on the request path.
type BreakerState int

const (
	// BreakerClosed is the normal state: every claimed segment uploads.
	BreakerClosed BreakerState = iota
	// BreakerHalfOpen admits exactly one probe upload after HalfOpenAfter.
	BreakerHalfOpen
	// BreakerOpen refuses every upload until HalfOpenAfter elapses.
	BreakerOpen
)

// String returns the lower-case label used as the state_name metric
// attribute: closed, half_open, open. Unknown values stringify as
// "unknown" rather than panicking.
func (s BreakerState) String() string {
	switch s {
	case BreakerClosed:
		return "closed"
	case BreakerHalfOpen:
		return "half_open"
	case BreakerOpen:
		return "open"
	default:
		return "unknown"
	}
}

// breaker is a per-track circuit-breaker. closed → open on N consecutive
// Upload failures; open → halfOpen once halfOpenAfter has elapsed (decided
// lazily in Allow, so there is no timer goroutine); halfOpen → closed on a
// probe success or back → open on a probe failure. closed never transitions
// straight to halfOpen.
type breaker struct {
	mu               sync.Mutex
	state            BreakerState
	failuresToOpen   int
	halfOpenAfter    time.Duration
	consecutiveFails int
	openedAt         time.Time
	now              func() time.Time

	// probeInFlight is the half-open reservation: set when Allow admits
	// the one probe, cleared when RecordSuccess / RecordFailure resolves
	// it or Release abandons it. While set, every other Allow in
	// halfOpen is refused — that is what stops a sealed/ backlog
	// stampeding a destination that has only just come back (#545).
	probeInFlight bool
}

func newBreaker(opts BreakerOpts, now func() time.Time) *breaker {
	opts = opts.withDefaults()
	if now == nil {
		now = time.Now
	}
	return &breaker{
		state:          BreakerClosed,
		failuresToOpen: opts.FailuresToOpen,
		halfOpenAfter:  opts.HalfOpenAfter,
		now:            now,
	}
}

// Allow reports whether the breaker permits an upload attempt right
// now. Closed always allows. Open allows once HalfOpenAfter has
// elapsed, transitioning to halfOpen and reserving that caller as the
// single probe. HalfOpen refuses every further caller until the probe
// resolves via RecordSuccess / RecordFailure (or is abandoned via
// Release) — exactly one probe is in flight at a time, regardless of
// how many uploader goroutines share the breaker.
func (b *breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case BreakerClosed:
		return true
	case BreakerHalfOpen:
		if b.probeInFlight {
			return false
		}
		b.probeInFlight = true
		return true
	case BreakerOpen:
		if b.now().Sub(b.openedAt) >= b.halfOpenAfter {
			b.state = BreakerHalfOpen
			b.probeInFlight = true
			return true
		}
		return false
	}
	return false
}

// Release abandons a half-open probe reservation without recording an
// outcome — for the caller that was admitted by Allow but never reached
// Connector.Upload (a lost claim race, shutdown before the first
// attempt). Without it the reservation would leak and the breaker would
// refuse every subsequent probe forever. A no-op outside halfOpen.
func (b *breaker) Release() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == BreakerHalfOpen {
		b.probeInFlight = false
	}
}

// RecordSuccess transitions any non-closed state back to closed,
// resets the failure counter, and resolves any in-flight probe.
func (b *breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = BreakerClosed
	b.consecutiveFails = 0
	b.probeInFlight = false
}

// RecordFailure increments the failure counter; the breaker opens
// when failuresToOpen is reached. In halfOpen it resolves the probe as
// failed and reopens.
func (b *breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probeInFlight = false
	switch b.state {
	case BreakerClosed:
		b.consecutiveFails++
		if b.consecutiveFails >= b.failuresToOpen {
			b.state = BreakerOpen
			b.openedAt = b.now()
		}
	case BreakerHalfOpen:
		// Probe failed — back to open.
		b.state = BreakerOpen
		b.openedAt = b.now()
	case BreakerOpen:
		// Already open; no-op.
	}
}

// State returns the current breaker state. Useful for metrics.
func (b *breaker) State() BreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}
