package resilience

import (
	"context"
	"sync"

	"github.com/andyjmorgan/sluice-gateway/contracts/events"
)

// AttemptBuffer is the per-request bridge between the resilience
// orchestrator and the reporter observer. The orchestrator installs
// one on the request context before running runFailover or
// runLoadBalance; the reporter consults it at OnComplete to decide
// whether to publish a per-attempt gateway.request event or defer
// terminal publish to the orchestrator.
//
// Lifecycle, per request:
//
//  1. Orchestrator constructs via NewAttemptBuffer(policyName) and
//     installs via WithAttemptBuffer(ctx, buf).
//  2. For each attempt the orchestrator appends an AttemptRecord
//     (success, failure_status, transport_error, cb_blocked) via
//     Record. Reporter's OnComplete sees the buffer, skips its
//     terminal publish, and registers a closure via
//     SetTerminalPublish. Every per-attempt OnComplete overwrites the
//     closure so the last one — the attempt that committed (commit
//     path) or the final failed attempt (exhausted path) — wins.
//  3. Orchestrator drives FireTerminal(durationMs, finalStatus) at
//     the end of the run. The registered closure consumes the buffer
//     via Drain (and reads PolicyRef) to assemble the consolidated
//     gateway.request payload and publish once.
//
// FireTerminal is a no-op when no closure was registered — e.g. when
// every target was cb_blocked and no attempt fired its observer, or
// when the orchestrator path produced no Observer-bearing call. The
// same gap exists in v1.1 and is intentional here.
//
// AttemptBuffer is per-request; lifetime is bounded by the inbound
// request context. The mutex protects against any future code path
// that records from a goroutine off the request path.
type AttemptBuffer struct {
	mu sync.Mutex

	policy string

	records []events.AttemptRecord

	terminalPublish func(durationMs int64, finalStatus int)
}

// attemptBufferKey is the context key under which AttemptBuffer is
// stashed. Local type so other packages cannot accidentally collide.
type attemptBufferKey struct{}

// NewAttemptBuffer returns an empty buffer tagged with the policy name
// so the terminal publish closure can stamp ev.PolicyRef without the
// reporter needing to thread the name through context.
func NewAttemptBuffer(policy string) *AttemptBuffer {
	return &AttemptBuffer{policy: policy}
}

// WithAttemptBuffer attaches buf to ctx so the reporter can detect
// multi-attempt orchestration. A nil buf is a passthrough so callers
// don't have to branch.
func WithAttemptBuffer(ctx context.Context, buf *AttemptBuffer) context.Context {
	if buf == nil {
		return ctx
	}
	return context.WithValue(ctx, attemptBufferKey{}, buf)
}

// AttemptBufferFromContext returns the buffer installed on ctx, or
// nil when single-shot. Reporter call sites treat nil as "no
// orchestration, publish normally."
func AttemptBufferFromContext(ctx context.Context) *AttemptBuffer {
	v, _ := ctx.Value(attemptBufferKey{}).(*AttemptBuffer)
	return v
}

// PolicyRef returns the policy name the buffer was constructed for.
// Used by the terminal-publish closure to stamp ev.PolicyRef.
func (b *AttemptBuffer) PolicyRef() string {
	if b == nil {
		return ""
	}
	return b.policy
}

// Record appends one AttemptRecord. Called by the orchestrator after
// each attempt resolves (success, failure_status, transport_error)
// and inline for cb_blocked targets where no attempt ran.
func (b *AttemptBuffer) Record(rec events.AttemptRecord) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.records = append(b.records, rec)
}

// SetTerminalPublish registers (or overwrites) the closure that
// publishes the consolidated gateway.request event. The reporter
// installs one each time its OnComplete fires under a buffer; the
// LAST one wins because that's the attempt whose ev (status,
// streaming flag, tokens, tags) corresponds to the bytes the client
// actually received.
func (b *AttemptBuffer) SetTerminalPublish(fn func(durationMs int64, finalStatus int)) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.terminalPublish = fn
}

// Drain returns the recorded attempts and clears the internal slice.
// Called once from the terminal-publish closure so ev.Attempts gets
// the full payload and subsequent calls (defensive against re-entry)
// see an empty buffer.
func (b *AttemptBuffer) Drain() []events.AttemptRecord {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.records
	b.records = nil
	return out
}

// FireTerminal invokes the registered terminal-publish closure (if
// any) with the orchestrator's measured end-to-end duration and final
// HTTP status. Idempotent — the closure pointer is cleared on first
// call so a deferred FireTerminal at the top of the orchestrator can
// coexist with an explicit one without double-publishing.
func (b *AttemptBuffer) FireTerminal(durationMs int64, finalStatus int) {
	if b == nil {
		return
	}
	b.mu.Lock()
	fn := b.terminalPublish
	b.terminalPublish = nil
	b.mu.Unlock()
	if fn != nil {
		fn(durationMs, finalStatus)
	}
}
