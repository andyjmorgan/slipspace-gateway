package admin

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

func TestPerSecond_ZeroWindow(t *testing.T) {
	if got := perSecond(100, 0); got != 0 {
		t.Errorf("perSecond on zero window = %f, want 0", got)
	}
	if got := perSecond(100, -time.Second); got != 0 {
		t.Errorf("perSecond on negative window = %f, want 0", got)
	}
}

func TestComputeByModel_RowsSkipMissingModelLabel(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	start := makeSample(t0)
	end := makeSample(t1)
	// Only one row carries the model label — the other should be skipped.
	withModel := []attribute.KeyValue{
		attribute.String(observability.AttrGenAIProviderName, "openai"),
		attribute.String(observability.AttrGenAIRequestModel, "gpt-4o-mini"),
		attribute.String(observability.AttrHTTPResponseStatusCode, "200"),
	}
	withoutModel := []attribute.KeyValue{
		attribute.String(observability.AttrGenAIProviderName, "openai"),
		attribute.String(observability.AttrHTTPResponseStatusCode, "200"),
	}
	setCounter(end, observability.MetricRequestsTotal, withModel, 10)
	setCounter(end, observability.MetricRequestsTotal, withoutModel, 5)

	sum := BuildDashboardSummary(start, end, time.Hour, nil, nil, nil, nil, nil)
	if len(sum.ByModel) != 1 {
		t.Fatalf("len(ByModel) = %d, want 1", len(sum.ByModel))
	}
	if sum.ByModel[0].Model != "gpt-4o-mini" {
		t.Errorf("Model = %q", sum.ByModel[0].Model)
	}
	if sum.ByModel[0].Requests != 10 {
		t.Errorf("Requests = %d, want 10", sum.ByModel[0].Requests)
	}
}

func TestComputeRulesFired_SkipsAbsentName(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	start := makeSample(t0)
	end := makeSample(t1)
	// Rule entry without a name attribute is a label-set bug from
	// upstream — the aggregator must skip it rather than blow up.
	setCounter(end, observability.MetricRuleMatchesTotal, nil, 9)
	setCounter(end, observability.MetricRuleMatchesTotal,
		[]attribute.KeyValue{attribute.String("rule_name", "real-rule")}, 11)

	sum := BuildDashboardSummary(start, end, time.Hour, nil, nil, nil, nil, nil)
	if len(sum.RulesFired) != 1 {
		t.Fatalf("len(RulesFired) = %d, want 1", len(sum.RulesFired))
	}
	if sum.RulesFired[0].RuleName != "real-rule" || sum.RulesFired[0].FireCount != 11 {
		t.Errorf("RulesFired[0] = %+v, want real-rule/11", sum.RulesFired[0])
	}
}

func TestComputeByProvider_StartHistogramShorterThanEnd(t *testing.T) {
	// Defensive case: the start sample has no histogram entry for a
	// label-set the end sample carries (e.g. the series appeared mid-
	// window). subtractHistogram should treat the missing entries as
	// zero.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	start := makeSample(t0)
	end := makeSample(t1)
	bounds := []float64{0.5, 1, 2}
	one := []uint64{0, 1, 0, 0}
	setCounter(end, observability.MetricRequestsTotal,
		[]attribute.KeyValue{attribute.String(observability.AttrGenAIProviderName, "openai"), attribute.String(observability.AttrHTTPResponseStatusCode, "200")}, 1)
	setHistogram(end,
		[]attribute.KeyValue{attribute.String(observability.AttrGenAIProviderName, "openai")}, 1, 1, bounds, one)

	sum := BuildDashboardSummary(start, end, time.Hour, nil, nil, nil, nil, nil)
	if len(sum.ByProvider) != 1 {
		t.Fatalf("len(ByProvider) = %d, want 1", len(sum.ByProvider))
	}
	if sum.ByProvider[0].P95LatencyMs == 0 {
		t.Error("P95LatencyMs = 0; expected the lone observation to be reflected")
	}
}

func TestBuildTimeseries_DispatchesAllNames(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	samples := []observability.Sample{
		makeSample(t0),
		makeSample(t0.Add(time.Minute)),
	}
	setCounter(samples[1], observability.MetricRequestsTotal,
		[]attribute.KeyValue{attribute.String(observability.AttrGenAIProviderName, "openai"), attribute.String(observability.AttrHTTPResponseStatusCode, "200")}, 60)

	if got := buildTimeseries(SeriesRequestsPerSecond, samples); len(got) != 1 {
		t.Errorf("rps dispatch: got %d series, want 1", len(got))
	}
	if got := buildTimeseries(SeriesErrorRate, samples); len(got) != 1 {
		t.Errorf("error_rate dispatch: got %d series, want 1", len(got))
	}
}

