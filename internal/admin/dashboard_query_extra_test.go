package admin

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
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
			attribute.String(observability.AttrSlipSpaceProtocol, "chat_completions"),
			attribute.String(observability.AttrHTTPResponseStatusCode, "200"),
		}, 30},
		{[]attribute.KeyValue{
			attribute.String(observability.AttrGenAIProviderName, "openai"),
			attribute.String(observability.AttrSlipSpaceProtocol, "chat_completions"),
			attribute.String(observability.AttrHTTPResponseStatusCode, "500"),
		}, 2},
		{[]attribute.KeyValue{
			attribute.String(observability.AttrGenAIProviderName, "anthropic"),
			attribute.String(observability.AttrSlipSpaceProtocol, "messages"),
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

func TestComputeByConfiguration_SkipsMissingConfigurationLabel(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	start := makeSample(t0)
	end := makeSample(t1)

	withConfig := []attribute.KeyValue{
		attribute.String(observability.AttrGenAIProviderName, "openai"),
		attribute.String(observability.AttrSlipSpaceConfiguration, "production"),
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

func TestComputeTagsFired_GroupsAndJoinsConfigs(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	start := makeSample(t0)
	end := makeSample(t1)

	// Two tags plus a delta with no tag label (must be skipped). "agent:x" has a
	// configuration attachment; "unattached" has none (nil -> []).
	setCounter(end, observability.MetricTagsAppliedTotal,
		[]attribute.KeyValue{attribute.String("tag", "agent:x")}, 7)
	setCounter(end, observability.MetricTagsAppliedTotal,
		[]attribute.KeyValue{attribute.String("tag", "unattached")}, 2)
	setCounter(end, observability.MetricTagsAppliedTotal,
		[]attribute.KeyValue{attribute.String(observability.AttrGenAIProviderName, "openai")}, 99)

	sum := BuildDashboardSummary(start, end, time.Hour, nil, nil,
		map[string][]string{"agent:x": {"prod"}}, nil, nil)

	if len(sum.TagsFired) != 2 {
		t.Fatalf("len(TagsFired) = %d, want 2 (no-tag delta skipped)", len(sum.TagsFired))
	}
	// Sorted by apply count desc: agent:x (7) then unattached (2).
	if sum.TagsFired[0].Tag != "agent:x" || sum.TagsFired[0].ApplyCount != 7 {
		t.Errorf("TagsFired[0] = %+v, want agent:x/7", sum.TagsFired[0])
	}
	if len(sum.TagsFired[0].UsedByConfigurations) != 1 || sum.TagsFired[0].UsedByConfigurations[0] != "prod" {
		t.Errorf("agent:x configs = %v, want [prod]", sum.TagsFired[0].UsedByConfigurations)
	}
	if sum.TagsFired[1].Tag != "unattached" || len(sum.TagsFired[1].UsedByConfigurations) != 0 {
		t.Errorf("TagsFired[1] = %+v, want unattached with empty configs", sum.TagsFired[1])
	}
}

func TestComputeByConfiguration_ErrorBranch(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	start := makeSample(t0)
	end := makeSample(t1)

	cfg := func(status string) []attribute.KeyValue {
		return []attribute.KeyValue{
			attribute.String(observability.AttrSlipSpaceConfiguration, "prod"),
			attribute.String(observability.AttrHTTPResponseStatusCode, status),
		}
	}
	setCounter(end, observability.MetricRequestsTotal, cfg("200"), 8)
	setCounter(end, observability.MetricRequestsTotal, cfg("500"), 2) // exercises the 5xx error branch

	sum := BuildDashboardSummary(start, end, time.Hour, nil, nil, nil, nil, nil)
	if len(sum.ByConfiguration) != 1 {
		t.Fatalf("len(ByConfiguration) = %d, want 1", len(sum.ByConfiguration))
	}
	row := sum.ByConfiguration[0]
	if row.Requests != 10 || row.ErrorRate < 0.19 || row.ErrorRate > 0.21 {
		t.Errorf("ByConfiguration[0] = %+v, want requests=10 errorRate~0.2", row)
	}
}

// TestQuantile covers the histogram quantile interpolation edge cases that the
// gateway timeseries p95-by-provider series depends on: empty histogram,
// leftmost bucket, +Inf clamp, and interior interpolation.
func TestQuantile(t *testing.T) {
	if quantile(observability.HistogramSnapshot{}, 0.95) != 0 {
		t.Error("empty histogram -> 0")
	}
	bounds := []float64{0.5, 1, 2}
	// All mass in the leftmost (-Inf, 0.5] bucket -> returns Bounds[0].
	left := observability.HistogramSnapshot{Count: 1, Bounds: bounds, Counts: []uint64{1, 0, 0, 0}}
	if got := quantile(left, 0.95); got != 0.5 {
		t.Errorf("leftmost bucket = %v, want 0.5", got)
	}
	// All mass in the +Inf bucket -> clamps to the max bound.
	inf := observability.HistogramSnapshot{Count: 1, Bounds: bounds, Counts: []uint64{0, 0, 0, 1}}
	if got := quantile(inf, 0.95); got != 2 {
		t.Errorf("+Inf bucket = %v, want 2 (clamp)", got)
	}
	// Interior bucket interpolation: 2 obs in (0.5,1], target rank 1 -> lands at 0.5.
	mid := observability.HistogramSnapshot{Count: 2, Bounds: bounds, Counts: []uint64{0, 2, 0, 0}}
	if got := quantile(mid, 0.5); got < 0.5 || got > 1 {
		t.Errorf("interior bucket = %v, want within (0.5,1]", got)
	}
}

// TestSubtractHistogram covers the element-wise delta, including the defensive
// branch where the start sample has fewer bucket counts than the end (a series
// that appeared mid-window).
func TestSubtractHistogram(t *testing.T) {
	bounds := []float64{0.5, 1}
	start := observability.HistogramSnapshot{Sum: 1, Count: 2, Bounds: bounds, Counts: []uint64{1}} // shorter
	end := observability.HistogramSnapshot{Sum: 5, Count: 6, Bounds: bounds, Counts: []uint64{3, 1, 0}}
	d := subtractHistogram(start, end)
	if d.Sum != 4 || d.Count != 4 {
		t.Errorf("delta sum/count = %v/%v, want 4/4", d.Sum, d.Count)
	}
	if len(d.Counts) != 3 || d.Counts[0] != 2 || d.Counts[1] != 1 || d.Counts[2] != 0 {
		t.Errorf("delta counts = %v, want [2 1 0]", d.Counts)
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
