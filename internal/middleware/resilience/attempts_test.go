package resilience

import (
	"context"
	"testing"
	"time"

	"github.com/andyjmorgan/sluice-gateway/contracts/events"
)

func TestAttemptBuffer_RecordAndDrain(t *testing.T) {
	buf := NewAttemptBuffer("pol")

	buf.Record(events.AttemptRecord{Target: "a", Outcome: attemptOutcomeFailureStatus})
	buf.Record(events.AttemptRecord{Target: "b", Outcome: attemptOutcomeSuccess})

	got := buf.Drain()
	if len(got) != 2 {
		t.Fatalf("Drain returned %d records, want 2", len(got))
	}
	if got[0].Target != "a" || got[0].Outcome != attemptOutcomeFailureStatus {
		t.Errorf("record[0] = %+v; want target=a outcome=failure_status", got[0])
	}
	if got[1].Target != "b" || got[1].Outcome != attemptOutcomeSuccess {
		t.Errorf("record[1] = %+v; want target=b outcome=success", got[1])
	}

	// Drain clears the buffer.
	if again := buf.Drain(); len(again) != 0 {
		t.Errorf("second Drain returned %d, want 0", len(again))
	}
}

func TestAttemptBuffer_PolicyRef(t *testing.T) {
	buf := NewAttemptBuffer("named-policy")
	if got := buf.PolicyRef(); got != "named-policy" {
		t.Errorf("PolicyRef = %q, want named-policy", got)
	}
}

func TestAttemptBuffer_ContextRoundTrip(t *testing.T) {
	buf := NewAttemptBuffer("pol")
	ctx := WithAttemptBuffer(context.Background(), buf)

	got := AttemptBufferFromContext(ctx)
	if got != buf {
		t.Fatalf("AttemptBufferFromContext returned %p; want %p", got, buf)
	}
}

func TestAttemptBuffer_ContextRoundTrip_NilBuffer(t *testing.T) {
	// WithAttemptBuffer(nil) is a passthrough; the context value
	// must not be set so AttemptBufferFromContext returns nil
	// rather than a typed-nil that would compare !nil at call sites.
	ctx := WithAttemptBuffer(context.Background(), nil)
	if got := AttemptBufferFromContext(ctx); got != nil {
		t.Errorf("AttemptBufferFromContext = %p; want nil after WithAttemptBuffer(nil)", got)
	}
}

func TestAttemptBuffer_ContextRoundTrip_NoBuffer(t *testing.T) {
	if got := AttemptBufferFromContext(context.Background()); got != nil {
		t.Errorf("AttemptBufferFromContext on bare context = %p; want nil", got)
	}
}

func TestAttemptBuffer_FireTerminal_InvokesLastClosure(t *testing.T) {
	buf := NewAttemptBuffer("pol")

	first := 0
	last := 0

	buf.SetTerminalPublish(func(durationMs int64, finalStatus int) {
		first++
	})
	buf.SetTerminalPublish(func(durationMs int64, finalStatus int) {
		last = finalStatus
	})

	buf.FireTerminal(42, 200)

	if first != 0 {
		t.Errorf("first closure invoked %d times; want 0 (overwritten)", first)
	}
	if last != 200 {
		t.Errorf("last closure got finalStatus=%d; want 200", last)
	}
}

func TestAttemptBuffer_FireTerminal_Idempotent(t *testing.T) {
	buf := NewAttemptBuffer("pol")

	calls := 0
	buf.SetTerminalPublish(func(int64, int) {
		calls++
	})

	buf.FireTerminal(1, 200)
	buf.FireTerminal(1, 200) // second call should be a no-op

	if calls != 1 {
		t.Errorf("terminal closure invoked %d times; want 1 (FireTerminal idempotent)", calls)
	}
}

func TestAttemptBuffer_FireTerminal_NoClosureNoOp(t *testing.T) {
	buf := NewAttemptBuffer("pol")
	// No closure registered — FireTerminal must not panic.
	buf.FireTerminal(1, 200)
}

func TestAttemptBuffer_NilReceiver(t *testing.T) {
	// All methods are nil-safe so the orchestrator can call without
	// branching when the buffer wasn't constructed (defensive
	// future-proofing — the orchestrator always constructs one today).
	var buf *AttemptBuffer

	buf.Record(events.AttemptRecord{Target: "x"})
	buf.SetTerminalPublish(func(int64, int) {})
	if got := buf.Drain(); got != nil {
		t.Errorf("nil-receiver Drain returned %v; want nil", got)
	}
	if got := buf.PolicyRef(); got != "" {
		t.Errorf("nil-receiver PolicyRef = %q; want empty", got)
	}
	buf.FireTerminal(1, 200)
}

func TestAttemptBuffer_DurationsCarryThrough(t *testing.T) {
	// Sanity check that timestamped records round-trip through
	// Record/Drain without modification.
	now := time.Now().UTC()
	rec := events.AttemptRecord{
		Target:     "t1",
		StartedAt:  now,
		DurationMs: 17,
		StatusCode: 503,
		Outcome:    attemptOutcomeFailureStatus,
	}
	buf := NewAttemptBuffer("pol")
	buf.Record(rec)
	got := buf.Drain()
	if len(got) != 1 {
		t.Fatalf("Drain returned %d; want 1", len(got))
	}
	if !got[0].StartedAt.Equal(rec.StartedAt) {
		t.Errorf("StartedAt = %v; want %v", got[0].StartedAt, rec.StartedAt)
	}
	if got[0].DurationMs != 17 {
		t.Errorf("DurationMs = %d; want 17", got[0].DurationMs)
	}
}
