package main

import (
	"log/slog"
	"net/http"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

const (
	headerCorrelationID = "X-Sluice-Correlation-Id"
	headerSessionID     = "X-Sluice-Session-Id"
)

// correlationMiddleware assigns a correlation ID (honouring the inbound header
// when present), enriches the per-request logger, and echoes the ID on the
// response. Per-request observer state lives on the proxy.Observer minted by
// the reporter factory; this middleware no longer allocates a shared state
// struct on context.
func correlationMiddleware(baseLogger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(headerCorrelationID)
		if id == "" {
			id = observability.NewCorrelationID()
		}
		ctx := observability.WithCorrelationID(r.Context(), id)
		logger := baseLogger.With(observability.LogFieldCorrelationID, id)
		ctx = observability.WithLogger(ctx, logger)

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
