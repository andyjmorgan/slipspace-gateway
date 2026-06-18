package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime/debug"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/andyjmorgan/slipspace-gateway/internal/httperr"
	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
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
			// http.ErrAbortHandler is the stdlib's benign "abort this request"
			// signal, not a code panic: httputil.ReverseProxy raises it when a
			// streamed response copy fails because the client disconnected or
			// the upstream broke mid-stream. The stdlib http.Server recovers it
			// silently; because this middleware sits below the server it catches
			// it first, so mirror that intent — log at debug, do NOT count it
			// against gateway.request.panics.total, and leave the already-aborted
			// stream alone (no 500: headers are long since on the wire). Without
			// this, every client-cancelled SSE stream looks like a handler panic.
			if err, ok := panicVal.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				logger.DebugContext(ctx, "request stream aborted",
					"path", r.URL.Path,
					"method", r.Method,
				)
				return
			}
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
						attribute.String("protocol", labels.Protocol),
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

// Flush delegates to the inner writer via http.ResponseController so
// streaming flushes (httputil.ReverseProxy with FlushInterval=-1) reach
// the underlying connection. Without this explicit method the proxy's
// `(http.Flusher)` type assertion fails on the wrapper — and Unwrap
// alone does not help, because the assertion is on the immediate type,
// not the chain. SSE then buffers until the upstream closes the body,
// which presents to clients as a slow gateway.
func (w *recordingResponseWriter) Flush() {
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Hijack delegates to the inner writer via http.ResponseController so
// HTTP/1.1 upgrade paths (websockets, raw byte streams) keep working
// through the wrap. Same reasoning as Flush: a Hijacker type-assertion
// on the wrapper would otherwise miss.
func (w *recordingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, brw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	w.headersWritten = true
	return conn, brw, nil
}
