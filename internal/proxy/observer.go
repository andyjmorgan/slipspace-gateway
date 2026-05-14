package proxy

import (
	"context"
	"net/http"
)

// Observer receives lifecycle signals from the Forwarder. Implementations
// bridge to telemetry and, in a follow-up wave, to the pipeline channel that
// publishes typed messages to downstream middleware.
type Observer interface {
	OnRequestStart(ctx context.Context, dest Destination)
	OnResponseHeaders(ctx context.Context, statusCode int, headers http.Header, streaming bool)
	OnUpstreamError(ctx context.Context, err error)
	OnComplete(ctx context.Context, statusCode int, durationMs int64)
}

type nopObserver struct{}

func (nopObserver) OnRequestStart(context.Context, Destination)               {}
func (nopObserver) OnResponseHeaders(context.Context, int, http.Header, bool) {}
func (nopObserver) OnUpstreamError(context.Context, error)                    {}
func (nopObserver) OnComplete(context.Context, int, int64)                    {}
