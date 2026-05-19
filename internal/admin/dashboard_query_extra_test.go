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
		attribute.String("provider", "openai"),
		attribute.String("model", "gpt-4o-mini"),
		attribute.String("status_code", "200"),
	}
	withoutModel := []attribute.KeyValue{
		attribute.String("provider", "openai"),
		attribute.String("status_code", "200"),
	}
	setCounter(end, observability.MetricRequestsTotal, withModel, 10)
	setCounter(end, observability.MetricRequestsTotal, withoutModel, 5)

	sum := BuildDashboardSummary(start, end, time.Hour, nil, nil, nil, nil)
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

	sum := BuildDashboardSummary(start, end, time.Hour, nil, nil, nil, nil)
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
		[]attribute.KeyValue{attribute.String("provider", "openai"), attribute.String("status_code", "200")}, 1)
	setHistogram(end,
		[]attribute.KeyValue{attribute.String("provider", "openai")}, 1, 1, bounds, one)

	sum := BuildDashboardSummary(start, end, time.Hour, nil, nil, nil, nil)
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
		[]attribute.KeyValue{attribute.String("provider", "openai"), attribute.String("status_code", "200")}, 60)

	if got := buildTimeseries(SeriesRequestsPerSecond, samples); len(got) != 1 {
		t.Errorf("rps dispatch: got %d series, want 1", len(got))
	}
	if got := buildTimeseries(SeriesErrorRate, samples); len(got) != 1 {
		t.Errorf("error_rate dispatch: got %d series, want 1", len(got))
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
		[]attribute.KeyValue{attribute.String("provider", "openai")}, 1)
	out := rpsSeries(samples)
	if len(out.Points) != 0 {
		t.Errorf("expected zero points for zero-interval samples, got %d", len(out.Points))
	}
}
