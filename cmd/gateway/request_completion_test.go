package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/andyjmorgan/slipspace-gateway/internal/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/httperr"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/auth"
	resiliencemw "github.com/andyjmorgan/slipspace-gateway/internal/middleware/resilience"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
	"github.com/andyjmorgan/slipspace-gateway/internal/observability/livefeed"
	"github.com/andyjmorgan/slipspace-gateway/internal/proxy"
)

func TestRequestCompletion_EarlyFailures(t *testing.T) {
	for _, bodies := range []bool{true, false} {
		t.Run(map[bool]string{true: "bodies", false: "no-bodies"}[bodies], func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			ring, err := livefeed.NewRing(20)
			if err != nil {
				t.Fatal(err)
			}
			bodyStore, err := livefeed.NewBodyStore(1 << 20)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := config.Load(context.Background(), writeTestConfig(t, "http://127.0.0.1:1"))
			if err != nil {
				t.Fatal(err)
			}
			store := config.NewStore(resolved)
			meters, err := observability.NewMeters(noop.NewMeterProvider().Meter("test"))
			if err != nil {
				t.Fatal(err)
			}
			reporter := newReporterFactory(nil, store, logger, meters, ring, bodyStore, nil, nil, false, testDefaultCaps(), nil)
			errs := httperr.New(meters.ErrorResponsesTotal, logger)
			plane := buildDataPlaneHandler(auth.NewResolver(store), proxy.New(proxy.Options{Logger: logger, ObserverFactory: reporter.Factory()}),
				rules.NewEvaluator(store, 8, meters), reporter.Factory(), store, resiliencemw.NewInMemoryBreakerStore(nil), nil, meters, errs, nil, logger)
			plane = responseCaptureMiddleware(4096, bodies, nil, reporter.requestCompletionMiddleware(recoverMiddleware(meters, errs, plane)))

			for _, tc := range []struct {
				name, method, path, body, token, reason, model string
				status                                         int
			}{
				{"binding", "POST", "/v1/messages", `{"model":"unmapped-internal","messages":[]}`, "sk_dev_local", "no_binding", "unmapped-internal", 404},
				{"models-route", "GET", "/unmapped/v1/models?token=do-not-store", "", "sk_dev_local", "no_route", "", 404},
				{"method", "POST", "/v1/models", "{}", "sk_dev_local", "method_not_allowed", "", 405},
				{"auth", "GET", "/v1/models", "", "", "unauthorized", "", 401},
				{"malformed", "POST", "/v1/messages", "{", "sk_dev_local", "", "", 400},
			} {
				t.Run(tc.name, func(t *testing.T) {
					before := len(ring.Recent(0))
					req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
					if tc.token != "" {
						req.Header.Set("Authorization", "Bearer "+tc.token)
					}
					ctx := observability.WithLogger(req.Context(), logger)
					ctx = observability.WithCorrelationID(ctx, tc.name)
					ctx = observability.WithSessionID(ctx, "session-test", "Session-Id")
					rec := httptest.NewRecorder()
					plane.ServeHTTP(rec, req.WithContext(ctx))
					if rec.Code != tc.status {
						t.Fatalf("status = %d; body %s", rec.Code, rec.Body.String())
					}
					entries := ring.Recent(0)
					if len(entries) != before+1 {
						t.Fatalf("entries = %d, want %d", len(entries), before+1)
					}
					entry := entries[len(entries)-1]
					if entry.StatusCode != tc.status || entry.Method != tc.method || entry.Model != tc.model || !strings.Contains(entry.GatewayError, tc.reason) || entry.GatewayError == "" {
						t.Fatalf("unexpected entry: %+v", entry)
					}
					if entry.CorrelationID != tc.name || entry.SessionID != "session-test" || strings.Contains(entry.Path, "?") || entry.Provider != "" || entry.UpstreamError != "" {
						t.Fatalf("metadata: %+v", entry)
					}
					if tc.token != "" && entry.Configuration != "dev" {
						t.Fatalf("configuration = %q", entry.Configuration)
					}
					if !strings.Contains(logs.String(), `"msg":"request completed"`) {
						t.Fatal("missing completion log")
					}
					env, ok := bodyStore.Get(entry.EventID)
					if bodies && (!ok || !bytes.Equal(env.Response, rec.Body.Bytes())) {
						t.Fatalf("missing response body: %+v", env)
					}
					if !bodies && len(env.Response) != 0 {
						t.Fatal("captured disabled body")
					}
				})
			}
		})
	}
}

func TestRequestCompletion_ReportedRequestNotDuplicated(t *testing.T) {
	ring, _ := livefeed.NewRing(10)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	reporter := newReporterFactory(nil, nil, logger, nil, ring, nil, nil, nil, false, testDefaultCaps(), nil)
	handler := reporter.requestCompletionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		run := reporter.Factory()(r.Context(), proxy.Destination{})
		w.WriteHeader(200)
		run.OnComplete(r.Context(), 200, 1)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/models", nil))
	if got := ring.Recent(0); len(got) != 1 || got[0].Path != "/v1/models" {
		t.Fatalf("entries: %+v", got)
	}
}

func TestRequestCompletion_Panic(t *testing.T) {
	ring, _ := livefeed.NewRing(10)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	reporter := newReporterFactory(nil, nil, logger, nil, ring, nil, nil, nil, false, testDefaultCaps(), nil)
	handler := reporter.requestCompletionMiddleware(recoverMiddleware(nil, httperr.New(nil, logger), http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("test") })))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/broken", nil))
	entries := ring.Recent(0)
	if len(entries) != 1 || entries[0].StatusCode != 500 || !strings.Contains(entries[0].GatewayError, "panic_recovered") {
		t.Fatalf("entries: %+v", entries)
	}
}

func TestCompletionWriter_StatusAndFlush(t *testing.T) {
	rec := httptest.NewRecorder()
	w := &completionWriter{ResponseWriter: rec}
	w.WriteHeader(103)
	if w.status.Load() != 0 {
		t.Fatal("informational response committed final status")
	}
	// httptest.ResponseRecorder treats 1xx as final, unlike net/http.Server.
	// Replace only the underlying recorder before exercising the final body.
	w.ResponseWriter = httptest.NewRecorder()
	w.WriteHeader(404)
	w.WriteHeader(500)
	_, _ = w.Write([]byte(strings.Repeat("x", 10000)))
	if w.status.Load() != 404 || len(w.errorBody) != 4096 {
		t.Fatal("incorrect final status or unbounded error body")
	}
	rec = httptest.NewRecorder()
	w = &completionWriter{ResponseWriter: rec}
	w.Flush()
	if !rec.Flushed || w.status.Load() != 200 || w.Unwrap() != rec {
		t.Fatal("flush/unwrap failed")
	}
}
