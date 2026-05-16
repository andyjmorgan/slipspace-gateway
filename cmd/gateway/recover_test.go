package main

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/andyjmorgan/sluice-gateway/internal/httperr"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

func newTestLogger(_ *testing.T) *slog.Logger {
	return slog.New(slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func newMetersForTest(t *testing.T) *observability.Meters {
	t.Helper()
	m, err := observability.NewMeters(noop.NewMeterProvider().Meter("test"))
	if err != nil {
		t.Fatalf("NewMeters: %v", err)
	}
	return m
}

// TestRecoverMiddleware_PanicYieldsHTTP500 — the load-bearing case.
// A downstream handler panics; the client gets a 500 with a JSON body,
// not a hijacked or half-written response, and the process keeps
// running (this test's continued execution is the proof).
func TestRecoverMiddleware_PanicYieldsHTTP500(t *testing.T) {
	t.Parallel()
	meters := newMetersForTest(t)
	errs := httperr.New(meters.ErrorResponsesTotal, newTestLogger(t))

	panicker := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("downstream blew up")
	})
	h := recoverMiddleware(meters, errs, panicker)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}

// TestRecoverMiddleware_NoPanic_PassesThrough — the no-op case.
// A normal handler executes; recoverMiddleware leaves the response
// untouched.
func TestRecoverMiddleware_NoPanic_PassesThrough(t *testing.T) {
	t.Parallel()
	meters := newMetersForTest(t)
	errs := httperr.New(meters.ErrorResponsesTotal, newTestLogger(t))

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Test", "yes")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	h := recoverMiddleware(meters, errs, ok)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202 (no recovery should have triggered)", rec.Code)
	}
	if got := rec.Header().Get("X-Test"); got != "yes" {
		t.Errorf("X-Test = %q (passthrough should have preserved headers)", got)
	}
	if got := rec.Body.String(); got != `{"ok":true}` {
		t.Errorf("body = %q (passthrough should have preserved body)", got)
	}
}

// TestRecoverMiddleware_PanicAfterWriteHeader — the partial-response
// case. If the downstream handler already started writing the
// response before panicking, recoverMiddleware cannot write a 500 on
// top (status line is already on the wire). The test asserts the
// middleware doesn't crash, doesn't try to double-write, and the
// process stays alive (test execution is the proof).
func TestRecoverMiddleware_PanicAfterWriteHeader(t *testing.T) {
	t.Parallel()
	meters := newMetersForTest(t)
	errs := httperr.New(meters.ErrorResponsesTotal, newTestLogger(t))

	misbehaving := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial response..."))
		panic("after WriteHeader")
	})
	h := recoverMiddleware(meters, errs, misbehaving)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Status stays 200 — recovery couldn't override it. Body is
	// whatever was written before the panic, no double-write attempt.
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (recovery can't change already-written status)", rec.Code)
	}
	if got := rec.Body.String(); got != "partial response..." {
		t.Errorf("body = %q, want only the pre-panic content", got)
	}
}

// TestRecoverMiddleware_UnwrapAllowsHijack — the http.Hijacker /
// Flusher path needs the wrapper to expose the inner ResponseWriter
// via Unwrap so stdlib's responseWriter type-assertion still works.
// Without Unwrap, http.ResponseController would fail to find a
// Hijacker on a hijacking handler, breaking SSE streaming.
func TestRecoverMiddleware_UnwrapAllowsHijack(t *testing.T) {
	t.Parallel()
	meters := newMetersForTest(t)
	errs := httperr.New(meters.ErrorResponsesTotal, newTestLogger(t))

	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctrl := http.NewResponseController(w)
		if err := ctrl.Flush(); err != nil {
			t.Errorf("Flush via ResponseController failed: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	})
	h := recoverMiddleware(meters, errs, probe)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req.WithContext(context.Background()))
}

// TestRecoverMiddleware_DirectFlusherAssertionWorks — load-bearing for
// SSE: httputil.ReverseProxy and the proxy's statusWriter both do a
// direct `(http.Flusher)` type assertion on the writer they receive,
// not an Unwrap walk. If the wrapper doesn't itself implement Flusher,
// chunks buffer until the upstream closes the body and clients see the
// gateway as slow (the e2e streaming test caught exactly this).
func TestRecoverMiddleware_DirectFlusherAssertionWorks(t *testing.T) {
	t.Parallel()
	meters := newMetersForTest(t)
	errs := httperr.New(meters.ErrorResponsesTotal, newTestLogger(t))

	var sawFlusher bool
	probe := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("wrapper does not implement http.Flusher via direct assertion")
			return
		}
		sawFlusher = true
		f.Flush()
		w.WriteHeader(http.StatusOK)
	})
	h := recoverMiddleware(meters, errs, probe)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req.WithContext(context.Background()))

	if !sawFlusher {
		t.Fatalf("inner handler never observed a Flusher on the wrapper")
	}
}
