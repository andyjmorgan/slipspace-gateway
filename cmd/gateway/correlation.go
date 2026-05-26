package main

import (
	"log/slog"
	"net/http"

	"github.com/andyjmorgan/sluice-gateway/internal/headers"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

const headerCorrelationID = "X-Sluice-Correlation-Id"

// correlationMiddleware assigns a correlation ID (honouring the inbound
// header when present), resolves the session/bundle id from the request
// headers, enriches the per-request logger with both, echoes the
// correlation ID and the resolved session ID on the response, and stores
// both on the request context for downstream surfaces.
//
// Session id is promoted to context (and onto records, spans, and the
// live feed) but never becomes an OTel metric label — it has unbounded
// cardinality and bundling is a records/live-feed concern, not telemetry.
func correlationMiddleware(baseLogger *slog.Logger, sessions *observability.SessionResolver, redactor *headers.Redactor, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(headerCorrelationID)
		if id == "" {
			id = observability.NewCorrelationID()
		}
		ctx := observability.WithCorrelationID(r.Context(), id)
		logger := baseLogger.With(observability.LogFieldCorrelationID, id)

		sessionID, sessionSource := sessions.Resolve(r.Header, redactor.IsSensitive)
		if sessionID != "" {
			ctx = observability.WithSessionID(ctx, sessionID, sessionSource)
			logger = logger.With(observability.LogFieldSessionID, sessionID)
		}
		ctx = observability.WithLogger(ctx, logger)

		w.Header().Set(headerCorrelationID, id)
		// Echo the resolved bundle id under the Sluice header so a client
		// or proxy sees the id Sluice settled on, even when it arrived via
		// a client-specific header (Codex's Thread_id, etc.).
		if sessionID != "" {
			w.Header().Set(observability.SluiceSessionHeader, sessionID)
		}

		logger.InfoContext(ctx, "request received",
			"method", r.Method,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
		)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
