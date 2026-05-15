package rules_test

import (
	"context"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/contracts/events"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
)

func TestMatchBuffer_NilReceiverIsSafe(t *testing.T) {
	t.Parallel()
	var b *rules.MatchBuffer
	b.Record(events.RuleMatched{RuleName: "x"})
	if got := b.Drain(); got != nil {
		t.Errorf("Drain on nil = %v, want nil", got)
	}
}

func TestMatchBufferFromContext_Nil(t *testing.T) {
	t.Parallel()
	//nolint:staticcheck // exercising the nil-ctx defensive branch
	if got := rules.MatchBufferFromContext(nil); got != nil {
		t.Errorf("nil ctx should yield nil buffer, got %v", got)
	}
	if got := rules.MatchBufferFromContext(context.Background()); got != nil {
		t.Errorf("unsalted ctx should yield nil buffer, got %v", got)
	}
}

func TestMatchBuffer_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx, b := rules.WithMatchBuffer(context.Background())
	if rules.MatchBufferFromContext(ctx) != b {
		t.Fatal("buffer not retrievable from ctx")
	}
	b.Record(events.RuleMatched{RuleName: "a"})
	b.Record(events.RuleMatched{RuleName: "b"})
	got := b.Drain()
	if len(got) != 2 || got[0].RuleName != "a" || got[1].RuleName != "b" {
		t.Errorf("drain = %v", got)
	}
	if leftover := b.Drain(); len(leftover) != 0 {
		t.Errorf("second drain should be empty, got %v", leftover)
	}
}
