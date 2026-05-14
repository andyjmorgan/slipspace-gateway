// Package resilience holds the gateway's resilience orchestrators. v1.0 ships
// only the no-op NoneOrchestrator that single-attempts via the supplied
// ExecuteFunc; failover, load-balance, and circuit-breaker land in v1.1. The
// public policy schema lives in contracts/resilience.
package resilience

import (
	"context"
	"io"

	contractsres "github.com/andyjmorgan/sluice-gateway/contracts/resilience"
)

// Orchestrator runs the request against one or more targets per the resilience
// mode. v1.0 only NoneOrchestrator is implemented; the other strategies arrive
// in v1.1 once the schema is locked.
type Orchestrator interface {
	Execute(ctx context.Context, targets []contractsres.ResilienceTarget, exec ExecuteFunc) (*Result, error)
}

// ExecuteFunc is the per-target call provided by the pipeline. Resilience
// doesn't know what is downstream — it just invokes this and inspects the
// outcome.
type ExecuteFunc func(ctx context.Context, target contractsres.ResilienceTarget) (*Result, error)

// Result captures the outcome of one ExecuteFunc invocation. Response may be
// nil when Error is non-nil; callers are responsible for closing Response.
type Result struct {
	StatusCode int

	Response io.ReadCloser

	Error error
}

// NoneOrchestrator single-attempts the first configured target (or the
// zero-value target when none are configured) without retry, circuit breaking,
// or failover. It is the v1.0 shim that keeps the pipeline wiring stable while
// the real orchestrators land in v1.1.
type NoneOrchestrator struct{}

// Execute invokes exec exactly once and returns its result verbatim.
func (NoneOrchestrator) Execute(ctx context.Context, targets []contractsres.ResilienceTarget, exec ExecuteFunc) (*Result, error) {
	var t contractsres.ResilienceTarget
	if len(targets) > 0 {
		t = targets[0]
	}
	return exec(ctx, t)
}
