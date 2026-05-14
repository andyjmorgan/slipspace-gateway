package main

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

const (
	headerCorrelationID = "X-Sluice-Correlation-Id"
	headerSessionID     = "X-Sluice-Session-Id"
)

// reqState carries the per-request mutable values the reporter Observer needs.
// It lives on the request context so a single Observer instance can serve all
// requests without owning any state itself.
type reqState struct {
	mu sync.Mutex

	// started is the wall-clock time at which OnRequestStart fired. Used
	// both as the base for the overall request duration and as the
	// reference point for the TTFB measurement.
	started time.Time

	// firstByte is the wall-clock time at which OnResponseHeaders fired —
	// the moment the upstream response headers reached the proxy. Zero
	// when the upstream returned no headers (transport failure).
	firstByte time.Time

	provider   string
	endpoint   string
	statusCode int
	streaming  bool
	upstream   error
}

type reqStateKey struct{}

func withReqState(ctx context.Context, s *reqState) context.Context {
	return context.WithValue(ctx, reqStateKey{}, s)
}

func reqStateFromContext(ctx context.Context) *reqState {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(reqStateKey{}).(*reqState)
	return s
}

// correlationMiddleware assigns a correlation ID (honouring the inbound header
// when present), enriches the per-request logger, echoes the ID on the
// response, and allocates the reqState slot used by the reporter Observer.
func correlationMiddleware(baseLogger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(headerCorrelationID)
		if id == "" {
			id = observability.NewCorrelationID()
		}
		ctx := observability.WithCorrelationID(r.Context(), id)
		logger := baseLogger.With(observability.LogFieldCorrelationID, id)
		ctx = observability.WithLogger(ctx, logger)

		ctx = withReqState(ctx, &reqState{})

		w.Header().Set(headerCorrelationID, id)
		if sid := r.Header.Get(headerSessionID); sid != "" {
			w.Header().Set(headerSessionID, sid)
		}

		logger.InfoContext(ctx, "request received",
			"method", r.Method,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
		)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
