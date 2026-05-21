package admin

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

// TestComputeByModel_PopulatesTokenColumns proves the per-model token
// joins line up by the shared (provider, model, ...) label key — a row
// only carries non-zero TokensIn/TokensOut when the matching meter saw
// the same model.
func TestComputeByModel_PopulatesTokenColumns(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	start := makeSample(t0)
	end := makeSample(t1)
	openaiAttrs := []attribute.KeyValue{
		attribute.String("provider", "openai"),
		attribute.String("model", "gpt-4o-mini"),
		attribute.String("status_code", "200"),
	}
	anthropicAttrs := []attribute.KeyValue{
		attribute.String("provider", "anthropic"),
		attribute.String("model", "claude-haiku-4-5"),
		attribute.String("status_code", "200"),
	}
	setCounter(end, observability.MetricRequestsTotal, openaiAttrs, 4)
	setCounter(end, observability.MetricRequestsTotal, anthropicAttrs, 6)
	setCounter(end, observability.MetricTokensInputTotal, openaiAttrs, 4_000)
	setCounter(end, observability.MetricTokensOutputTotal, openaiAttrs, 200)
	setCounter(end, observability.MetricTokensInputTotal, anthropicAttrs, 9_000)
	setCounter(end, observability.MetricTokensOutputTotal, anthropicAttrs, 850)

	sum := BuildDashboardSummary(start, end, time.Hour, nil, nil, nil, nil)

	if got := sum.Totals.TokensIn; got != 13_000 {
		t.Errorf("Totals.TokensIn = %d, want 13000", got)
	}
	if got := sum.Totals.TokensOut; got != 1_050 {
		t.Errorf("Totals.TokensOut = %d, want 1050", got)
	}

	byModel := map[string]struct{ in, out int64 }{}
	for _, r := range sum.ByModel {
		byModel[r.Model] = struct{ in, out int64 }{r.TokensIn, r.TokensOut}
	}
	if got := byModel["gpt-4o-mini"]; got.in != 4_000 || got.out != 200 {
		t.Errorf("gpt-4o-mini = %+v, want {4000 200}", got)
	}
	if got := byModel["claude-haiku-4-5"]; got.in != 9_000 || got.out != 850 {
		t.Errorf("claude-haiku-4-5 = %+v, want {9000 850}", got)
	}
}

// TestComputeByModel_TokensWithoutMatchingRequest defends the join when
// a request never shows up on the requests counter but its tokens do —
// e.g. a deltaSample interval that captured the .Add token bump but
// missed the requests bump. The row should still appear so the operator
// sees the token activity.
func TestComputeByModel_TokensWithoutMatchingRequest(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	start := makeSample(t0)
	end := makeSample(t1)
	attrs := []attribute.KeyValue{
		attribute.String("provider", "gemini"),
		attribute.String("model", "gemini-2.5-flash"),
		attribute.String("status_code", "200"),
	}
	setCounter(end, observability.MetricTokensInputTotal, attrs, 500)
	setCounter(end, observability.MetricTokensOutputTotal, attrs, 12)

	sum := BuildDashboardSummary(start, end, time.Hour, nil, nil, nil, nil)
	if len(sum.ByModel) != 1 {
		t.Fatalf("len(ByModel) = %d", len(sum.ByModel))
	}
	row := sum.ByModel[0]
	if row.Model != "gemini-2.5-flash" || row.Requests != 0 || row.TokensIn != 500 || row.TokensOut != 12 {
		t.Errorf("row = %+v, want {gemini-2.5-flash 0 500 12}", row)
	}
}

// TestSumCounter_FoldsAllLabelSets exercises the helper directly to
// cover both the empty-map (early exit) and populated paths.
func TestSumCounter_FoldsAllLabelSets(t *testing.T) {
	if got := sumCounter(nil); got != 0 {
		t.Errorf("sumCounter(nil) = %d, want 0", got)
	}
	m := map[observability.LabelKey]int64{}
	if got := sumCounter(m); got != 0 {
		t.Errorf("sumCounter({}) = %d, want 0", got)
	}
}

// TestTokensPerSecondSeries_TwoCurvesPerSnapshotInterval exercises the
// new timeseries shape: a two-curve response keyed by kind=input/output
// where each curve carries one point per consecutive sample pair.
func TestTokensPerSecondSeries_TwoCurvesPerSnapshotInterval(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(60 * time.Second)
	t2 := t1.Add(60 * time.Second)
	s0 := makeSample(t0)
	s1 := makeSample(t1)
	s2 := makeSample(t2)
	attrs := []attribute.KeyValue{
		attribute.String("provider", "openai"),
		attribute.String("model", "gpt"),
	}
	// s0: zero. s1: +600 in, +60 out. s2: +1200 in, +120 out (cumulative).
	setCounter(s1, observability.MetricTokensInputTotal, attrs, 600)
	setCounter(s1, observability.MetricTokensOutputTotal, attrs, 60)
	setCounter(s2, observability.MetricTokensInputTotal, attrs, 1800)
	setCounter(s2, observability.MetricTokensOutputTotal, attrs, 180)

	got := buildTimeseries(SeriesTokensPerSecond, []observability.Sample{s0, s1, s2})
	if len(got) != 2 {
		t.Fatalf("len(series) = %d, want 2", len(got))
	}
	byKind := map[string][]float64{}
	for _, s := range got {
		kind := s.Labels["kind"]
		for _, p := range s.Points {
			byKind[kind] = append(byKind[kind], p.Value)
		}
	}
	// Each interval is 60s, so the input rate at s1 = 600/60 = 10,
	// and at s2 = (1800-600)/60 = 20.
	wantIn := []float64{10, 20}
	wantOut := []float64{1, 2}
	if !floatEq(byKind["input"], wantIn) {
		t.Errorf("input points = %v, want %v", byKind["input"], wantIn)
	}
	if !floatEq(byKind["output"], wantOut) {
		t.Errorf("output points = %v, want %v", byKind["output"], wantOut)
	}
}

// TestTokensPerSecondSeries_SingleSampleReturnsEmpty covers the
// early-exit branch — one sample alone cannot produce an interval.
func TestTokensPerSecondSeries_SingleSampleReturnsEmpty(t *testing.T) {
	s := makeSample(time.Now())
	if got := buildTimeseries(SeriesTokensPerSecond, []observability.Sample{s}); len(got) != 0 {
		t.Errorf("len(series) = %d, want 0", len(got))
	}
}

// TestTokensPerSecondSeries_SkipsZeroDurationIntervals covers the
// per-sample-pair guard.
func TestTokensPerSecondSeries_SkipsZeroDurationIntervals(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s0 := makeSample(t0)
	s1 := makeSample(t0) // same timestamp -> zero interval
	got := buildTimeseries(SeriesTokensPerSecond, []observability.Sample{s0, s1})
	if len(got) != 2 {
		t.Fatalf("len(series) = %d, want 2", len(got))
	}
	for _, s := range got {
		if len(s.Points) != 0 {
			t.Errorf("%s points = %d, want 0 (interval skipped)", s.Labels["kind"], len(s.Points))
		}
	}
}

func floatEq(a, b []float64) bool {
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
