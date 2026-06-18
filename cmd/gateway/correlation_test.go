package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/andyjmorgan/slipspace-gateway/internal/headers"
	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ctxIDs captures the session + conversation + parent + agent + user id/source
// resolved onto the downstream request context by the middleware.
type ctxIDs struct {
	sessionID, sessionSource           string
	conversationID, conversationSource string
	parentID                           string
	agentID, agentSource               string
	userID, userSource                 string
}

// serveCorrelation runs the middleware against one request and returns the
// recorder plus the ids observed on the downstream context. The conversation
// and parent resolvers always use the shipped defaults (so tests exercise the
// real Thread-Id / X-Claude-Code-Agent-Id / X-Codex-Parent-Thread-Id chains); a
// nil agent or user resolver defaults to one with no operator extras.
func serveCorrelation(t *testing.T, resolver *observability.SessionResolver, agents *observability.AgentResolver, users *observability.UserResolver, redactor *headers.Redactor, setup func(*http.Request)) (*httptest.ResponseRecorder, ctxIDs) {
	t.Helper()
	if agents == nil {
		agents = observability.NewAgentResolver(nil)
	}
	if users == nil {
		users = observability.NewUserResolver(nil)
	}
	var got ctxIDs
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.sessionID = observability.SessionIDFromContext(r.Context())
		got.sessionSource = observability.SessionIDSourceFromContext(r.Context())
		got.conversationID = observability.ConversationIDFromContext(r.Context())
		got.conversationSource = observability.ConversationIDSourceFromContext(r.Context())
		got.parentID = observability.ParentConversationIDFromContext(r.Context())
		got.agentID = observability.AgentIDFromContext(r.Context())
		got.agentSource = observability.AgentIDSourceFromContext(r.Context())
		got.userID = observability.UserIDFromContext(r.Context())
		got.userSource = observability.UserIDSourceFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	mw := correlationMiddleware(quietLogger(), resolver,
		observability.NewConversationResolver(nil), observability.NewParentResolver(nil),
		agents, users, redactor, next)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	setup(req)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	return rec, got
}

func TestCorrelationMiddleware_ExtractsInboundTraceContext(t *testing.T) {
	// Global propagator is normally installed by observability.Setup; set it
	// directly here. Not parallel — it mutates process-global state.
	otel.SetTextMapPropagator(propagation.TraceContext{})

	var sc trace.SpanContext
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sc = trace.SpanContextFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	mw := correlationMiddleware(quietLogger(), observability.NewSessionResolver(nil),
		observability.NewConversationResolver(nil), observability.NewParentResolver(nil),
		observability.NewAgentResolver(nil), observability.NewUserResolver(nil), headers.NewRedactor(nil), next)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	mw.ServeHTTP(httptest.NewRecorder(), req)

	if !sc.IsValid() {
		t.Fatalf("expected a valid extracted span context from the inbound traceparent")
	}
	if got := sc.TraceID().String(); got != "0af7651916cd43dd8448eb211c80319c" {
		t.Errorf("extracted trace id = %q, want the inbound 0af7651916cd43dd8448eb211c80319c", got)
	}
	if !sc.IsRemote() {
		t.Errorf("expected the extracted span context to be marked remote")
	}
}

func TestCorrelationMiddleware_ResolvesClientHeaderAndEchoes(t *testing.T) {
	t.Parallel()
	rec, got := serveCorrelation(t, observability.NewSessionResolver(nil), nil, nil, headers.NewRedactor(nil), func(r *http.Request) {
		r.Header.Set("Session-Id", "sess-42")
	})
	if got.sessionID != "sess-42" || got.sessionSource != "Session-Id" {
		t.Errorf("context session = (%q, %q), want (sess-42, Session-Id)", got.sessionID, got.sessionSource)
	}
	// The resolved bundle id is echoed under the Sluice header.
	if h := rec.Header().Get(observability.SluiceSessionHeader); h != "sess-42" {
		t.Errorf("echoed session = %q, want sess-42", h)
	}
	if rec.Header().Get(headerCorrelationID) == "" {
		t.Errorf("correlation id should be minted and echoed")
	}
}

func TestCorrelationMiddleware_SluiceHeaderWins(t *testing.T) {
	t.Parallel()
	_, got := serveCorrelation(t, observability.NewSessionResolver(nil), nil, nil, headers.NewRedactor(nil), func(r *http.Request) {
		r.Header.Set(observability.SluiceSessionHeader, "sess-authoritative")
		r.Header.Set("Session-Id", "sess-42")
	})
	if got.sessionID != "sess-authoritative" || got.sessionSource != observability.SluiceSessionHeader {
		t.Errorf("session = (%q, %q), want (sess-authoritative, %s)", got.sessionID, got.sessionSource, observability.SluiceSessionHeader)
	}
}

func TestCorrelationMiddleware_NoSessionNoEcho(t *testing.T) {
	t.Parallel()
	rec, got := serveCorrelation(t, observability.NewSessionResolver(nil), nil, nil, headers.NewRedactor(nil), func(r *http.Request) {})
	if got.sessionID != "" {
		t.Errorf("session id = %q, want empty", got.sessionID)
	}
	if h := rec.Header().Get(observability.SluiceSessionHeader); h != "" {
		t.Errorf("no session resolved, but echoed %q", h)
	}
}

