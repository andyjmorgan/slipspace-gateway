package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/httperr"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/auth"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/bodycapture"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/proxy"
	"github.com/andyjmorgan/sluice-gateway/internal/routing"
	"github.com/andyjmorgan/sluice-gateway/providers/anthropic/messages"
	openaichat "github.com/andyjmorgan/sluice-gateway/providers/openai/chat"
	openairesponses "github.com/andyjmorgan/sluice-gateway/providers/openai/responses"
)

func TestOutboundModel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		captured bodycapture.Captured
		match    routing.Match
		want     string
	}{
		{
			name:     "openai chat body model",
			captured: bodycapture.Captured{Kind: bodycapture.KindChat, Body: &openaichat.ChatCompletionRequest{Model: "gpt-4o-mini"}},
			match:    routing.Match{Provider: "openai", Endpoint: "chat_completions"},
			want:     "gpt-4o-mini",
		},
		{
			name:     "openai responses body model",
			captured: bodycapture.Captured{Kind: bodycapture.KindResponses, Body: &openairesponses.ResponsesRequest{Model: "o1-mini"}},
			match:    routing.Match{Provider: "openai", Endpoint: "responses"},
			want:     "o1-mini",
		},
		{
			name:     "anthropic messages body model",
			captured: bodycapture.Captured{Kind: bodycapture.KindMessages, Body: &messages.MessagesRequest{Model: "claude-haiku-4-5-20251001"}},
			match:    routing.Match{Provider: "anthropic", Endpoint: "messages"},
			want:     "claude-haiku-4-5-20251001",
		},
		{
			name:  "gemini route param wins",
			match: routing.Match{Provider: "gemini", Endpoint: "generate_content", Params: map[string]string{"model": "gemini-2.5-pro"}},
			want:  "gemini-2.5-pro",
		},
		{
			name:  "route param trumps body when both present",
			match: routing.Match{Params: map[string]string{"model": "gemini-2.0-flash"}},
			// A typed body present alongside a route param is non-physical
			// today (only Gemini carries a route model param, and its body
			// type has no Model field), but the precedence is locked
			// regardless to keep the rule unambiguous.
			captured: bodycapture.Captured{Body: &openaichat.ChatCompletionRequest{Model: "should-be-ignored"}},
			want:     "gemini-2.0-flash",
		},
		{
			name:     "passthrough listing endpoint",
			captured: bodycapture.Captured{Kind: bodycapture.KindPassthrough},
			match:    routing.Match{Provider: "openai", Endpoint: "models"},
			want:     "",
		},
		{
			name:     "whitespace trimmed on body",
			captured: bodycapture.Captured{Kind: bodycapture.KindChat, Body: &openaichat.ChatCompletionRequest{Model: "  gpt-4o  "}},
			want:     "gpt-4o",
		},
		{
			name:  "whitespace trimmed on path param",
			match: routing.Match{Params: map[string]string{"model": "  gemini-2.0-flash  "}},
			want:  "gemini-2.0-flash",
		},
		{
			name:     "empty body model returns empty",
			captured: bodycapture.Captured{Kind: bodycapture.KindChat, Body: &openaichat.ChatCompletionRequest{}},
			want:     "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := outboundModel(tc.captured, tc.match)
			if got != tc.want {
				t.Fatalf("outboundModel = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSanitiseModelLabel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty stays empty", "", ""},
		{"whitespace only stays empty", "   ", ""},
		{"well-known openai", "gpt-4o-mini-2024-07-18", "gpt-4o-mini-2024-07-18"},
		{"well-known anthropic", "claude-haiku-4-5-20251001", "claude-haiku-4-5-20251001"},
		{"trims surrounding whitespace", "  gemini-2.0-flash  ", "gemini-2.0-flash"},
		{"caps long input as other", strings.Repeat("x", modelLabelMaxLen+1), modelLabelOther},
		{"exactly at cap survives", strings.Repeat("y", modelLabelMaxLen), strings.Repeat("y", modelLabelMaxLen)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := sanitiseModelLabel(tc.in); got != tc.want {
				t.Fatalf("sanitiseModelLabel(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestGateway_RequestsMetricCarriesModelLabel exercises the full handler
// chain with a real meter SDK and asserts the model attribute is present
// on gateway.requests.total after a chat request. This is the unit-side
// guarantee that the label survives from the typed body through the
// per-request reporter observer to OTel.
func TestGateway_RequestsMetricCarriesModelLabel(t *testing.T) {
	env := newTestEnvWithMeters(t)

	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`
	req := newReq(t, http.MethodPost, env.gatewayURL+"/v1/chat/completions", body)
	req.Header.Set("Authorization", "Bearer sk_dev_local")
	req.Header.Set("Content-Type", "application/json")

	resp := doReq(t, req)
	defer closeBody(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var rm metricdata.ResourceMetrics
	if err := env.reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	requireAttribute(t, &rm, observability.MetricRequestsTotal, "model", "gpt-4o-mini")
	requireAttribute(t, &rm, observability.MetricRequestDuration, "model", "gpt-4o-mini")
}

// TestGateway_RequestsMetricEmptyModelForPassthrough verifies that
// passthrough endpoints (e.g. /v1/models listings) emit the model label
// with an empty value rather than the previous request's value or any
// fallback bucket.
func TestGateway_RequestsMetricEmptyModelForPassthrough(t *testing.T) {
	env := newTestEnvWithMeters(t)

	req := newReq(t, http.MethodGet, env.gatewayURL+"/v1/models", "")
	req.Header.Set("Authorization", "Bearer sk_dev_local")
	resp := doReq(t, req)
	defer closeBody(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var rm metricdata.ResourceMetrics
	if err := env.reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	requireAttribute(t, &rm, observability.MetricRequestsTotal, "model", "")
}

type meterEnv struct {
	*testEnv
	reader *sdkmetric.ManualReader
}

// newTestEnvWithMeters mirrors newTestEnv but wires a manual-reader meter
// provider into the observer so tests can scrape the data points
// synchronously after a request.
func newTestEnvWithMeters(t *testing.T) *meterEnv {
	t.Helper()

	cap := &capturedUpstream{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		cap.mu.Lock()
		cap.method = r.Method
		cap.path = r.URL.Path
		cap.headers = r.Header.Clone()
		cap.body = b
		cap.count++
		cap.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	dir := writeTestConfig(t, upstream.URL)

	ctx, cancel := context.WithCancel(context.Background())
	resolved, err := config.Load(ctx, dir)
	if err != nil {
		upstream.Close()
		cancel()
		t.Fatalf("config.Load: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	router, err := routing.New(resolved)
	if err != nil {
		upstream.Close()
		cancel()
		t.Fatalf("routing.New: %v", err)
	}

	resolver := auth.NewResolver(resolved)

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	meters, err := observability.NewMeters(mp.Meter(observability.MeterName))
	if err != nil {
		upstream.Close()
		cancel()
		t.Fatalf("NewMeters: %v", err)
	}

	reporter := newReporterFactory(nil, logger, meters)
	forwarder := proxy.New(proxy.Options{Logger: logger, ObserverFactory: reporter.Factory()})

	errs := httperr.New(meters.ErrorResponsesTotal, logger)
	dataPlane := buildDataPlaneHandler(router, resolver, forwarder, resolved.Providers, errs, logger)
	root := correlationMiddleware(logger, dataPlane)

	gateway := httptest.NewServer(root)

	te := &testEnv{
		gatewayURL:  gateway.URL,
		upstreamURL: upstream.URL,
		upstream:    cap,
		shutdown: func() {
			gateway.Close()
			upstream.Close()
			cancel()
		},
	}
	t.Cleanup(te.shutdown)
	return &meterEnv{testEnv: te, reader: reader}
}

func requireAttribute(t *testing.T, rm *metricdata.ResourceMetrics, metricName, key, wantValue string) {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != metricName {
				continue
			}
			points := dataPointAttrs(m.Data)
			for _, attrs := range points {
				if got, ok := attrs[key]; ok && got == wantValue {
					return
				}
			}
			t.Fatalf("metric %q: attribute %q=%q not found; data points = %v", metricName, key, wantValue, points)
		}
	}
	t.Fatalf("metric %q not collected", metricName)
}

func dataPointAttrs(data metricdata.Aggregation) []map[string]string {
	var out []map[string]string
	switch d := data.(type) {
	case metricdata.Sum[int64]:
		for _, dp := range d.DataPoints {
			out = append(out, attrSetToMap(dp.Attributes.ToSlice()))
		}
	case metricdata.Sum[float64]:
		for _, dp := range d.DataPoints {
			out = append(out, attrSetToMap(dp.Attributes.ToSlice()))
		}
	case metricdata.Histogram[float64]:
		for _, dp := range d.DataPoints {
			out = append(out, attrSetToMap(dp.Attributes.ToSlice()))
		}
	case metricdata.Histogram[int64]:
		for _, dp := range d.DataPoints {
			out = append(out, attrSetToMap(dp.Attributes.ToSlice()))
		}
	}
	return out
}

func attrSetToMap(kvs []attribute.KeyValue) map[string]string {
	out := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		out[string(kv.Key)] = kv.Value.Emit()
	}
	return out
}
