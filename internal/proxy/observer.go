package proxy

import (
	"context"
	"net/http"
)

// Observer receives lifecycle signals from the Forwarder. Implementations
// bridge to telemetry and, in a follow-up wave, to the pipeline channel
// that publishes typed messages to downstream middleware.
//
// One Observer instance is created per Forward call by an ObserverFactory,
// so implementations may own per-request state as plain struct fields. All
// four methods are invoked synchronously from the request goroutine — the
// Forwarder does no concurrent fan-out — so no internal locking is required
// to coordinate writes across the lifecycle.
type Observer interface {
	// OnRequestStart fires once, before the upstream HTTP call is issued.
	OnRequestStart(ctx context.Context, dest Destination)

	// OnResponseHeaders fires once when the upstream response headers
	// arrive. streaming is true iff the upstream Content-Type is
	// text/event-stream (per RFC 6202 / SSE).
	OnResponseHeaders(ctx context.Context, statusCode int, headers http.Header, streaming bool)

	// OnUpstreamError fires instead of OnResponseHeaders when the request
	// cannot reach the upstream or the upstream tears the connection
	// before headers arrive. The Forwarder will have written 502 to the
	// client before this fires.
	OnUpstreamError(ctx context.Context, err error)

	// OnComplete fires once, after the response is fully written (or
	// aborted). statusCode reflects whatever the client actually saw —
	// the upstream status on success, 502 on transport failure.
	OnComplete(ctx context.Context, statusCode int, durationMs int64)
}

// ObserverFactory produces a fresh Observer for a single Forward call. The
// Forwarder invokes the factory at the top of every Forward, then drives
// the four lifecycle methods on the returned value. Callers use this seam
// to own per-request state as plain struct fields on the Observer — no
// shared singleton, no context-value plumbing, no locking.
//
// dest is the resolved destination for the call; ctx is the request
// context (and so carries the per-request logger and correlation ID via
// observability).
type ObserverFactory func(ctx context.Context, dest Destination) Observer

type nopObserver struct{}

func (nopObserver) OnRequestStart(context.Context, Destination)               {}
func (nopObserver) OnResponseHeaders(context.Context, int, http.Header, bool) {}
func (nopObserver) OnUpstreamError(context.Context, error)                    {}
func (nopObserver) OnComplete(context.Context, int, int64)                    {}

// nopObserverFactory is the default factory installed when New is given a
// nil ObserverFactory. It allocates nothing per request.
func nopObserverFactory(context.Context, Destination) Observer { return nopObserver{} }
