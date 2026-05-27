package observability_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

func TestNewMeters_NilMeterReturnsError(t *testing.T) {
	t.Parallel()

	if _, err := observability.NewMeters(nil); err == nil {
		t.Fatalf("expected error for nil meter")
	}
}

func TestNewMeters_RegistersAllInstruments(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		_ = mp.Shutdown(context.Background())
	})

	meters, err := observability.NewMeters(mp.Meter(observability.MeterName))
	if err != nil {
		t.Fatalf("NewMeters: %v", err)
	}

	ctx := context.Background()
	meters.RequestsTotal.Add(ctx, 1)
	meters.TokenUsage.Record(ctx, 42, metric.WithAttributes(attribute.String(observability.AttrGenAITokenType, observability.TokenTypeInput)))
	meters.TokenUsage.Record(ctx, 21, metric.WithAttributes(attribute.String(observability.AttrGenAITokenType, observability.TokenTypeOutput)))
	meters.TokensCachedTotal.Add(ctx, 7)
	meters.TokensCacheCreationTotal.Add(ctx, 5)
	meters.TagsAppliedTotal.Add(ctx, 2)
	meters.UnmappedFieldsTotal.Add(ctx, 3)
	meters.ConfigReloadTotal.Add(ctx, 1)
	meters.UpstreamErrorsTotal.Add(ctx, 2)
	meters.ErrorResponsesTotal.Add(ctx, 1)
	meters.RequestDuration.Record(ctx, 0.42)
	meters.TimeToFirstChunk.Record(ctx, 0.075)
	meters.ActiveRequests.Add(ctx, 1)
	meters.ActiveRequests.Add(ctx, -1)
	meters.RuleMatchesTotal.Add(ctx, 1)
	meters.RuleErrorsTotal.Add(ctx, 1)
	meters.RuleEvaluationDuration.Record(ctx, 0.0007)
	meters.AdminRequestsTotal.Add(ctx, 1)
	meters.ResilienceAttemptsTotal.Add(ctx, 1)
	meters.ResilienceAttemptDuration.Record(ctx, 0.05)
	meters.ResilienceAttemptsPerRequest.Record(ctx, 2)
	meters.ResilienceOutcomeTotal.Add(ctx, 1)
	meters.CircuitBreakerTransitionTotal.Add(ctx, 1)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	got := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			got[m.Name] = true
		}
	}

	want := []string{
		observability.MetricRequestsTotal,
		observability.MetricTokenUsage,
		observability.MetricTokensCachedTotal,
		observability.MetricTokensCacheCreationTotal,
		observability.MetricTagsAppliedTotal,
		observability.MetricUnmappedFieldsTotal,
		observability.MetricConfigReloadTotal,
		observability.MetricUpstreamErrorsTotal,
		observability.MetricErrorResponsesTotal,
		observability.MetricRequestDuration,
		observability.MetricTimeToFirstChunk,
		observability.MetricActiveRequests,
		observability.MetricRuleMatchesTotal,
		observability.MetricRuleErrorsTotal,
		observability.MetricRuleEvaluationDuration,
		observability.MetricAdminRequestsTotal,
		observability.MetricResilienceAttemptsTotal,
		observability.MetricResilienceAttemptDuration,
		observability.MetricResilienceAttemptsPerRequest,
		observability.MetricResilienceOutcomeTotal,
		observability.MetricCircuitBreakerTransitionTotal,
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("missing metric %q in collected output", name)
		}
	}
}

// stubCBSource satisfies CircuitBreakerStateSource so the gauge
// callback has something to read at collection time.
type stubCBSource struct {
	rows []observability.CircuitBreakerSnapshot
}

func (s stubCBSource) Snapshot() []observability.CircuitBreakerSnapshot { return s.rows }

