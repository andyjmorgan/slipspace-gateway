package admin

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

func TestRpsSeries_DeltaBetweenSamples(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	samples := []observability.Sample{
		makeSample(t0),
		makeSample(t0.Add(60 * time.Second)),
		makeSample(t0.Add(120 * time.Second)),
	}
	openai200 := []attribute.KeyValue{
		attribute.String("provider", "openai"),
		attribute.String("status_code", "200"),
	}
	setCounter(samples[0], observability.MetricRequestsTotal, openai200, 0)
	setCounter(samples[1], observability.MetricRequestsTotal, openai200, 60)  // 60 reqs in 60s = 1 rps
	setCounter(samples[2], observability.MetricRequestsTotal, openai200, 180) // 120 reqs in 60s = 2 rps

	series := rpsSeries(samples)
	if len(series.Points) != 2 {
		t.Fatalf("len(points) = %d, want 2", len(series.Points))
	}
	if got := series.Points[0].Value; absDelta(got, 1.0) > 0.001 {
		t.Errorf("point[0] = %f, want 1.0", got)
	}
	if got := series.Points[1].Value; absDelta(got, 2.0) > 0.001 {
		t.Errorf("point[1] = %f, want 2.0", got)
	}
	if series.Name == "" {
		t.Error("Name empty")
	}
	if series.Unit != "req/s" {
		t.Errorf("Unit = %q, want req/s", series.Unit)
	}
}

func TestErrorRateSeries_PercentageOfErrored(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	samples := []observability.Sample{
		makeSample(t0),
		makeSample(t0.Add(time.Minute)),
	}
	ok := []attribute.KeyValue{
		attribute.String("provider", "openai"),
		attribute.String("status_code", "200"),
	}
	bad := []attribute.KeyValue{
		attribute.String("provider", "openai"),
		attribute.String("status_code", "500"),
	}
	setCounter(samples[1], observability.MetricRequestsTotal, ok, 90)
	setCounter(samples[1], observability.MetricRequestsTotal, bad, 10)

	series := errorRateSeries(samples)
	if len(series.Points) != 1 {
		t.Fatalf("len(points) = %d, want 1", len(series.Points))
	}
	// 10/100 = 10%
	if got := series.Points[0].Value; absDelta(got, 10.0) > 0.0001 {
		t.Errorf("point[0] = %f, want 10.0", got)
	}
}

func TestP95ByProviderSeries_OneSeriesPerProvider(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	samples := []observability.Sample{
		makeSample(t0),
		makeSample(t0.Add(time.Minute)),
	}
	bounds := []float64{0.1, 0.5, 1, 2, 5}
	// One observation in bucket 0 (<=0.1) — p95 of {0.1} is 0.1 → 100ms.
	openaiCounts := []uint64{1, 0, 0, 0, 0, 0}
	anthropicCounts := []uint64{0, 0, 0, 0, 0, 1} // in +Inf bucket → 5000ms (clamped)
	setHistogram(samples[1],
		[]attribute.KeyValue{attribute.String("provider", "openai")}, 0.1, 1, bounds, openaiCounts)
	setHistogram(samples[1],
		[]attribute.KeyValue{attribute.String("provider", "anthropic")}, 10, 1, bounds, anthropicCounts)

	series := p95ByProviderSeries(samples)
	if len(series) != 2 {
		t.Fatalf("len(series) = %d, want 2 (one per provider)", len(series))
	}
	byName := map[string]int{}
	for i, s := range series {
		byName[s.Name] = i
	}
	openai := series[byName["openai"]]
	if openai.Labels["provider"] != "openai" {
		t.Errorf("openai labels = %v", openai.Labels)
	}
	if len(openai.Points) != 1 {
		t.Fatalf("openai points = %d, want 1", len(openai.Points))
	}
	if got := openai.Points[0].Value; got != 100 {
		t.Errorf("openai p95 = %f ms, want 100", got)
	}
	anthropic := series[byName["anthropic"]]
	if got := anthropic.Points[0].Value; got != 5000 {
		t.Errorf("anthropic p95 = %f ms, want 5000 (clamped to max bound)", got)
	}
}

func TestBuildTimeseries_EmptySamplesProducesEmptySeries(t *testing.T) {
	if got := buildTimeseries(SeriesRequestsPerSecond, nil); len(got) != 1 || len(got[0].Points) != 0 {
		t.Errorf("rps empty: got %+v", got)
	}
	if got := buildTimeseries(SeriesP95ByProvider, nil); len(got) != 0 {
		t.Errorf("p95 empty: got %+v", got)
	}
	if got := buildTimeseries("unknown_series", nil); len(got) != 0 {
		t.Errorf("unknown series: got %+v", got)
	}
}
