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

	"github.com/andyjmorgan/sluice-gateway/internal/headers"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ctxIDs captures the session + agent id/source resolved onto the downstream
// request context by the middleware.
type ctxIDs struct {
	sessionID, sessionSource string
	agentID, agentSource     string
}

// serveCorrelation runs the middleware against one request and returns the
// recorder plus the session/agent id/source observed on the downstream context.
// A nil agent resolver defaults to one with no operator extras.
func serveCorrelation(t *testing.T, resolver *observability.SessionResolver, agents *observability.AgentResolver, redactor *headers.Redactor, setup func(*http.Request)) (*httptest.ResponseRecorder, ctxIDs) {
	t.Helper()
	if agents == nil {
		agents = observability.NewAgentResolver(nil)
	}
	var got ctxIDs
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.sessionID = observability.SessionIDFromContext(r.Context())
		got.sessionSource = observability.SessionIDSourceFromContext(r.Context())
		got.agentID = observability.AgentIDFromContext(r.Context())
		got.agentSource = observability.AgentIDSourceFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	mw := correlationMiddleware(quietLogger(), resolver, agents, redactor, next)
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
	mw := correlationMiddleware(quietLogger(), observability.NewSessionResolver(nil), observability.NewAgentResolver(nil), headers.NewRedactor(nil), next)
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
	rec, got := serveCorrelation(t, observability.NewSessionResolver(nil), nil, headers.NewRedactor(nil), func(r *http.Request) {
		r.Header.Set("Thread_id", "thread-42")
	})
	if got.sessionID != "thread-42" || got.sessionSource != "Thread_id" {
		t.Errorf("context session = (%q, %q), want (thread-42, Thread_id)", got.sessionID, got.sessionSource)
	}
	// The resolved bundle id is echoed under the Sluice header.
	if h := rec.Header().Get(observability.SluiceSessionHeader); h != "thread-42" {
		t.Errorf("echoed session = %q, want thread-42", h)
	}
	if rec.Header().Get(headerCorrelationID) == "" {
		t.Errorf("correlation id should be minted and echoed")
	}
}

func TestCorrelationMiddleware_SluiceHeaderWins(t *testing.T) {
	t.Parallel()
	_, got := serveCorrelation(t, observability.NewSessionResolver(nil), nil, headers.NewRedactor(nil), func(r *http.Request) {
		r.Header.Set(observability.SluiceSessionHeader, "sess-authoritative")
		r.Header.Set("Thread_id", "thread-42")
	})
	if got.sessionID != "sess-authoritative" || got.sessionSource != observability.SluiceSessionHeader {
		t.Errorf("session = (%q, %q), want (sess-authoritative, %s)", got.sessionID, got.sessionSource, observability.SluiceSessionHeader)
	}
}

func TestCorrelationMiddleware_NoSessionNoEcho(t *testing.T) {
	t.Parallel()
	rec, got := serveCorrelation(t, observability.NewSessionResolver(nil), nil, headers.NewRedactor(nil), func(r *http.Request) {})
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
	_, got := serveCorrelation(t, observability.NewSessionResolver(nil), nil, redactor, func(r *http.Request) {
		r.Header.Set(observability.SluiceSessionHeader, "sess-secret")
		r.Header.Set("Thread_id", "thread-42")
	})
	if got.sessionID != "thread-42" || got.sessionSource != "Thread_id" {
		t.Errorf("session = (%q, %q), want (thread-42, Thread_id) — redacted Sluice header must fall through", got.sessionID, got.sessionSource)
	}
}

func TestCorrelationMiddleware_ResolvesAgentHeaderAndEchoes(t *testing.T) {
	t.Parallel()
	rec, got := serveCorrelation(t, observability.NewSessionResolver(nil), observability.NewAgentResolver(nil), headers.NewRedactor(nil), func(r *http.Request) {
		r.Header.Set("X-Claude-Code-Agent-Id", "agt-42")
	})
	if got.agentID != "agt-42" || got.agentSource != "X-Claude-Code-Agent-Id" {
		t.Errorf("context agent = (%q, %q), want (agt-42, X-Claude-Code-Agent-Id)", got.agentID, got.agentSource)
	}
	// The resolved agent id is echoed under the Sluice agent header.
	if h := rec.Header().Get(observability.SluiceAgentHeader); h != "agt-42" {
		t.Errorf("echoed agent = %q, want agt-42", h)
	}
}

func TestCorrelationMiddleware_SluiceAgentHeaderWins(t *testing.T) {
	t.Parallel()
	_, got := serveCorrelation(t, observability.NewSessionResolver(nil), observability.NewAgentResolver(nil), headers.NewRedactor(nil), func(r *http.Request) {
		r.Header.Set(observability.SluiceAgentHeader, "agt-authoritative")
		r.Header.Set("X-Claude-Code-Agent-Id", "agt-42")
	})
	if got.agentID != "agt-authoritative" || got.agentSource != observability.SluiceAgentHeader {
		t.Errorf("agent = (%q, %q), want (agt-authoritative, %s)", got.agentID, got.agentSource, observability.SluiceAgentHeader)
	}
}

func TestCorrelationMiddleware_NoAgentNoEcho(t *testing.T) {
	t.Parallel()
	rec, got := serveCorrelation(t, observability.NewSessionResolver(nil), observability.NewAgentResolver(nil), headers.NewRedactor(nil), func(r *http.Request) {})
	if got.agentID != "" {
		t.Errorf("agent id = %q, want empty", got.agentID)
	}
	if h := rec.Header().Get(observability.SluiceAgentHeader); h != "" {
		t.Errorf("no agent resolved, but echoed %q", h)
	}
}

func TestCorrelationMiddleware_RedactedAgentHeaderFallsThrough(t *testing.T) {
	t.Parallel()
	// Operator redacts the Sluice agent header; resolution must skip it and
	// fall through to the client default rather than promote a redacted value.
	redactor := headers.NewRedactor([]string{"x-sluice-agent-id"})
	_, got := serveCorrelation(t, observability.NewSessionResolver(nil), observability.NewAgentResolver(nil), redactor, func(r *http.Request) {
		r.Header.Set(observability.SluiceAgentHeader, "agt-secret")
		r.Header.Set("X-Claude-Code-Agent-Id", "agt-42")
	})
	if got.agentID != "agt-42" || got.agentSource != "X-Claude-Code-Agent-Id" {
		t.Errorf("agent = (%q, %q), want (agt-42, X-Claude-Code-Agent-Id) — redacted Sluice header must fall through", got.agentID, got.agentSource)
	}
}