func TestComputeByProtocol_PartitionsByProviderProtocolPair(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	start := makeSample(t0)
	end := makeSample(t1)

	rows := []struct {
		attrs []attribute.KeyValue
		v     int64
	}{
		{[]attribute.KeyValue{
			attribute.String(observability.AttrGenAIProviderName, "openai"),
			attribute.String(observability.AttrSluiceProtocol, "chat_completions"),
			attribute.String(observability.AttrHTTPResponseStatusCode, "200"),
		}, 30},
		{[]attribute.KeyValue{
			attribute.String(observability.AttrGenAIProviderName, "openai"),
			attribute.String(observability.AttrSluiceProtocol, "chat_completions"),
			attribute.String(observability.AttrHTTPResponseStatusCode, "500"),
		}, 2},
		{[]attribute.KeyValue{
			attribute.String(observability.AttrGenAIProviderName, "anthropic"),
			attribute.String(observability.AttrSluiceProtocol, "messages"),
			attribute.String(observability.AttrHTTPResponseStatusCode, "200"),
		}, 10},
		// missing protocol label — must be skipped.
		{[]attribute.KeyValue{
			attribute.String(observability.AttrGenAIProviderName, "openai"),
			attribute.String(observability.AttrHTTPResponseStatusCode, "200"),
		}, 99},
	}
	for _, r := range rows {
		setCounter(end, observability.MetricRequestsTotal, r.attrs, r.v)
	}

	sum := BuildDashboardSummary(start, end, time.Hour, nil, nil, nil, nil, nil)
	if len(sum.ByProtocol) != 2 {
		t.Fatalf("len(ByProtocol) = %d, want 2", len(sum.ByProtocol))
	}
	// Sorted by requests desc — openai/chat_completions has 32, anthropic/messages has 10.
	if sum.ByProtocol[0].Provider != "openai" || sum.ByProtocol[0].Protocol != "chat_completions" {
		t.Errorf("ByProtocol[0] = %+v, want openai/chat_completions", sum.ByProtocol[0])
	}
	if sum.ByProtocol[0].Requests != 32 {
		t.Errorf("ByProtocol[0].Requests = %d, want 32", sum.ByProtocol[0].Requests)
	}
	if got := sum.ByProtocol[0].ErrorRate; got <= 0 || got > 0.1 {
		t.Errorf("ByProtocol[0].ErrorRate = %f, want roughly 2/32", got)
	}
	if sum.ByProtocol[1].Provider != "anthropic" || sum.ByProtocol[1].Protocol != "messages" {
		t.Errorf("ByProtocol[1] = %+v, want anthropic/messages", sum.ByProtocol[1])
	}
}

func TestComputeByProtocol_HistogramFolding(t *testing.T) {
	// Exercise the histogram-folding path: end has a (provider, protocol)
	// pair with both a counter row and a histogram. The first histogram
	// observation initialises the per-pair accumulator's Bounds/Counts
	// slices; the second observation accumulates onto them.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	start := makeSample(t0)
	end := makeSample(t1)
	bounds := []float64{0.5, 1, 2}

	attrs200 := []attribute.KeyValue{
		attribute.String(observability.AttrGenAIProviderName, "openai"),
		attribute.String(observability.AttrSluiceProtocol, "chat_completions"),
		attribute.String(observability.AttrHTTPResponseStatusCode, "200"),
	}
	attrs500 := []attribute.KeyValue{
		attribute.String(observability.AttrGenAIProviderName, "openai"),
		attribute.String(observability.AttrSluiceProtocol, "chat_completions"),
		attribute.String(observability.AttrHTTPResponseStatusCode, "500"),
	}
	setCounter(end, observability.MetricRequestsTotal, attrs200, 1)
	setCounter(end, observability.MetricRequestsTotal, attrs500, 1)
	setHistogram(end, attrs200, 0.6, 1, bounds, []uint64{0, 1, 0, 0})
	setHistogram(end, attrs500, 1.8, 1, bounds, []uint64{0, 0, 1, 0})

	sum := BuildDashboardSummary(start, end, time.Hour, nil, nil, nil, nil, nil)
	if len(sum.ByProtocol) != 1 {
		t.Fatalf("len(ByProtocol) = %d, want 1", len(sum.ByProtocol))
	}
	if sum.ByProtocol[0].P95LatencyMs == 0 {
		t.Error("P95LatencyMs = 0; expected folded observations to be reflected")
	}
}

