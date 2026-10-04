package spool

import (
	"sync"
	"sync/atomic"
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
	if b.State() != BreakerClosed {
		t.Errorf("state after success = %v, want closed", b.State())
	}
}

func TestBreaker_OpensAfterConsecutiveFailures(t *testing.T) {
	b := newBreaker(BreakerOpts{FailuresToOpen: 3, HalfOpenAfter: time.Hour}, fixedClock())
	b.RecordFailure()
	b.RecordFailure()
	if b.State() != BreakerClosed {
		t.Errorf("state after 2 failures = %v, want still closed", b.State())
	}
	b.RecordFailure()
	if b.State() != BreakerOpen {
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
	if b.State() != BreakerClosed {
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
	if b.State() != BreakerOpen {
		t.Fatal("expected open")
	}
	if b.Allow() {
		t.Error("open should reject before cooldown elapses")
	}
	tick = tick.Add(200 * time.Millisecond)
	if !b.Allow() {
		t.Error("expected probe allowed after cooldown")
	}
	if b.State() != BreakerHalfOpen {
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
	if b.State() != BreakerOpen {
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
	if b.State() != BreakerClosed {
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

// TestBreaker_HalfOpenAdmitsExactlyOneProbe pins #545: while the probe is
// outstanding every further Allow is refused; the probe's outcome — or an
// explicit Release — is what frees the gate.
func TestBreaker_HalfOpenAdmitsExactlyOneProbe(t *testing.T) {
	tick := time.Now()
	clock := func() time.Time { return tick }
	b := newBreaker(BreakerOpts{FailuresToOpen: 1, HalfOpenAfter: time.Millisecond}, clock)
	b.RecordFailure()
	tick = tick.Add(time.Hour)

	if !b.Allow() {
		t.Fatal("first caller after cooldown should be admitted as the probe")
	}
	if b.State() != BreakerHalfOpen {
		t.Fatalf("state = %v, want half-open", b.State())
	}
	for i := 0; i < 5; i++ {
		if b.Allow() {
			t.Fatalf("caller %d admitted while the probe is still in flight", i+2)
		}
	}

	// Probe fails → open; nothing admitted until the cooldown elapses
	// again, then exactly one more probe.
	b.RecordFailure()
	if b.State() != BreakerOpen || b.Allow() {
		t.Fatal("failed probe should reopen and refuse before cooldown")
	}
	tick = tick.Add(time.Hour)
	if !b.Allow() || b.Allow() {
		t.Fatal("second cooldown should admit exactly one probe")
	}

	// Probe succeeds → closed → everyone admitted.
	b.RecordSuccess()
	for i := 0; i < 3; i++ {
		if !b.Allow() {
			t.Fatalf("closed breaker refused caller %d", i)
		}
	}
}

func TestBreaker_ReleaseFreesHalfOpenProbe(t *testing.T) {
	tick := time.Now()
	clock := func() time.Time { return tick }
	b := newBreaker(BreakerOpts{FailuresToOpen: 1, HalfOpenAfter: time.Millisecond}, clock)
	b.RecordFailure()
	tick = tick.Add(time.Hour)

	if !b.Allow() || b.Allow() {
		t.Fatal("expected exactly one probe admitted")
	}
	b.Release() // the probe never reached Upload (lost claim race)
	if b.State() != BreakerHalfOpen {
		t.Errorf("Release must not change state, got %v", b.State())
	}
	if !b.Allow() {
		t.Error("Release should hand the probe slot back")
	}

	// Release outside half-open is a no-op.
	b.RecordSuccess()
	b.Release()
	if b.State() != BreakerClosed || !b.Allow() {
		t.Error("Release on a closed breaker should change nothing")
	}
}

// TestBreaker_HalfOpenConcurrentAllow drives N goroutines at a half-open
// breaker and checks that exactly one is admitted. Run under -race this
// also proves the reservation is taken under the mutex, not a
// check-then-set.
func TestBreaker_HalfOpenConcurrentAllow(t *testing.T) {
	tick := time.Now()
	clock := func() time.Time { return tick }
	b := newBreaker(BreakerOpts{FailuresToOpen: 1, HalfOpenAfter: time.Millisecond}, clock)
	b.RecordFailure()
	tick = tick.Add(time.Hour)

	const callers = 64
	var (
		start    = make(chan struct{})
		wg       sync.WaitGroup
		admitted atomic.Int32
	)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if b.Allow() {
				admitted.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := admitted.Load(); got != 1 {
		t.Fatalf("half-open admitted %d concurrent callers, want exactly 1", got)
	}
	// Resolve the probe and the gate reopens for exactly one more.
	b.RecordFailure()
	tick = tick.Add(time.Hour)
	admitted.Store(0)
	start = make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if b.Allow() {
				admitted.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := admitted.Load(); got != 1 {
		t.Fatalf("second half-open window admitted %d, want exactly 1", got)
	}
}

func fixedClock() func() time.Time {
	t := time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}