func TestRegisterCircuitBreakerStateGauge_ReportsRows(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	src := stubCBSource{rows: []observability.CircuitBreakerSnapshot{
		{Policy: "p1", Target: "t1", State: 0, StateName: "closed"},
		{Policy: "p1", Target: "t2", State: 1, StateName: "open"},
	}}

	meter := mp.Meter(observability.MeterName)
	if err := observability.RegisterCircuitBreakerStateGauge(meter, src, "test-pod"); err != nil {
		t.Fatalf("RegisterCircuitBreakerStateGauge: %v", err)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	var seen []observability.CircuitBreakerSnapshot
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != observability.MetricCircuitBreakerState {
				continue
			}
			gauge, ok := m.Data.(metricdata.Gauge[int64])
			if !ok {
				t.Fatalf("%s: expected gauge[int64], got %T", m.Name, m.Data)
			}
			for _, dp := range gauge.DataPoints {
				row := observability.CircuitBreakerSnapshot{State: dp.Value}
				if v, ok := dp.Attributes.Value("policy"); ok {
					row.Policy = v.AsString()
				}
				if v, ok := dp.Attributes.Value("target"); ok {
					row.Target = v.AsString()
				}
				if v, ok := dp.Attributes.Value("state_name"); ok {
					row.StateName = v.AsString()
				}
				if v, ok := dp.Attributes.Value("pod"); ok && v.AsString() != "test-pod" {
					t.Errorf("pod label = %q; want test-pod", v.AsString())
				}
				seen = append(seen, row)
			}
		}
	}

	if len(seen) != 2 {
		t.Fatalf("collected gauge rows = %d; want 2", len(seen))
	}
}

func TestRegisterCircuitBreakerStateGauge_NilSourceNoOp(t *testing.T) {
	t.Parallel()
	mp := sdkmetric.NewMeterProvider()
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	if err := observability.RegisterCircuitBreakerStateGauge(mp.Meter(observability.MeterName), nil, "pod"); err != nil {
		t.Errorf("nil source should be a no-op, got error: %v", err)
	}
}

func TestRegisterCircuitBreakerStateGauge_NilMeterErrors(t *testing.T) {
	t.Parallel()
	src := stubCBSource{rows: nil}
	if err := observability.RegisterCircuitBreakerStateGauge(nil, src, "pod"); err == nil {
		t.Errorf("expected error for nil meter")
	}
}

// failingMeter is a metric.Meter implementation that returns an error
// from Int64ObservableGauge so the wrap branch in
// RegisterCircuitBreakerStateGauge is exercised in tests.
type failingMeter struct {
	noop.Meter
}

func (failingMeter) Int64ObservableGauge(_ string, _ ...metric.Int64ObservableGaugeOption) (metric.Int64ObservableGauge, error) {
	return nil, errors.New("synthetic register failure")
}

func TestRegisterCircuitBreakerStateGauge_RegisterErrorIsWrapped(t *testing.T) {
	t.Parallel()
	src := stubCBSource{rows: nil}
	err := observability.RegisterCircuitBreakerStateGauge(failingMeter{}, src, "pod")
	if err == nil {
		t.Fatalf("expected error from failing meter")
	}
	if got := err.Error(); got == "" || !strings.Contains(got, observability.MetricCircuitBreakerState) {
		t.Errorf("error %q should mention %q", got, observability.MetricCircuitBreakerState)
	}
}

// TestMeters_RequestsTotalCarriesModelAttribute proves the
// sluice.requests.total instrument accepts and surfaces the
// gen_ai.request.model attribute. The attribute itself is applied by the
// cmd/gateway reporter; this test exists per the issue-#4 acceptance
// check so a meter-level regression (e.g. instrument swapped for one with
// attribute filtering) fails here rather than at the dashboard.
func TestMeters_RequestsTotalCarriesModelAttribute(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	meters, err := observability.NewMeters(mp.Meter(observability.MeterName))
	if err != nil {
		t.Fatalf("NewMeters: %v", err)
	}

	ctx := context.Background()
	meters.RequestsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String(observability.AttrGenAIProviderName, "openai"),
		attribute.String(observability.AttrSluiceEndpoint, "chat_completions"),
		attribute.String(observability.AttrGenAIRequestModel, "gpt-4o-mini"),
		attribute.Int(observability.AttrHTTPResponseStatusCode, 200),
	))

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != observability.MetricRequestsTotal {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("%s: expected int64 sum, got %T", m.Name, m.Data)
			}
			if len(sum.DataPoints) == 0 {
				t.Fatalf("%s: no data points", m.Name)
			}
			got, ok := sum.DataPoints[0].Attributes.Value(observability.AttrGenAIRequestModel)
			if !ok || got.AsString() != "gpt-4o-mini" {
				t.Fatalf("%s: %s attribute = %q, ok=%v; want %q", m.Name, observability.AttrGenAIRequestModel, got.AsString(), ok, "gpt-4o-mini")
			}
			return
		}
	}
	t.Fatalf("%s not collected", observability.MetricRequestsTotal)
}

