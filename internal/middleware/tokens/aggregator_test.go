package tokens_test

import (
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/middleware/tokens"
)

func TestAggregator_ZeroUsageIsNoOp(t *testing.T) {
	t.Parallel()
	var a tokens.Aggregator
	a.Handle(tokens.StrategyLastWins, tokens.Usage{})
	a.Handle(tokens.StrategyAddition, tokens.Usage{})
	a.Handle(tokens.StrategyIncremental, tokens.Usage{})
	got := a.Snapshot()
	if got.Recognised {
		t.Errorf("Recognised=true after only zero-valued Handle calls; want false")
	}
	if got.Input != 0 || got.Output != 0 || got.Cached != 0 || got.CacheCreation != 0 {
		t.Errorf("non-zero snapshot from zero-only handling: %+v", got)
	}
}

func TestAggregator_LastWinsOverwrites(t *testing.T) {
	t.Parallel()
	var a tokens.Aggregator
	a.Handle(tokens.StrategyLastWins, tokens.Usage{Input: 100, Output: 50, Cached: 20, CacheCreation: 10})
	a.Handle(tokens.StrategyLastWins, tokens.Usage{Input: 200, Output: 75, Cached: 40, CacheCreation: 5})

	got := a.Snapshot()
	want := tokens.Snapshot{Input: 200, Output: 75, Cached: 40, CacheCreation: 5, Recognised: true}
	if got != want {
		t.Errorf("snapshot = %+v, want %+v", got, want)
	}
}

func TestAggregator_AdditionSums(t *testing.T) {
	t.Parallel()
	var a tokens.Aggregator
	a.Handle(tokens.StrategyAddition, tokens.Usage{Input: 10, Output: 5})
	a.Handle(tokens.StrategyAddition, tokens.Usage{Input: 7, Output: 3, Cached: 2})
	a.Handle(tokens.StrategyAddition, tokens.Usage{Output: 8, CacheCreation: 1})

	got := a.Snapshot()
	want := tokens.Snapshot{Input: 17, Output: 16, Cached: 2, CacheCreation: 1, Recognised: true}
	if got != want {
		t.Errorf("snapshot = %+v, want %+v", got, want)
	}
}

// TestAggregator_IncrementalKeepsInitialInput proves the airia-platform
// invariant: once Input is set by the first emission, a later
// emission's Input does NOT overwrite it; only Output moves. Matches
// vendors that quote input once up front and stream growing output.
func TestAggregator_IncrementalKeepsInitialInput(t *testing.T) {
	t.Parallel()
	var a tokens.Aggregator
	a.Handle(tokens.StrategyIncremental, tokens.Usage{Input: 100, Output: 5, Cached: 10, CacheCreation: 2})
	a.Handle(tokens.StrategyIncremental, tokens.Usage{Input: 999, Output: 25, Cached: 999, CacheCreation: 999})
	a.Handle(tokens.StrategyIncremental, tokens.Usage{Input: 1, Output: 42})

	got := a.Snapshot()
	want := tokens.Snapshot{Input: 100, Output: 42, Cached: 10, CacheCreation: 2, Recognised: true}
	if got != want {
		t.Errorf("snapshot = %+v, want %+v", got, want)
	}
}

// TestAggregator_RecognisedFlag confirms Recognised tracks "any non-zero
// emission was observed" so the reporter can distinguish "provider did
// not return usage" from "provider returned all zeros".
func TestAggregator_RecognisedFlag(t *testing.T) {
	t.Parallel()
	var a tokens.Aggregator
	if a.Snapshot().Recognised {
		t.Fatalf("Recognised=true on fresh aggregator")
	}
	a.Handle(tokens.StrategyLastWins, tokens.Usage{Input: 1})
	if !a.Snapshot().Recognised {
		t.Errorf("Recognised=false after non-zero Handle")
	}
}
