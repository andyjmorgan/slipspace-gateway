package spool

import (
	"testing"
	"time"
)

func TestBreaker_DefaultsApplied(t *testing.T) {
	b := newBreaker(BreakerOpts{}, nil)
	if b.failuresToOpen != DefaultFailuresToOpen {
		t.Errorf("failuresToOpen = %d, want %d", b.failuresToOpen, DefaultFailuresToOpen)
	}
	if b.halfOpenAfter != DefaultHalfOpenAfter {
		t.Errorf("halfOpenAfter = %v, want %v", b.halfOpenAfter, DefaultHalfOpenAfter)
	}
}

func TestBreaker_ClosedAllowsAndIgnoresSuccess(t *testing.T) {
	b := newBreaker(BreakerOpts{FailuresToOpen: 3, HalfOpenAfter: time.Second}, fixedClock())
	for i := 0; i < 10; i++ {
		if !b.Allow() {
			t.Fatalf("closed breaker should always Allow (iter %d)", i)
		}
	}
	b.RecordSuccess()
	if b.State() != breakerClosed {
		t.Errorf("state after success = %v, want closed", b.State())
	}
}

func TestBreaker_OpensAfterConsecutiveFailures(t *testing.T) {
	b := newBreaker(BreakerOpts{FailuresToOpen: 3, HalfOpenAfter: time.Hour}, fixedClock())
	b.RecordFailure()
	b.RecordFailure()
	if b.State() != breakerClosed {
		t.Errorf("state after 2 failures = %v, want still closed", b.State())
	}
	b.RecordFailure()
	if b.State() != breakerOpen {
		t.Errorf("state after 3 failures = %v, want open", b.State())
	}
	if b.Allow() {
		t.Error("open breaker should reject Allow before cooldown")
	}
}

func TestBreaker_SuccessResetsFailureCount(t *testing.T) {
	b := newBreaker(BreakerOpts{FailuresToOpen: 3, HalfOpenAfter: time.Hour}, fixedClock())
	b.RecordFailure()
	b.RecordFailure()
	b.RecordSuccess()
	b.RecordFailure()
	b.RecordFailure()
	if b.State() != breakerClosed {
		t.Errorf("state should still be closed after intervening success, got %v", b.State())
	}
}

func TestBreaker_HalfOpenOnCooldown(t *testing.T) {
	now := time.Now()
	tick := now
	clock := func() time.Time { return tick }

	b := newBreaker(BreakerOpts{FailuresToOpen: 2, HalfOpenAfter: 100 * time.Millisecond}, clock)
	b.RecordFailure()
	b.RecordFailure()
	if b.State() != breakerOpen {
		t.Fatal("expected open")
	}
	if b.Allow() {
		t.Error("open should reject before cooldown elapses")
	}
	tick = tick.Add(200 * time.Millisecond)
	if !b.Allow() {
		t.Error("expected probe allowed after cooldown")
	}
	if b.State() != breakerHalfOpen {
		t.Errorf("state = %v, want half-open after probe Allow", b.State())
	}
}

func TestBreaker_HalfOpenFailureReopens(t *testing.T) {
	now := time.Now()
	tick := now
	clock := func() time.Time { return tick }
	b := newBreaker(BreakerOpts{FailuresToOpen: 1, HalfOpenAfter: time.Millisecond}, clock)
	b.RecordFailure()
	tick = tick.Add(time.Hour)
	if !b.Allow() { // transitions to half-open
		t.Fatal("expected probe allowed")
	}
	b.RecordFailure()
	if b.State() != breakerOpen {
		t.Errorf("state = %v, want open after probe failure", b.State())
	}
}

func TestBreaker_HalfOpenSuccessCloses(t *testing.T) {
	now := time.Now()
	tick := now
	clock := func() time.Time { return tick }
	b := newBreaker(BreakerOpts{FailuresToOpen: 1, HalfOpenAfter: time.Millisecond}, clock)
	b.RecordFailure()
	tick = tick.Add(time.Hour)
	_ = b.Allow()
	b.RecordSuccess()
	if b.State() != breakerClosed {
		t.Errorf("state = %v, want closed after probe success", b.State())
	}
}

func TestBreaker_DoubleOpenIsNoop(t *testing.T) {
	b := newBreaker(BreakerOpts{FailuresToOpen: 1, HalfOpenAfter: time.Hour}, fixedClock())
	b.RecordFailure() // open
	state1 := b.State()
	b.RecordFailure() // still open, no-op
	if b.State() != state1 {
		t.Errorf("state changed after no-op open failure")
	}
}

func fixedClock() func() time.Time {
	t := time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}
