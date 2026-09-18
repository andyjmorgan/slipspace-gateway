package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/contracts/events"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/auth"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/bodycapture"
	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
	"github.com/andyjmorgan/slipspace-gateway/internal/proxy"
)

// requestCompletion lives for one inbound request, across routing and retry
// contexts. Only the request goroutine reads/writes it; proxy flush goroutines
// never touch it. The ordinary reporter claims completion after retries finish.
type requestCompletion struct {
	ctx       context.Context
	path      string
	published bool
}

type requestCompletionKey struct{}

func completionFromContext(ctx context.Context) *requestCompletion {
	state, _ := ctx.Value(requestCompletionKey{}).(*requestCompletion)
	return state
}

// completionCheckpoint retains metadata added by middleware whose derived
// context would otherwise be invisible to the outer completion fallback.
func completionCheckpoint(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if state := completionFromContext(r.Context()); state != nil {
			state.ctx = r.Context()
		}
		next.ServeHTTP(w, r)
	})
}

// requestCompletionMiddleware reports requests that never reached a terminal
// proxy/rule observer. It deliberately publishes only to the local live feed
// and structured logs: an authentication failure has no trusted connector
// configuration, and a routing failure is not an upstream GenAI invocation.
func (f *reporterFactory) requestCompletionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		state := &requestCompletion{path: r.URL.Path}
		ctx := context.WithValue(r.Context(), requestCompletionKey{}, state)
		state.ctx = ctx
		writer := &completionWriter{ResponseWriter: w}
		next.ServeHTTP(writer, r.WithContext(ctx))
		if state.published {
			return
		}

		f.publishUnreported(r.WithContext(state.ctx), writer, time.Since(start).Milliseconds()) //nolint:contextcheck // checkpoint context is derived from this request, enriched by downstream middleware.
	})
}

func (f *reporterFactory) publishUnreported(r *http.Request, writer *completionWriter, durationMs int64) {
	ctx := r.Context()
	labels := observability.RequestLabelsFromContext(ctx)
	labels.Method = r.Method
	if ar, ok := auth.FromContext(ctx); ok {
		labels.Configuration = ar.ConfigurationName
	}
	pi, _ := protocolInfoFromContext(ctx)
	if labels.Protocol == "" {
		labels.Protocol = pi.protocol
	}
	if labels.Model == "" {
		labels.Model = pi.params["model"]
		if captured, ok := bodycapture.FromContext(ctx); ok && labels.Model == "" {
			labels.Model = bodycapture.Model(captured.Body)
		}
	}
	ctx = observability.WithRequestLabels(ctx, labels)
	run := f.Factory()(ctx, proxy.Destination{}).(*reporterRun)
	status := int(writer.status.Load())
	if status == 0 {
		status = http.StatusOK
	}
	reason := ""
	if status >= 400 {
		var body struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal(writer.errorBody, &body) == nil {
			reason = body.Error
			if body.Message != "" {
				reason += ": " + body.Message
			}
		}
		if reason == "" {
			reason = http.StatusText(status)
		}
	}
	run.gatewayError = reason
	ev := events.Request{
		CorrelationID: observability.CorrelationIDFromContext(ctx),
		Provider:      labels.Provider, Protocol: labels.Protocol, Model: labels.Model,
		Method: r.Method, StatusCode: status, DurationMs: durationMs,
	}
	entryID := run.appendLiveFeed(ev, nil)
	run.captureBody(ctx, entryID, nil, false)
	observability.FromContext(ctx).InfoContext(ctx, "request completed",
		"method", r.Method, "path", r.URL.Path, "status_code", status,
		"duration_ms", ev.DurationMs, "configuration", labels.Configuration,
		"protocol", labels.Protocol, "model", labels.Model, "gateway_error", reason)
}

// completionWriter tracks the client status, ignoring informational responses,
// and bounds the gateway error envelope independently of optional body capture.
// Unwrap and Flush preserve ResponseController and SSE semantics.
type completionWriter struct {
	http.ResponseWriter
	status    atomic.Int64
	errorBody []byte
}

func (w *completionWriter) WriteHeader(status int) {
	if status >= 200 {
		w.status.CompareAndSwap(0, int64(status))
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *completionWriter) Write(p []byte) (int, error) {
	w.status.CompareAndSwap(0, http.StatusOK)
	n, err := w.ResponseWriter.Write(p)
	if w.status.Load() >= 400 && len(w.errorBody) < 4096 {
		w.errorBody = append(w.errorBody, p[:min(n, 4096-len(w.errorBody))]...)
	}
	return n, err
}

func (w *completionWriter) Flush() {
	if w.status.Load() == 0 {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *completionWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func requestPath(ctx context.Context) string {
	if state := completionFromContext(ctx); state != nil {
		return state.path
	}
	return ""
}
