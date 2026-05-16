package main

import (
	"context"
	"fmt"
	"net/http"
	"runtime/debug"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/andyjmorgan/sluice-gateway/internal/httperr"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

// recoverMiddleware is the outermost layer in the data-plane handler
// chain. A panic in any downstream middleware (routing, auth,
// bodycapture, rules, forwarder) is captured here, logged at error
// level with the full stack, recorded against
// gateway.request.panics.total, and converted to a JSON 500 so the
// client never sees a hijacked connection or a half-written body.
//
// Two assumptions worth preserving across edits:
//
//  1. This wraps EVERYTHING below correlationMiddleware so the
//     captured log entry already carries the correlation_id and the
//     enriched per-request logger.
//  2. headersWritten is a best-effort guard. If a downstream handler
//     panicked AFTER calling WriteHeader, we cannot write a 500 — the
//     status line is already on the wire. In that case we log + meter
//     and rely on the client seeing a truncated response. The
//     errResponseWriter wrapper tracks the state.
//
// Standing project posture (per ADR-002): any panic is wrapped; the
// service stays up. This middleware is what enforces that for the
// request path; safego.Go is what enforces it for background goroutines.
func recoverMiddleware(meters *observability.Meters, errs *httperr.Writer, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrapper := &recordingResponseWriter{ResponseWriter: w}
		defer func() { //nolint:contextcheck // recovery handler uses background ctx for meter; request ctx may be partially torn down post-panic
			panicVal := recover()
			if panicVal == nil {
				return
			}
			ctx := r.Context()
			logger := observability.FromContext(ctx)
			stack := debug.Stack()
			logger.ErrorContext(ctx, "request handler panic recovered",
				"path", r.URL.Path,
				"method", r.Method,
				"panic", fmt.Sprintf("%v", panicVal),
				"stack", string(stack),
			)
			if meters != nil && meters.RequestPanicsTotal != nil {
				labels := observability.RequestLabelsFromContext(ctx)
				meters.RequestPanicsTotal.Add(
					context.Background(),
					1,
					metric.WithAttributes(
						attribute.String("provider", labels.Provider),
						attribute.String("endpoint", labels.Endpoint),
					),
				)
			}
			if !wrapper.headersWritten {
				errs.Write(ctx, w, http.StatusInternalServerError, "handler", "panic_recovered", "internal error")
			}
		}()
		next.ServeHTTP(wrapper, r)
	})
}

// recordingResponseWriter is a thin http.ResponseWriter wrapper that
// remembers whether WriteHeader has fired. The recovery middleware
// uses headersWritten to decide whether it can still emit a 500 body
// or has to give up and just log.
type recordingResponseWriter struct {
	http.ResponseWriter

	headersWritten bool
}

func (w *recordingResponseWriter) WriteHeader(code int) {
	w.headersWritten = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *recordingResponseWriter) Write(p []byte) (int, error) {
	w.headersWritten = true
	return w.ResponseWriter.Write(p)
}

// Unwrap exposes the inner ResponseWriter so net/http internals
// (Hijacker, Flusher) keep working after the wrap. Go 1.20+ uses
// the rwUnwrapper interface contract.
func (w *recordingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
