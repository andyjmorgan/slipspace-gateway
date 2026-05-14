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

// Orchestrator runs the request against one or more targets per the
// configured resilience mode. v1.0 only NoneOrchestrator is implemented;
// failover, load-balance, and circuit-breaker arrive in v1.1 once the schema
// is locked.
//
// Implementations must be safe for concurrent use across requests since one
// Orchestrator instance is shared by the entire pipeline.
type Orchestrator interface {
	Execute(ctx context.Context, targets []contractsres.ResilienceTarget, exec ExecuteFunc) (*Result, error)
}

// ExecuteFunc is the per-target call the orchestrator invokes. The pipeline
// supplies it; resilience itself does not know what is downstream — it just
// invokes ExecuteFunc and inspects the Result.
//
// Implementations must respect ctx for cancellation and must not retain the
// returned *Result.Response past their own scope (orchestrators may discard
// it on retry).
type ExecuteFunc func(ctx context.Context, target contractsres.ResilienceTarget) (*Result, error)

// Result captures the outcome of one ExecuteFunc invocation.
type Result struct {
	// StatusCode is the upstream HTTP status code. Populated even on
	// upstream errors that produced a response body (e.g. 5xx with a
	// JSON error envelope).
	StatusCode int

	// Response is the upstream response body reader. May be nil when
	// Error is non-nil (transport-level failure with no response). The
	// caller owns the close; orchestrators that abandon a Result on
	// retry must close it themselves.
	Response io.ReadCloser

	// Error is the transport- or orchestrator-level error. Distinct from
	// an HTTP error response — a 500 from the upstream produces
	// StatusCode=500 with Error=nil. Error is non-nil only when the
	// request did not complete.
	Error error
}

// NoneOrchestrator single-attempts the first configured target (or the
// zero-value target when none are configured) without retry, circuit
// breaking, or failover. It is the v1.0 shim that keeps the pipeline
// wiring stable while the real orchestrators land in v1.1.
type NoneOrchestrator struct{}

// Execute invokes exec exactly once against the first target (or the
// zero-value ResilienceTarget when targets is empty) and returns the
// result verbatim.
func (NoneOrchestrator) Execute(ctx context.Context, targets []contractsres.ResilienceTarget, exec ExecuteFunc) (*Result, error) {
	var t contractsres.ResilienceTarget
	if len(targets) > 0 {
		t = targets[0]
	}
	return exec(ctx, t)
}
