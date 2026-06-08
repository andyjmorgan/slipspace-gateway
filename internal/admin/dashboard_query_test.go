package admin

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

// makeSample is a test helper that constructs a Sample with the given
// timestamp and lets the caller add counters/histograms inline.
func makeSample(at time.Time) observability.Sample {
	return observability.EmptySample(at)
}

func setCounter(s observability.Sample, metric string, attrs []attribute.KeyValue, value int64) {
	if s.Counters[metric] == nil {
		s.Counters[metric] = map[observability.LabelKey]int64{}
	}
	s.Counters[metric][observability.EncodeLabels(attrs)] = value
}

// setHistogram seeds the request-duration histogram on a Sample. Count is
// derived from the per-bucket counts (their sum) so callers describe the
// distribution once. Today every test uses the same metric name; if a future
// test needs a different histogram, take metric back as a parameter.
func setHistogram(s observability.Sample, attrs []attribute.KeyValue, sum float64, bounds []float64, counts []uint64) {
	const metric = observability.MetricRequestDuration
	if s.Histograms[metric] == nil {
		s.Histograms[metric] = map[observability.LabelKey]observability.HistogramSnapshot{}
	}
	var count uint64
	for _, c := range counts {
		count += c
	}
	s.Histograms[metric][observability.EncodeLabels(attrs)] = observability.HistogramSnapshot{
		Sum: sum, Count: count, Bounds: bounds, Counts: counts,
	}
}

// setTokenUsage seeds the gen_ai.client.token.usage histogram on a Sample
// for one direction (gen_ai.token.type=input|output). The dashboard reads
// the histogram SUM as the token count, so sum carries the token total;
// count is a single observation, which is all the sum-based reads need.
func setTokenUsage(s observability.Sample, attrs []attribute.KeyValue, tokenType string, sum float64) {
	const metric = observability.MetricTokenUsage
	if s.Histograms[metric] == nil {
		s.Histograms[metric] = map[observability.LabelKey]observability.HistogramSnapshot{}
	}
	full := append(append([]attribute.KeyValue(nil), attrs...), attribute.String(observability.AttrGenAITokenType, tokenType))
	s.Histograms[metric][observability.EncodeLabels(full)] = observability.HistogramSnapshot{
		Sum: sum, Count: 1,
	}
}

func TestBuildDashboardSummary_TotalsAndRates(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)

	start := makeSample(t0)
	end := makeSample(t1)

	openai200 := []attribute.KeyValue{
		attribute.String(observability.AttrGenAIProviderName, "openai"),
		attribute.String(observability.AttrHTTPResponseStatusCode, "200"),
	}
	openai500 := []attribute.KeyValue{
		attribute.String(observability.AttrGenAIProviderName, "openai"),
		attribute.String(observability.AttrHTTPResponseStatusCode, "500"),
	}
	anthropic404 := []attribute.KeyValue{
		attribute.String(observability.AttrGenAIProviderName, "anthropic"),
		attribute.String(observability.AttrHTTPResponseStatusCode, "404"),
	}

	setCounter(start, observability.MetricRequestsTotal, openai200, 10)
	setCounter(end, observability.MetricRequestsTotal, openai200, 3610)  // +3600
	setCounter(end, observability.MetricRequestsTotal, openai500, 200)   // +200
	setCounter(end, observability.MetricRequestsTotal, anthropic404, 50) // +50

	sum := BuildDashboardSummary(start, end, time.Hour, nil, nil, nil, nil, nil)

	if sum.Totals.Requests != 3850 {
		t.Errorf("Totals.Requests = %d, want 3850", sum.Totals.Requests)
	}
	if sum.Totals.RequestsSuccess != 3600 {
		t.Errorf("Totals.RequestsSuccess = %d, want 3600", sum.Totals.RequestsSuccess)
	}
	if sum.Totals.RequestsErrored != 250 {
		t.Errorf("Totals.RequestsErrored = %d, want 250", sum.Totals.RequestsErrored)
	}
	wantRPS := float64(3850) / 3600
	if got := sum.Rates.RequestsPerSecond; absDelta(got, wantRPS) > 0.001 {
		t.Errorf("RequestsPerSecond = %f, want ~%f", got, wantRPS)
	}
	wantErr := float64(250) / float64(3850)
	if got := sum.Rates.ErrorRate; absDelta(got, wantErr) > 0.0001 {
		t.Errorf("ErrorRate = %f, want ~%f", got, wantErr)
	}
}