func TestNewMeters_HistogramBoundariesMatchSpec(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		_ = mp.Shutdown(context.Background())
	})

	meters, err := observability.NewMeters(mp.Meter(observability.MeterName))
	if err != nil {
		t.Fatalf("NewMeters: %v", err)
	}

	ctx := context.Background()
	meters.RequestDuration.Record(ctx, 0.5)
	meters.TimeToFirstChunk.Record(ctx, 0.05)
	meters.RuleEvaluationDuration.Record(ctx, 0.002)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	checks := map[string][]float64{
		observability.MetricRequestDuration:        observability.RequestDurationBuckets,
		observability.MetricTimeToFirstChunk:       observability.TimeToFirstChunkBuckets,
		observability.MetricRuleEvaluationDuration: observability.RuleEvaluationDurationBuckets,
	}

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if want, ok := checks[m.Name]; ok {
				h, ok := m.Data.(metricdata.Histogram[float64])
				if !ok {
					t.Fatalf("%s: expected float64 histogram, got %T", m.Name, m.Data)
				}
				if len(h.DataPoints) == 0 {
					t.Fatalf("%s: no data points", m.Name)
				}
				if !floatSlicesEqual(h.DataPoints[0].Bounds, want) {
					t.Errorf("%s: bounds = %v, want %v", m.Name, h.DataPoints[0].Bounds, want)
				}
			}
		}
	}
}

// failMeter embeds noop.Meter to satisfy metric.Meter and overrides the
// constructor methods NewMeters touches. The fail*At knobs trip exactly
// one construction call so each error branch can be exercised in
// isolation.
type failMeter struct {
	noop.Meter

	failInt64CounterAt int
	int64CounterCalls  int

	failFloat64HistAt int
	float64HistCalls  int

	failInt64HistAt int
	int64HistCalls  int

	failInt64UpDown bool
}

var errInjected = errors.New("injected")

func (f *failMeter) Int64Counter(name string, opts ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	f.int64CounterCalls++
	if f.failInt64CounterAt > 0 && f.int64CounterCalls == f.failInt64CounterAt {
		return nil, errInjected
	}
	return f.Meter.Int64Counter(name, opts...)
}

func (f *failMeter) Float64Histogram(name string, opts ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	f.float64HistCalls++
	if f.failFloat64HistAt > 0 && f.float64HistCalls == f.failFloat64HistAt {
		return nil, errInjected
	}
	return f.Meter.Float64Histogram(name, opts...)
}

func (f *failMeter) Int64UpDownCounter(name string, opts ...metric.Int64UpDownCounterOption) (metric.Int64UpDownCounter, error) {
	if f.failInt64UpDown {
		return nil, errInjected
	}
	return f.Meter.Int64UpDownCounter(name, opts...)
}

func (f *failMeter) Int64Histogram(name string, opts ...metric.Int64HistogramOption) (metric.Int64Histogram, error) {
	f.int64HistCalls++
	if f.failInt64HistAt > 0 && f.int64HistCalls == f.failInt64HistAt {
		return nil, errInjected
	}
	return f.Meter.Int64Histogram(name, opts...)
}

func TestNewMeters_PropagatesConstructionErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		meter metric.Meter
	}{
		{"counter", &failMeter{failInt64CounterAt: 1}},
		{"request_duration", &failMeter{failFloat64HistAt: 1}},
		{"ttfb", &failMeter{failFloat64HistAt: 2}},
		{"token_usage", &failMeter{failInt64HistAt: 1}},
		{"active_requests", &failMeter{failInt64UpDown: true}},
		{"rule_eval_duration", &failMeter{failFloat64HistAt: 3}},
		{"attempts_per_request", &failMeter{failInt64HistAt: 2}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := observability.NewMeters(tc.meter); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func floatSlicesEqual(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