func TestCorrelationMiddleware_RedactedSessionHeaderFallsThrough(t *testing.T) {
	t.Parallel()
	// Operator redacts the Sluice session header; resolution must skip it
	// and fall through rather than promote a redacted value.
	redactor := headers.NewRedactor([]string{"x-sluice-session-id"})
	_, got := serveCorrelation(t, observability.NewSessionResolver(nil), nil, nil, redactor, func(r *http.Request) {
		r.Header.Set(observability.SluiceSessionHeader, "sess-secret")
		r.Header.Set("Session-Id", "sess-42")
	})
	if got.sessionID != "sess-42" || got.sessionSource != "Session-Id" {
		t.Errorf("session = (%q, %q), want (sess-42, Session-Id) — redacted Sluice header must fall through", got.sessionID, got.sessionSource)
	}
}

// --- conversation / thread / parent combination ---

func TestCorrelationMiddleware_CodexMainAgent(t *testing.T) {
	t.Parallel()
	// Codex main agent: Thread-Id equals Session-Id, no parent header. The
	// conversation collapses to the session and there is no parent edge.
	_, got := serveCorrelation(t, observability.NewSessionResolver(nil), nil, nil, headers.NewRedactor(nil), func(r *http.Request) {
		r.Header.Set("Session-Id", "sess-1")
		r.Header.Set("Thread-Id", "sess-1")
	})
	if got.sessionID != "sess-1" {
		t.Errorf("session = %q, want sess-1", got.sessionID)
	}
	if got.conversationID != "sess-1" || got.conversationSource != "Thread-Id" {
		t.Errorf("conversation = (%q, %q), want (sess-1, Thread-Id)", got.conversationID, got.conversationSource)
	}
	if got.parentID != "" {
		t.Errorf("parent = %q, want empty for a main agent", got.parentID)
	}
}

func TestCorrelationMiddleware_CodexSubagentExplicitParent(t *testing.T) {
	t.Parallel()
	// Codex subagent: distinct Thread-Id, explicit X-Codex-Parent-Thread-Id. The
	// conversation is the subagent thread; the parent is read verbatim.
	rec, got := serveCorrelation(t, observability.NewSessionResolver(nil), nil, nil, headers.NewRedactor(nil), func(r *http.Request) {
		r.Header.Set("Session-Id", "sess-1")
		r.Header.Set("Thread-Id", "thread-2")
		r.Header.Set("X-Codex-Parent-Thread-Id", "sess-1")
	})
	if got.sessionID != "sess-1" {
		t.Errorf("session = %q, want sess-1", got.sessionID)
	}
	if got.conversationID != "thread-2" || got.conversationSource != "Thread-Id" {
		t.Errorf("conversation = (%q, %q), want (thread-2, Thread-Id)", got.conversationID, got.conversationSource)
	}
	if got.parentID != "sess-1" {
		t.Errorf("parent = %q, want sess-1 (explicit X-Codex-Parent-Thread-Id)", got.parentID)
	}
	// The resolved conversation is echoed under the Sluice thread header.
	if h := rec.Header().Get(observability.SluiceThreadHeader); h != "thread-2" {
		t.Errorf("echoed conversation = %q, want thread-2", h)
	}
}

func TestCorrelationMiddleware_ClaudeSubagentInferredParent(t *testing.T) {
	t.Parallel()
	// Claude Code: X-Claude-Code-Agent-Id is the subagent thread (NOT an agent),
	// with the session as the implicit parent. It must NOT populate agentID.
	_, got := serveCorrelation(t, observability.NewSessionResolver(nil), observability.NewAgentResolver(nil), nil, headers.NewRedactor(nil), func(r *http.Request) {
		r.Header.Set("X-Claude-Code-Session-Id", "sess-9")
		r.Header.Set("X-Claude-Code-Agent-Id", "agt-thread-5")
	})
	if got.sessionID != "sess-9" {
		t.Errorf("session = %q, want sess-9", got.sessionID)
	}
	if got.conversationID != "agt-thread-5" || got.conversationSource != "X-Claude-Code-Agent-Id" {
		t.Errorf("conversation = (%q, %q), want (agt-thread-5, X-Claude-Code-Agent-Id)", got.conversationID, got.conversationSource)
	}
	if got.parentID != "sess-9" {
		t.Errorf("parent = %q, want sess-9 (inferred from session)", got.parentID)
	}
	if got.agentID != "" {
		t.Errorf("agentID = %q, want empty — X-Claude-Code-Agent-Id must not squat on gen_ai.agent.id", got.agentID)
	}
}

// --- named agent (X-Sluice-Agent-Id only) ---

