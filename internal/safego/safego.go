// Package safego launches goroutines with a defer recover() that
// converts any panic to a logged + metered event, keeping the
// process alive.
//
// Standing project posture (per CLAUDE.md load-bearing invariant
// #11 — see ADR-002): "any exception must be wrapped and if it's
// unrecoverable we 500, service stays up." Background goroutines
// have no caller to return an error to, so the default Go behaviour
// (a panic in any goroutine crashes the whole process) violates
// this directly. safego.Go is the only blessed way to launch a
// background goroutine; a `TestNoUnsafeGoroutineLaunches`
// reflection-style guard enforces the convention.
package safego

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

// Go launches fn in a new goroutine guarded by a defer recover().
// A panic is logged at error level (including the captured stack)
// and the gateway.goroutine.panics.total counter is incremented,
// then the goroutine returns normally — the process keeps running.
//
// `ctx` flows through to the meter Add via context.WithoutCancel
// so cancellation (commonly active during a panic-induced
// shutdown) does NOT suppress the panic counter — the meter add
// is synchronous and must not be elided.
//
// `site` is the operator-readable identifier for the launch site,
// emitted as the `site` label on the panic counter and the
// structured-log field. Use a stable string (e.g.
// "bus.publisher.worker", "guardrails.post_response",
// "server.signal_handler") so dashboards can attribute panic rate
// to a specific code path.
//
// log + meters may be nil — tests that don't care about
// observability can call safego.Go(ctx, name, nil, nil, fn) and
// still get the panic-suppression behaviour without instrumentation.
func Go(ctx context.Context, site string, log *slog.Logger, meters *observability.Meters, fn func()) {
	go func() {
		defer recoverPanic(ctx, site, log, meters)
		fn()
	}()
}

// Run is the synchronous variant — runs fn on the caller's
// goroutine, recovers from any panic, and returns normally.
// Useful for the request-path recovery middleware where the
// caller's goroutine IS the request goroutine and a new go func
// would defeat the purpose.
func Run(ctx context.Context, site string, log *slog.Logger, meters *observability.Meters, fn func()) {
	defer recoverPanic(ctx, site, log, meters)
	fn()
}

// recoverPanic is the shared deferred handler used by Go and Run.
// Not part of the public API.
func recoverPanic(ctx context.Context, site string, log *slog.Logger, meters *observability.Meters) {
	r := recover()
	if r == nil {
		return
	}
	stack := debug.Stack()
	if log != nil {
		log.Error(
			"goroutine panic recovered",
			"site", site,
			"panic", fmt.Sprintf("%v", r),
			"stack", string(stack),
		)
	}
	if meters != nil && meters.GoroutinePanicsTotal != nil {
		// WithoutCancel preserves any request-scoped values the
		// caller stamped on ctx (correlation_id, tenant labels,
		// etc.) while severing the cancellation parent — the
		// panic may be the very thing that triggered shutdown, and
		// we must not let the counter add disappear because the
		// parent ctx is already Done.
		meters.GoroutinePanicsTotal.Add(
			context.WithoutCancel(ctx),
			1,
			metric.WithAttributes(attribute.String("site", site)),
		)
	}
}
