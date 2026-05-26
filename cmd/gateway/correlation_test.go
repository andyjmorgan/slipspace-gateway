package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/headers"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// serveCorrelation runs the middleware against one request and returns the
// recorder plus the session id/source observed on the downstream context.
func serveCorrelation(t *testing.T, resolver *observability.SessionResolver, redactor *headers.Redactor, setup func(*http.Request)) (*httptest.ResponseRecorder, string, string) {
	t.Helper()
	var gotID, gotSource string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = observability.SessionIDFromContext(r.Context())
		gotSource = observability.SessionIDSourceFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	mw := correlationMiddleware(quietLogger(), resolver, redactor, next)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	setup(req)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	return rec, gotID, gotSource
}

func TestCorrelationMiddleware_ResolvesClientHeaderAndEchoes(t *testing.T) {
	t.Parallel()
	rec, id, source := serveCorrelation(t, observability.NewSessionResolver(nil), headers.NewRedactor(nil), func(r *http.Request) {
		r.Header.Set("Thread_id", "thread-42")
	})
	if id != "thread-42" || source != "Thread_id" {
		t.Errorf("context session = (%q, %q), want (thread-42, Thread_id)", id, source)
	}
	// The resolved bundle id is echoed under the Sluice header.
	if got := rec.Header().Get(observability.SluiceSessionHeader); got != "thread-42" {
		t.Errorf("echoed session = %q, want thread-42", got)
	}
	if rec.Header().Get(headerCorrelationID) == "" {
		t.Errorf("correlation id should be minted and echoed")
	}
}

func TestCorrelationMiddleware_SluiceHeaderWins(t *testing.T) {
	t.Parallel()
	_, id, source := serveCorrelation(t, observability.NewSessionResolver(nil), headers.NewRedactor(nil), func(r *http.Request) {
		r.Header.Set(observability.SluiceSessionHeader, "sess-authoritative")
		r.Header.Set("Thread_id", "thread-42")
	})
	if id != "sess-authoritative" || source != observability.SluiceSessionHeader {
		t.Errorf("session = (%q, %q), want (sess-authoritative, %s)", id, source, observability.SluiceSessionHeader)
	}
}

func TestCorrelationMiddleware_NoSessionNoEcho(t *testing.T) {
	t.Parallel()
	rec, id, _ := serveCorrelation(t, observability.NewSessionResolver(nil), headers.NewRedactor(nil), func(r *http.Request) {})
	if id != "" {
		t.Errorf("session id = %q, want empty", id)
	}
	if got := rec.Header().Get(observability.SluiceSessionHeader); got != "" {
		t.Errorf("no session resolved, but echoed %q", got)
	}
}

func TestCorrelationMiddleware_RedactedSessionHeaderFallsThrough(t *testing.T) {
	t.Parallel()
	// Operator redacts the Sluice session header; resolution must skip it
	// and fall through rather than promote a redacted value.
	redactor := headers.NewRedactor([]string{"x-sluice-session-id"})
	_, id, source := serveCorrelation(t, observability.NewSessionResolver(nil), redactor, func(r *http.Request) {
		r.Header.Set(observability.SluiceSessionHeader, "sess-secret")
		r.Header.Set("Thread_id", "thread-42")
	})
	if id != "thread-42" || source != "Thread_id" {
		t.Errorf("session = (%q, %q), want (thread-42, Thread_id) — redacted Sluice header must fall through", id, source)
	}
}
