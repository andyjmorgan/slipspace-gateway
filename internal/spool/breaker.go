package spool

import (
	"sync"
	"time"
)

// breakerState is the circuit-breaker FSM. Exposed as an int for the
// connector_circuit_breaker_state gauge.
type breakerState int

const (
	breakerClosed breakerState = iota
	breakerHalfOpen
	breakerOpen
)

// breaker is a per-track circuit-breaker. closed → halfOpen on N
// consecutive Upload failures; halfOpen → closed on a probe success or
// → open on probe failure; open → halfOpen after a cooldown elapses.
type breaker struct {
	mu               sync.Mutex
	state            breakerState
	failuresToOpen   int
	halfOpenAfter    time.Duration
	consecutiveFails int
	openedAt         time.Time
	now              func() time.Time
}

func newBreaker(opts BreakerOpts, now func() time.Time) *breaker {
	opts = opts.withDefaults()
	if now == nil {
		now = time.Now
	}
	return &breaker{
		state:          breakerClosed,
		failuresToOpen: opts.FailuresToOpen,
		halfOpenAfter:  opts.HalfOpenAfter,
		now:            now,
	}
}

// Allow reports whether the breaker permits an upload attempt right
// now. Closed always allows; halfOpen allows one probe at a time;
// open allows once HalfOpenAfter has elapsed, transitioning to
// halfOpen.
func (b *breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case breakerClosed, breakerHalfOpen:
		return true
	case breakerOpen:
		if b.now().Sub(b.openedAt) >= b.halfOpenAfter {
			b.state = breakerHalfOpen
			return true
		}
		return false
	}
	return false
}

// RecordSuccess transitions any non-closed state back to closed and
// resets the failure counter.
func (b *breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = breakerClosed
	b.consecutiveFails = 0
}

// RecordFailure increments the failure counter; the breaker opens
// when failuresToOpen is reached.
func (b *breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case breakerClosed:
		b.consecutiveFails++
		if b.consecutiveFails >= b.failuresToOpen {
			b.state = breakerOpen
			b.openedAt = b.now()
		}
	case breakerHalfOpen:
		// Probe failed — back to open.
		b.state = breakerOpen
		b.openedAt = b.now()
	case breakerOpen:
		// Already open; no-op.
	}
}

// State returns the current breaker state. Useful for metrics.
func (b *breaker) State() breakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}
