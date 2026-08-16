// Package resilience holds the gateway's resilience machinery. Failover,
// load-balance, load_balance_with_failover, and the circuit breaker are
// implemented in middleware.go's HTTPHandler (breaker.go for the circuit
// breaker); the Orchestrator/NoneOrchestrator abstraction in this file is a
// superseded v1.0 shim not referenced by the current request path. The
// public policy schema lives in contracts/resilience.
package resilience

import (
	"context"
	"io"

	contractsres "github.com/andyjmorgan/slipspace-gateway/contracts/resilience"
)

// Orchestrator runs the request against one or more targets per the
// configured resilience mode. Only NoneOrchestrator is implemented here;
// failover, load-balance, and circuit-breaker are implemented directly in
// middleware.go's HTTPHandler rather than through this interface (see the
// package doc), so Orchestrator itself is unused by the current request
// path.
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
// breaking, or failover. It is a superseded v1.0 shim; the real failover,
// load-balance, and circuit-breaker behavior lives in middleware.go's
// HTTPHandler, not here.
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