func TestComputeByProtocol_HistogramSkippedWhenNoCounter(t *testing.T) {
	// Defensive: a histogram label-set with no matching counter row in
	// the same window should not produce a phantom protocol row.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	start := makeSample(t0)
	end := makeSample(t1)
	bounds := []float64{0.5, 1, 2}

	histOnly := []attribute.KeyValue{
		attribute.String(observability.AttrGenAIProviderName, "openai"),
		attribute.String(observability.AttrSluiceProtocol, "responses"),
		attribute.String(observability.AttrHTTPResponseStatusCode, "200"),
	}
	setHistogram(end, histOnly, 0.4, 1, bounds, []uint64{1, 0, 0, 0})

	sum := BuildDashboardSummary(start, end, time.Hour, nil, nil, nil, nil, nil)
	if len(sum.ByProtocol) != 0 {
		t.Fatalf("len(ByProtocol) = %d, want 0 (no counter row)", len(sum.ByProtocol))
	}
}

func TestComputeByConfiguration_HistogramSkippedWhenNoCounter(t *testing.T) {
	// Mirror of TestComputeByProtocol_HistogramSkippedWhenNoCounter for
	// the configuration partition: histogram-only series must not
	// produce a phantom configuration row.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	start := makeSample(t0)
	end := makeSample(t1)
	bounds := []float64{0.5, 1, 2}

	histOnly := []attribute.KeyValue{
		attribute.String(observability.AttrGenAIProviderName, "openai"),
		attribute.String(observability.AttrSluiceConfiguration, "production"),
		attribute.String(observability.AttrHTTPResponseStatusCode, "200"),
	}
	setHistogram(end, histOnly, 0.4, 1, bounds, []uint64{1, 0, 0, 0})

	sum := BuildDashboardSummary(start, end, time.Hour, nil, nil, nil, nil, nil)
	if len(sum.ByConfiguration) != 0 {
		t.Fatalf("len(ByConfiguration) = %d, want 0 (no counter row)", len(sum.ByConfiguration))
	}
}

func TestComputeByConfiguration_SkipsMissingConfigurationLabel(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	start := makeSample(t0)
	end := makeSample(t1)

	withConfig := []attribute.KeyValue{
		attribute.String(observability.AttrGenAIProviderName, "openai"),
		attribute.String(observability.AttrSluiceConfiguration, "production"),
		attribute.String(observability.AttrHTTPResponseStatusCode, "200"),
	}
	withoutConfig := []attribute.KeyValue{
		attribute.String(observability.AttrGenAIProviderName, "openai"),
		attribute.String(observability.AttrHTTPResponseStatusCode, "200"),
	}
	setCounter(end, observability.MetricRequestsTotal, withConfig, 17)
	setCounter(end, observability.MetricRequestsTotal, withoutConfig, 4)

	sum := BuildDashboardSummary(start, end, time.Hour, nil, nil, nil, nil, nil)
	if len(sum.ByConfiguration) != 1 {
		t.Fatalf("len(ByConfiguration) = %d, want 1", len(sum.ByConfiguration))
	}
	if sum.ByConfiguration[0].Configuration != "production" {
		t.Errorf("Configuration = %q, want production", sum.ByConfiguration[0].Configuration)
	}
	if sum.ByConfiguration[0].Requests != 17 {
		t.Errorf("Requests = %d, want 17", sum.ByConfiguration[0].Requests)
	}
}

func TestComputeByConfiguration_HistogramFolding(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	start := makeSample(t0)
	end := makeSample(t1)

	bounds := []float64{0.5, 1, 2}
	attrs := []attribute.KeyValue{
		attribute.String(observability.AttrGenAIProviderName, "openai"),
		attribute.String(observability.AttrSluiceConfiguration, "production"),
		attribute.String(observability.AttrHTTPResponseStatusCode, "200"),
	}
	setCounter(end, observability.MetricRequestsTotal, attrs, 1)
	setHistogram(end, attrs, 0.75, 1, bounds, []uint64{0, 1, 0, 0})

	sum := BuildDashboardSummary(start, end, time.Hour, nil, nil, nil, nil, nil)
	if len(sum.ByConfiguration) != 1 {
		t.Fatalf("len(ByConfiguration) = %d", len(sum.ByConfiguration))
	}
	if sum.ByConfiguration[0].P95LatencyMs == 0 {
		t.Error("P95LatencyMs = 0; expected the lone observation to be reflected")
	}
}

func TestRpsSeries_DropsZeroOrNegativeIntervals(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Two samples at the same timestamp — interval seconds = 0.
	samples := []observability.Sample{
		makeSample(t0),
		makeSample(t0),
	}
	setCounter(samples[1], observability.MetricRequestsTotal,
		[]attribute.KeyValue{attribute.String(observability.AttrGenAIProviderName, "openai")}, 1)
	out := rpsSeries(samples)
	if len(out.Points) != 0 {
		t.Errorf("expected zero points for zero-interval samples, got %d", len(out.Points))
	}
}
