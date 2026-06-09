package main

import (
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/andyjmorgan/sluice-gateway/internal/headers"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

const headerCorrelationID = "X-Sluice-Correlation-Id"

// correlationMiddleware assigns a correlation ID (honouring the inbound
// header when present), resolves the session/bundle id, the agent id, and the
// user id from the request headers, enriches the per-request logger with all
// of them, echoes the correlation ID and the resolved session/agent/user IDs
// on the response, and stores them on the request context for downstream
// surfaces.
//
// Session id, conversation/thread id, parent id, agent id, and user id are
// promoted to context (and onto records, spans, and the live feed) but never
// become an OTel metric label — all have unbounded cardinality and are a
// records/live-feed concern, not telemetry.
//
// The conversation/parent combination realises the unified subagent paradigm
// (see the Session Bundling design note): the resolved conversation is the
// thread when present, else the session; the parent is the explicit parent
// header when present, else the session when the thread differs from it. A main
// agent (thread == session, or no thread) has conversation == session and no
// parent.
func correlationMiddleware(baseLogger *slog.Logger, sessions *observability.SessionResolver, conversations *observability.ConversationResolver, parents *observability.ParentResolver, agents *observability.AgentResolver, users *observability.UserResolver, redactor *headers.Redactor, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract any inbound W3C trace context (traceparent/baggage) so the
		// gateway's gen_ai span nests under the caller's distributed trace
		// instead of starting a fresh root. No-op when the caller sent none.
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))

		id := r.Header.Get(headerCorrelationID)
		if id == "" {
			id = observability.NewCorrelationID()
		}
		ctx = observability.WithCorrelationID(ctx, id)
		logger := baseLogger.With(observability.LogFieldCorrelationID, id)

		sessionID, sessionSource := sessions.Resolve(r.Header, redactor.IsSensitive)
		threadID, threadSource := conversations.Resolve(r.Header, redactor.IsSensitive)
		explicitParent, _ := parents.Resolve(r.Header, redactor.IsSensitive)

		// Combine the two axes into the resolved conversation + parent. The
		// conversation is the thread when present, else the session. A thread
		// distinct from the session is a subagent: its parent is the explicit
		// parent header, or the session as a fallback. thread == session (main
		// agent) collapses to conversation == session with no parent.
		conversationID, conversationSource := sessionID, sessionSource
		if threadID != "" {
			conversationID, conversationSource = threadID, threadSource
		}
		parentID := ""
		if conversationID != "" && conversationID != sessionID {
			parentID = explicitParent
			if parentID == "" {
				parentID = sessionID
			}
		}

		if sessionID != "" {
			ctx = observability.WithSessionID(ctx, sessionID, sessionSource)
			logger = logger.With(observability.LogFieldSessionID, sessionID)
		}
		if conversationID != "" {
			ctx = observability.WithConversationID(ctx, conversationID, conversationSource)
		}
		if parentID != "" {
			ctx = observability.WithParentConversationID(ctx, parentID)
		}

		agentID, agentSource := agents.Resolve(r.Header, redactor.IsSensitive)
		if agentID != "" {
			ctx = observability.WithAgentID(ctx, agentID, agentSource)
			logger = logger.With(observability.LogFieldAgentID, agentID)
		}

		userID, userSource := users.Resolve(r.Header, redactor.IsSensitive)
		if userID != "" {
			ctx = observability.WithUserID(ctx, userID, userSource)
			logger = logger.With(observability.LogFieldUserID, userID)
		}
		ctx = observability.WithLogger(ctx, logger)

		w.Header().Set(headerCorrelationID, id)
		// Echo the resolved bundle root under the Sluice header so a client
		// or proxy sees the id Sluice settled on, even when it arrived via
		// a client-specific header (Codex's Session-Id, etc.).
		if sessionID != "" {
			w.Header().Set(observability.SluiceSessionHeader, sessionID)
		}
		// Echo the resolved conversation/thread under the Sluice header, even
		// when it arrived via a client-specific header (Thread-Id,
		// X-Claude-Code-Agent-Id).
		if conversationID != "" {
			w.Header().Set(observability.SluiceThreadHeader, conversationID)
		}
		// Likewise echo the resolved agent id under the Sluice header.
		if agentID != "" {
			w.Header().Set(observability.SluiceAgentHeader, agentID)
		}
		// And the resolved user id, even when it arrived via an operator-configured
		// fallback header rather than the authoritative X-Sluice-User-Id.
		if userID != "" {
			w.Header().Set(observability.SluiceUserHeader, userID)
		}

		logger.InfoContext(ctx, "request received",
			"method", r.Method,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
		)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