func TestCorrelationMiddleware_ResolvesNamedAgentAndEchoes(t *testing.T) {
	t.Parallel()
	// gen_ai.agent.id is reserved for a genuinely named agent: only the
	// authoritative X-Sluice-Agent-Id (or an operator extra) feeds it.
	rec, got := serveCorrelation(t, observability.NewSessionResolver(nil), observability.NewAgentResolver(nil), nil, headers.NewRedactor(nil), func(r *http.Request) {
		r.Header.Set(observability.SluiceAgentHeader, "reviewer")
	})
	if got.agentID != "reviewer" || got.agentSource != observability.SluiceAgentHeader {
		t.Errorf("context agent = (%q, %q), want (reviewer, %s)", got.agentID, got.agentSource, observability.SluiceAgentHeader)
	}
	if h := rec.Header().Get(observability.SluiceAgentHeader); h != "reviewer" {
		t.Errorf("echoed agent = %q, want reviewer", h)
	}
}

func TestCorrelationMiddleware_NamedAgentViaOperatorHeader(t *testing.T) {
	t.Parallel()
	// There is no shipped agent default, so an operator extra is the only
	// non-Sluice path to a named agent.
	_, got := serveCorrelation(t, observability.NewSessionResolver(nil), observability.NewAgentResolver([]string{"X-Acme-Agent"}), nil, headers.NewRedactor(nil), func(r *http.Request) {
		r.Header.Set("X-Acme-Agent", "acme-agent")
	})
	if got.agentID != "acme-agent" || got.agentSource != "X-Acme-Agent" {
		t.Errorf("agent = (%q, %q), want (acme-agent, X-Acme-Agent)", got.agentID, got.agentSource)
	}
}

func TestCorrelationMiddleware_NoAgentNoEcho(t *testing.T) {
	t.Parallel()
	rec, got := serveCorrelation(t, observability.NewSessionResolver(nil), observability.NewAgentResolver(nil), nil, headers.NewRedactor(nil), func(r *http.Request) {})
	if got.agentID != "" {
		t.Errorf("agent id = %q, want empty", got.agentID)
	}
	if h := rec.Header().Get(observability.SluiceAgentHeader); h != "" {
		t.Errorf("no agent resolved, but echoed %q", h)
	}
}

func TestCorrelationMiddleware_ResolvesUserHeaderAndEchoes(t *testing.T) {
	t.Parallel()
	rec, got := serveCorrelation(t, observability.NewSessionResolver(nil), nil, observability.NewUserResolver(nil), headers.NewRedactor(nil), func(r *http.Request) {
		r.Header.Set(observability.SluiceUserHeader, "usr-42")
	})
	if got.userID != "usr-42" || got.userSource != observability.SluiceUserHeader {
		t.Errorf("context user = (%q, %q), want (usr-42, %s)", got.userID, got.userSource, observability.SluiceUserHeader)
	}
	// The resolved user id is echoed under the Sluice user header.
	if h := rec.Header().Get(observability.SluiceUserHeader); h != "usr-42" {
		t.Errorf("echoed user = %q, want usr-42", h)
	}
}

func TestCorrelationMiddleware_ResolvesUserViaOperatorHeader(t *testing.T) {
	t.Parallel()
	// No client ships a default user header, so an operator extra is the only
	// non-Sluice path. The resolved value is still echoed under the Sluice header.
	rec, got := serveCorrelation(t, observability.NewSessionResolver(nil), nil, observability.NewUserResolver([]string{"X-Acme-User-Id"}), headers.NewRedactor(nil), func(r *http.Request) {
		r.Header.Set("X-Acme-User-Id", "acme-9")
	})
	if got.userID != "acme-9" || got.userSource != "X-Acme-User-Id" {
		t.Errorf("user = (%q, %q), want (acme-9, X-Acme-User-Id)", got.userID, got.userSource)
	}
	if h := rec.Header().Get(observability.SluiceUserHeader); h != "acme-9" {
		t.Errorf("echoed user = %q, want acme-9", h)
	}
}

func TestCorrelationMiddleware_NoUserNoEcho(t *testing.T) {
	t.Parallel()
	rec, got := serveCorrelation(t, observability.NewSessionResolver(nil), nil, observability.NewUserResolver(nil), headers.NewRedactor(nil), func(r *http.Request) {})
	if got.userID != "" {
		t.Errorf("user id = %q, want empty", got.userID)
	}
	if h := rec.Header().Get(observability.SluiceUserHeader); h != "" {
		t.Errorf("no user resolved, but echoed %q", h)
	}
}

func TestCorrelationMiddleware_RedactedUserHeaderFallsThrough(t *testing.T) {
	t.Parallel()
	// Operator redacts the Sluice user header; resolution must skip it and fall
	// through to the operator extra rather than promote a redacted value.
	redactor := headers.NewRedactor([]string{"x-sluice-user-id"})
	_, got := serveCorrelation(t, observability.NewSessionResolver(nil), nil, observability.NewUserResolver([]string{"X-Acme-User-Id"}), redactor, func(r *http.Request) {
		r.Header.Set(observability.SluiceUserHeader, "usr-secret")
		r.Header.Set("X-Acme-User-Id", "acme-9")
	})
	if got.userID != "acme-9" || got.userSource != "X-Acme-User-Id" {
		t.Errorf("user = (%q, %q), want (acme-9, X-Acme-User-Id) — redacted Sluice header must fall through", got.userID, got.userSource)
	}
}
