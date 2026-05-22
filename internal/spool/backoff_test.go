package spool

import (
	"math"
	"testing"
	"time"
)

func TestFullJitter_ReturnsZeroForNonPositive(t *testing.T) {
	if got := fullJitter(0); got != 0 {
		t.Errorf("fullJitter(0) = %v", got)
	}
	if got := fullJitter(-1); got != 0 {
		t.Errorf("fullJitter(-1) = %v", got)
	}
	if got := fullJitter(1); got != 0 {
		t.Errorf("fullJitter(1ns) = %v, want 0 (rounded)", got)
	}
}

func TestFullJitter_StaysWithinBound(t *testing.T) {
	// Substitute the source so the test is deterministic.
	orig := randSource
	t.Cleanup(func() { randSource = orig })
	randSource = func(n int64) int64 { return n / 2 }

	got := fullJitter(time.Second)
	if got < 0 || got > time.Second {
		t.Errorf("fullJitter = %v, expected in [0, 1s]", got)
	}
}

func TestNextBackoff_DoublingWithCap(t *testing.T) {
	got := nextBackoff(time.Second, 2.0, 10*time.Second)
	if got != 2*time.Second {
		t.Errorf("nextBackoff(1s,2,10s) = %v, want 2s", got)
	}
	got = nextBackoff(8*time.Second, 2.0, 10*time.Second)
	if got != 10*time.Second {
		t.Errorf("nextBackoff(8s,2,10s) = %v, want capped at 10s", got)
	}
}

func TestNextBackoff_GuardsAgainstOverflow(t *testing.T) {
	// A pathological multiplier wraps int64; we should hit the cap, not
	// roll over to a tiny negative duration.
	got := nextBackoff(time.Duration(math.MaxInt64/2), 10.0, time.Hour)
	if got != time.Hour {
		t.Errorf("nextBackoff overflow guard = %v, want hour cap", got)
	}
}
