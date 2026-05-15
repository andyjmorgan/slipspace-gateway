package rules

import (
	"context"
	"sync"

	"github.com/andyjmorgan/sluice-gateway/contracts/events"
)

// MatchBuffer is the per-request slice of rule-match records the
// evaluator writes to. The forwarder's per-request observer (in
// cmd/gateway) drains the buffer at OnComplete to flush
// gateway.rule.matched events as a batch — one publish per request
// rather than mid-evaluation bursts.
//
// The mutex guards against future concurrent writers; today the
// evaluator runs synchronously on the request goroutine so it is
// uncontended.
type MatchBuffer struct {
	mu      sync.Mutex
	records []events.RuleMatched
}

// Record appends a match to the buffer. Safe for concurrent use.
func (b *MatchBuffer) Record(m events.RuleMatched) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.records = append(b.records, m)
}

// Drain returns the records collected so far and clears the buffer.
// Called once at request completion by the observer that publishes
// them onward.
func (b *MatchBuffer) Drain() []events.RuleMatched {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.records
	b.records = nil
	return out
}

type matchBufferKey struct{}

// WithMatchBuffer attaches a fresh MatchBuffer to ctx and returns
// both the derived context and the buffer. The rules middleware
// installs one of these at the top of every request.
func WithMatchBuffer(ctx context.Context) (context.Context, *MatchBuffer) {
	b := &MatchBuffer{}
	return context.WithValue(ctx, matchBufferKey{}, b), b
}

// MatchBufferFromContext returns the buffer installed by
// WithMatchBuffer, or nil when no buffer is present. Consumers
// should tolerate a nil return — it just means rules did not run
// for this request (e.g. control-plane paths that bypass the data-
// plane chain).
func MatchBufferFromContext(ctx context.Context) *MatchBuffer {
	if ctx == nil {
		return nil
	}
	b, _ := ctx.Value(matchBufferKey{}).(*MatchBuffer)
	return b
}
