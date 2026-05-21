package observability_test

import (
	"context"
	"errors"
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
	meters.TokensInputTotal.Add(ctx, 42)
	meters.TokensOutputTotal.Add(ctx, 21)
	meters.TokensCachedTotal.Add(ctx, 7)
	meters.TokensCacheCreationTotal.Add(ctx, 5)
	meters.TagsAppliedTotal.Add(ctx, 2)
	meters.EventsPublishedTotal.Add(ctx, 1)
	meters.EventsDroppedTotal.Add(ctx, 1)
	meters.EventsStashedTotal.Add(ctx, 1)
	meters.UnmappedFieldsTotal.Add(ctx, 3)
	meters.ConfigReloadTotal.Add(ctx, 1)
	meters.UpstreamErrorsTotal.Add(ctx, 2)
	meters.ErrorResponsesTotal.Add(ctx, 1)
	meters.RequestDuration.Record(ctx, 0.42)
	meters.RequestTimeToFirstByte.Record(ctx, 0.075)
	meters.EventsInlineBytes.Record(ctx, 4096)
	meters.ActiveRequests.Add(ctx, 1)
	meters.ActiveRequests.Add(ctx, -1)
	meters.RuleMatchesTotal.Add(ctx, 1)
	meters.RuleErrorsTotal.Add(ctx, 1)
	meters.RuleEvaluationDuration.Record(ctx, 0.0007)
	meters.AdminRequestsTotal.Add(ctx, 1)

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
		observability.MetricTokensInputTotal,
		observability.MetricTokensOutputTotal,
		observability.MetricTokensCachedTotal,
		observability.MetricTokensCacheCreationTotal,
		observability.MetricTagsAppliedTotal,
		observability.MetricEventsPublishedTotal,
		observability.MetricEventsDroppedTotal,
		observability.MetricEventsStashedTotal,
		observability.MetricUnmappedFieldsTotal,
		observability.MetricConfigReloadTotal,
		observability.MetricUpstreamErrorsTotal,
		observability.MetricErrorResponsesTotal,
		observability.MetricRequestDuration,
		observability.MetricRequestTimeToFirstByte,
		observability.MetricEventsInlineBytes,
		observability.MetricActiveRequests,
		observability.MetricRuleMatchesTotal,
		observability.MetricRuleErrorsTotal,
		observability.MetricRuleEvaluationDuration,
		observability.MetricAdminRequestsTotal,
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("missing metric %q in collected output", name)
		}
	}
}

// TestMeters_RequestsTotalCarriesModelAttribute proves the
// gateway.requests.total instrument accepts and surfaces a `model`
// attribute. The attribute itself is applied by the cmd/gateway reporter;
// this test exists per the issue-#4 acceptance check so a meter-level
// regression (e.g. instrument swapped for one with attribute filtering)
// fails here rather than at the dashboard.
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
		attribute.String("provider", "openai"),
		attribute.String("endpoint", "chat_completions"),
		attribute.String("model", "gpt-4o-mini"),
		attribute.Int("status_code", 200),
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
			got, ok := sum.DataPoints[0].Attributes.Value("model")
			if !ok || got.AsString() != "gpt-4o-mini" {
				t.Fatalf("%s: model attribute = %q, ok=%v; want %q", m.Name, got.AsString(), ok, "gpt-4o-mini")
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
	meters.RequestTimeToFirstByte.Record(ctx, 0.05)
	meters.EventsInlineBytes.Record(ctx, 16384)
	meters.RuleEvaluationDuration.Record(ctx, 0.002)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	checks := map[string][]float64{
		observability.MetricRequestDuration:        observability.RequestDurationBuckets,
		observability.MetricRequestTimeToFirstByte: observability.TimeToFirstByteBuckets,
		observability.MetricRuleEvaluationDuration: observability.RuleEvaluationDurationBuckets,
	}

	intChecks := map[string][]float64{
		observability.MetricEventsInlineBytes: observability.InlineBytesBuckets,
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
			if want, ok := intChecks[m.Name]; ok {
				h, ok := m.Data.(metricdata.Histogram[int64])
				if !ok {
					t.Fatalf("%s: expected int64 histogram, got %T", m.Name, m.Data)
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

	failInt64Histogram bool

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

func (f *failMeter) Int64Histogram(name string, opts ...metric.Int64HistogramOption) (metric.Int64Histogram, error) {
	if f.failInt64Histogram {
		return nil, errInjected
	}
	return f.Meter.Int64Histogram(name, opts...)
}

func (f *failMeter) Int64UpDownCounter(name string, opts ...metric.Int64UpDownCounterOption) (metric.Int64UpDownCounter, error) {
	if f.failInt64UpDown {
		return nil, errInjected
	}
	return f.Meter.Int64UpDownCounter(name, opts...)
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
		{"inline_bytes", &failMeter{failInt64Histogram: true}},
		{"active_requests", &failMeter{failInt64UpDown: true}},
		{"rule_eval_duration", &failMeter{failFloat64HistAt: 3}},
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