func TestBuildDashboardSummary_ByProviderSortedDescending(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	start := makeSample(t0)
	end := makeSample(t1)
	bounds := []float64{0.5, 1, 2}
	one := []uint64{0, 1, 0, 0}

	setCounter(end, observability.MetricRequestsTotal, []attribute.KeyValue{attribute.String(observability.AttrGenAIProviderName, "anthropic"), attribute.String(observability.AttrHTTPResponseStatusCode, "200")}, 50)
	setCounter(end, observability.MetricRequestsTotal, []attribute.KeyValue{attribute.String(observability.AttrGenAIProviderName, "openai"), attribute.String(observability.AttrHTTPResponseStatusCode, "200")}, 200)
	setCounter(end, observability.MetricRequestsTotal, []attribute.KeyValue{attribute.String(observability.AttrGenAIProviderName, "openai"), attribute.String(observability.AttrHTTPResponseStatusCode, "500")}, 10)

	setHistogram(end, []attribute.KeyValue{attribute.String(observability.AttrGenAIProviderName, "openai")}, 1, bounds, one)
	setHistogram(end, []attribute.KeyValue{attribute.String(observability.AttrGenAIProviderName, "anthropic")}, 1, bounds, one)

	sum := BuildDashboardSummary(start, end, time.Hour, nil, nil, nil, nil, nil)

	if len(sum.ByProvider) != 2 {
		t.Fatalf("len(ByProvider) = %d, want 2", len(sum.ByProvider))
	}
	if sum.ByProvider[0].Provider != "openai" {
		t.Errorf("first row provider = %q, want openai (highest volume)", sum.ByProvider[0].Provider)
	}
	if sum.ByProvider[0].Requests != 210 {
		t.Errorf("openai requests = %d, want 210", sum.ByProvider[0].Requests)
	}
	wantErrRate := float64(10) / float64(210)
	if got := sum.ByProvider[0].ErrorRate; absDelta(got, wantErrRate) > 0.0001 {
		t.Errorf("openai ErrorRate = %f, want ~%f", got, wantErrRate)
	}
}

func TestBuildDashboardSummary_RulesFiredJoinedToConfigurations(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	start := makeSample(t0)
	end := makeSample(t1)
	setCounter(end, observability.MetricRuleMatchesTotal, []attribute.KeyValue{attribute.String("rule_name", "redact-emails")}, 42)
	setCounter(end, observability.MetricRuleMatchesTotal, []attribute.KeyValue{attribute.String("rule_name", "route-qwen")}, 17)

	attachments := map[string][]string{
		"redact-emails": {"production", "passthrough"},
		"route-qwen":    {"internal-dev"},
	}
	sum := BuildDashboardSummary(start, end, time.Hour, nil, attachments, nil, nil, nil)

	if len(sum.RulesFired) != 2 {
		t.Fatalf("len(RulesFired) = %d, want 2", len(sum.RulesFired))
	}
	if sum.RulesFired[0].RuleName != "redact-emails" || sum.RulesFired[0].FireCount != 42 {
		t.Errorf("top rule = %+v, want redact-emails/42", sum.RulesFired[0])
	}
	if len(sum.RulesFired[0].UsedByConfigurations) != 2 {
		t.Errorf("UsedByConfigurations: got %v, want 2 entries", sum.RulesFired[0].UsedByConfigurations)
	}
}

