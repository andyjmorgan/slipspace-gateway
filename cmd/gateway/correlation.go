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
//
// sessionIDHeaders is the operator-configured list of header names treated as
// session-id aliases (SLUICE_SESSION_ID_HEADERS). The canonical
// X-Sluice-Session-Id always wins; aliases are consulted in order only when it
// is absent. A resolved session id is echoed back on X-Sluice-Session-Id and
// added to the per-request logger as session_id.
func correlationMiddleware(baseLogger *slog.Logger, sessionIDHeaders []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(headerCorrelationID)
		if id == "" {
			id = observability.NewCorrelationID()
		}
		logger := baseLogger.With(observability.LogFieldCorrelationID, id)

		w.Header().Set(headerCorrelationID, id)

		if sid := resolveSessionID(r, sessionIDHeaders); sid != "" {
			w.Header().Set(headerSessionID, sid)
			logger = logger.With(observability.LogFieldSessionID, sid)
		}

		ctx := observability.WithCorrelationID(r.Context(), id)
		ctx = observability.WithLogger(ctx, logger)

		logger.InfoContext(ctx, "request received",
			"method", r.Method,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
		)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// resolveSessionID returns the request's session id. The canonical
// X-Sluice-Session-Id header takes precedence; when it carries no value the
// configured alias headers are tried in order and the first non-empty value
// wins. Returns the empty string when no session header is present.
func resolveSessionID(r *http.Request, aliasHeaders []string) string {
	if sid := r.Header.Get(headerSessionID); sid != "" {
		return sid
	}
	for _, h := range aliasHeaders {
		if sid := r.Header.Get(h); sid != "" {
			return sid
		}
	}
	return ""
}