func TestBuildDashboardSummary_ProviderHealthHonoursFiveMinWindow(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	tFiveMinAgo := t1.Add(-5 * time.Minute)

	start := makeSample(t0)
	end := makeSample(t1)
	fiveStart := makeSample(tFiveMinAgo)
	fiveEnd := makeSample(t1)

	// In the last 5 minutes, openai saw 100 requests, 1 errored — healthy.
	// anthropic saw 50 requests, 10 errored — unhealthy (>5%).
	setCounter(fiveStart, observability.MetricRequestsTotal, []attribute.KeyValue{attribute.String(observability.AttrGenAIProviderName, "openai"), attribute.String(observability.AttrHTTPResponseStatusCode, "200")}, 0)
	setCounter(fiveEnd, observability.MetricRequestsTotal, []attribute.KeyValue{attribute.String(observability.AttrGenAIProviderName, "openai"), attribute.String(observability.AttrHTTPResponseStatusCode, "200")}, 99)
	setCounter(fiveEnd, observability.MetricRequestsTotal, []attribute.KeyValue{attribute.String(observability.AttrGenAIProviderName, "openai"), attribute.String(observability.AttrHTTPResponseStatusCode, "500")}, 1)

	setCounter(fiveStart, observability.MetricRequestsTotal, []attribute.KeyValue{attribute.String(observability.AttrGenAIProviderName, "anthropic"), attribute.String(observability.AttrHTTPResponseStatusCode, "200")}, 0)
	setCounter(fiveEnd, observability.MetricRequestsTotal, []attribute.KeyValue{attribute.String(observability.AttrGenAIProviderName, "anthropic"), attribute.String(observability.AttrHTTPResponseStatusCode, "200")}, 40)
	setCounter(fiveEnd, observability.MetricRequestsTotal, []attribute.KeyValue{attribute.String(observability.AttrGenAIProviderName, "anthropic"), attribute.String(observability.AttrHTTPResponseStatusCode, "500")}, 10)

	sum := BuildDashboardSummary(start, end, time.Hour,
		[]string{"openai", "anthropic", "gemini"}, nil, nil,
		&fiveStart, &fiveEnd,
	)

	if len(sum.ProviderHealth) != 3 {
		t.Fatalf("len(ProviderHealth) = %d, want 3", len(sum.ProviderHealth))
	}
	byName := map[string]int{}
	for i, p := range sum.ProviderHealth {
		byName[p.Provider] = i
	}
	o := sum.ProviderHealth[byName["openai"]]
	a := sum.ProviderHealth[byName["anthropic"]]
	g := sum.ProviderHealth[byName["gemini"]]

	if !o.Healthy {
		t.Errorf("openai Healthy = false; want true (1%% error)")
	}
	if got := o.ErrorRate5m; absDelta(got, 0.01) > 0.0001 {
		t.Errorf("openai ErrorRate5m = %f, want ~0.01", got)
	}
	if a.Healthy {
		t.Errorf("anthropic Healthy = true; want false (20%% error)")
	}
	if got := a.ErrorRate5m; absDelta(got, 0.20) > 0.0001 {
		t.Errorf("anthropic ErrorRate5m = %f, want ~0.20", got)
	}
	if !g.Healthy {
		t.Errorf("gemini (no traffic) Healthy = false; want true")
	}
	if g.ErrorRate5m != 0 {
		t.Errorf("gemini ErrorRate5m = %f, want 0", g.ErrorRate5m)
	}
}

func TestBuildDashboardSummary_ProviderHealthMissingFiveMinFallsBack(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	start := makeSample(t0)
	end := makeSample(t1)

	sum := BuildDashboardSummary(start, end, time.Hour,
		[]string{"openai", "anthropic"}, nil, nil,
		nil, nil,
	)
	if len(sum.ProviderHealth) != 2 {
		t.Fatalf("len(ProviderHealth) = %d, want 2", len(sum.ProviderHealth))
	}
	for _, p := range sum.ProviderHealth {
		if !p.Healthy {
			t.Errorf("%s Healthy = false; want true when 5m window unavailable", p.Provider)
		}
		if p.ErrorRate5m != 0 {
			t.Errorf("%s ErrorRate5m = %f; want 0 when 5m window unavailable", p.Provider, p.ErrorRate5m)
		}
	}
}

func TestFormatWindow(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{24 * time.Hour, "24h"},
		{48 * time.Hour, "2d"},
		{12 * time.Hour, "12h"},
		{time.Hour, "1h"},
		{30 * time.Minute, "30m"},
		{45 * time.Second, "45s"},
	}
	for _, tc := range cases {
		if got := formatWindow(tc.d); got != tc.want {
			t.Errorf("formatWindow(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func absDelta(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}
