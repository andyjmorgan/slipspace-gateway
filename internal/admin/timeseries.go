package admin

import (
	"net/http"
	"sort"
	"strings"
	"time"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

// Supported series names for /api/v1/dashboard/timeseries?series=...
const (
	SeriesRequestsPerSecond   = "rps"
	SeriesErrorRate           = "error_rate"
	SeriesP95ByProvider       = "p95_by_provider"
	SeriesTokensPerSecond     = "tokens_per_second" //nolint:gosec // series name (gateway.tokens.*), not a credential
	SeriesRPSByProviderTop5   = "rps_by_provider_top5"
	SeriesErrorRateByProvTop5 = "error_rate_by_provider_top5"
)

// topNProviders is the cap for per-provider time-series panels. Five
// is enough to show the busiest providers without overwhelming the
// chart legend; smaller deployments with fewer providers naturally
// show fewer lines.
const topNProviders = 5

// TimeseriesHandler serves chart data computed off the snapshotter's
// ring of Samples. Each consecutive pair of samples produces one point;
// the value is the per-second rate (counters) or quantile (histograms)
// over the interval between them.
//
// A query with no samples in the ring (process just started) returns
// an empty Series array — the SPA can render an "awaiting data" state.
//
// ?window=1h|24h slices the ring to samples within that window before
// computing. Anything outside the allowlist falls back to the full
// ring contents (effectively 24h at production cadence).
func TimeseriesHandler(snap *observability.Snapshotter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		series := strings.TrimSpace(r.URL.Query().Get("series"))
		window := parseWindow(r.URL.Query().Get("window"), 24*time.Hour)
		var resp adminc.DashboardTimeseries
		if snap != nil {
			samples := samplesInWindow(snap.Samples(), window)
			resp.Series = buildTimeseries(series, samples)
		}
		if resp.Series == nil {
			resp.Series = []adminc.DashboardSeries{}
		}
		writeJSON(w, resp)
	})
}

// samplesInWindow returns the suffix of the samples slice that covers
// the requested window backwards from the most recent sample. When
// the ring has less coverage than asked for, returns the full slice.
func samplesInWindow(samples []observability.Sample, window time.Duration) []observability.Sample {
	if len(samples) == 0 || window <= 0 {
		return samples
	}
	cutoff := samples[len(samples)-1].At.Add(-window)
	startIdx := 0
	for i, s := range samples {
		if !s.At.Before(cutoff) {
			startIdx = i
			break
		}
		startIdx = i
	}
	return samples[startIdx:]
}

// buildTimeseries dispatches on the requested series name.
func buildTimeseries(name string, samples []observability.Sample) []adminc.DashboardSeries {
	switch name {
	case SeriesRequestsPerSecond:
		return []adminc.DashboardSeries{rpsSeries(samples)}
	case SeriesErrorRate:
		return []adminc.DashboardSeries{errorRateSeries(samples)}
	case SeriesP95ByProvider:
		return p95ByProviderSeries(samples)
	case SeriesTokensPerSecond:
		return tokensPerSecondSeries(samples)
	case SeriesRPSByProviderTop5:
		return rpsByProviderTopN(samples, topNProviders)
	case SeriesErrorRateByProvTop5:
		return errorRateByProviderTopN(samples, topNProviders)
	default:
		return []adminc.DashboardSeries{}
	}
}

// tokensPerSecondSeries returns two curves — input and output token
// rate per second — over each snapshot interval. Cached + cache-write
// stay off the chart for now to keep the legend uncluttered; the
// dashboard KPI tiles surface them as standalone totals.
//
// Labels are set so the SPA can colour the two curves distinctly via
// the same colorByLabel mechanism used by p95_by_provider.
func tokensPerSecondSeries(samples []observability.Sample) []adminc.DashboardSeries {
	if len(samples) < 2 {
		return []adminc.DashboardSeries{}
	}
	in := adminc.DashboardSeries{
		Name:   "Tokens in",
		Unit:   "tok/s",
		Labels: map[string]string{"kind": "input"},
		Points: make([]adminc.DashboardPoint, 0, len(samples)),
	}
	out := adminc.DashboardSeries{
		Name:   "Tokens out",
		Unit:   "tok/s",
		Labels: map[string]string{"kind": "output"},
		Points: make([]adminc.DashboardPoint, 0, len(samples)),
	}
	for i := 1; i < len(samples); i++ {
		prev, cur := samples[i-1], samples[i]
		secs := cur.At.Sub(prev.At).Seconds()
		if secs <= 0 {
			continue
		}
		dIn := counterDeltaSum(prev, cur, observability.MetricTokensInputTotal)
		dOut := counterDeltaSum(prev, cur, observability.MetricTokensOutputTotal)
		in.Points = append(in.Points, adminc.DashboardPoint{Timestamp: cur.At, Value: float64(dIn) / secs})
		out.Points = append(out.Points, adminc.DashboardPoint{Timestamp: cur.At, Value: float64(dOut) / secs})
	}
	return []adminc.DashboardSeries{in, out}
}

// counterDeltaSum is the per-interval analog of dashboard_query.go's
// sumCounter — sums (cur - prev) across every label-set under one
// counter metric. Kept in the timeseries package because it walks the
// Sample pair shape the timeseries handler already operates on, rather
// than the (Counters, Histograms) maps the summary handler indexes
// directly.
func counterDeltaSum(prev, cur observability.Sample, metric string) int64 {
	var total int64
	for key, v := range cur.Counters[metric] {
		total += v - prev.Counters[metric][key]
	}
	return total
}

// rpsSeries returns one curve: requests per second across all labels,
// computed as (delta count) / (interval seconds) between successive
// samples.
func rpsSeries(samples []observability.Sample) adminc.DashboardSeries {
	out := adminc.DashboardSeries{
		Name:   "Requests per second",
		Unit:   "req/s",
		Points: make([]adminc.DashboardPoint, 0, len(samples)),
	}
	for i := 1; i < len(samples); i++ {
		prev, cur := samples[i-1], samples[i]
		secs := cur.At.Sub(prev.At).Seconds()
		if secs <= 0 {
			continue
		}
		delta := totalRequestsDelta(prev, cur)
		out.Points = append(out.Points, adminc.DashboardPoint{
			Timestamp: cur.At,
			Value:     float64(delta) / secs,
		})
	}
	return out
}

// errorRateSeries returns one curve: errored / total over each
// interval, expressed as a percentage (0..100).
func errorRateSeries(samples []observability.Sample) adminc.DashboardSeries {
	out := adminc.DashboardSeries{
		Name:   "Error rate",
		Unit:   "%",
		Points: make([]adminc.DashboardPoint, 0, len(samples)),
	}
	for i := 1; i < len(samples); i++ {
		prev, cur := samples[i-1], samples[i]
		total, _, errored := classifyDeltaTotals(prev, cur)
		var pct float64
		if total > 0 {
			pct = (float64(errored) / float64(total)) * 100
		}
		out.Points = append(out.Points, adminc.DashboardPoint{
			Timestamp: cur.At,
			Value:     pct,
		})
	}
	return out
}

// p95ByProviderSeries returns one curve per provider observed in the
// histogram label set. Each point is the p95 latency in ms over the
// interval ending at the point's timestamp.
func p95ByProviderSeries(samples []observability.Sample) []adminc.DashboardSeries {
	if len(samples) < 2 {
		return []adminc.DashboardSeries{}
	}
	// Discover provider labels from the latest sample.
	providers := map[string]struct{}{}
	last := samples[len(samples)-1]
	for key := range last.Histograms[observability.MetricRequestDuration] {
		if p := key.Get("provider"); p != "" {
			providers[p] = struct{}{}
		}
	}
	if len(providers) == 0 {
		return []adminc.DashboardSeries{}
	}

	out := make([]adminc.DashboardSeries, 0, len(providers))
	for provider := range providers {
		series := adminc.DashboardSeries{
			Name:   provider,
			Unit:   "ms",
			Labels: map[string]string{"provider": provider},
			Points: make([]adminc.DashboardPoint, 0, len(samples)),
		}
		for i := 1; i < len(samples); i++ {
			prev, cur := samples[i-1], samples[i]
			hist := providerHistogramDelta(prev, cur, provider)
			q := quantile(hist, 0.95)
			series.Points = append(series.Points, adminc.DashboardPoint{
				Timestamp: cur.At,
				Value:     q * 1000, // seconds → ms for the SPA axis
			})
		}
		out = append(out, series)
	}
	return out
}

// rpsByProviderTopN returns one curve per provider, ranked by total
// requests across the window, capped at n entries. Tiny providers fall
// off the chart so the legend stays readable on a busy gateway.
func rpsByProviderTopN(samples []observability.Sample, n int) []adminc.DashboardSeries {
	if len(samples) < 2 {
		return []adminc.DashboardSeries{}
	}
	totals := totalsByProvider(samples)
	providers := topProvidersByTotal(totals, n)
	if len(providers) == 0 {
		return []adminc.DashboardSeries{}
	}
	out := make([]adminc.DashboardSeries, 0, len(providers))
	for _, provider := range providers {
		series := adminc.DashboardSeries{
			Name:   provider,
			Unit:   "req/s",
			Labels: map[string]string{"provider": provider},
			Points: make([]adminc.DashboardPoint, 0, len(samples)),
		}
		for i := 1; i < len(samples); i++ {
			prev, cur := samples[i-1], samples[i]
			secs := cur.At.Sub(prev.At).Seconds()
			if secs <= 0 {
				continue
			}
			delta := providerRequestsDelta(prev, cur, provider)
			series.Points = append(series.Points, adminc.DashboardPoint{
				Timestamp: cur.At,
				Value:     float64(delta) / secs,
			})
		}
		out = append(out, series)
	}
	return out
}

// errorRateByProviderTopN returns one error-rate curve per provider,
// ranked by total requests across the window, capped at n entries.
// Aggregate error rate is usually dominated by one noisy provider; the
// per-provider split exposes which one without forcing operators to
// pivot through the by-provider table.
func errorRateByProviderTopN(samples []observability.Sample, n int) []adminc.DashboardSeries {
	if len(samples) < 2 {
		return []adminc.DashboardSeries{}
	}
	totals := totalsByProvider(samples)
	providers := topProvidersByTotal(totals, n)
	if len(providers) == 0 {
		return []adminc.DashboardSeries{}
	}
	out := make([]adminc.DashboardSeries, 0, len(providers))
	for _, provider := range providers {
		series := adminc.DashboardSeries{
			Name:   provider,
			Unit:   "%",
			Labels: map[string]string{"provider": provider},
			Points: make([]adminc.DashboardPoint, 0, len(samples)),
		}
		for i := 1; i < len(samples); i++ {
			prev, cur := samples[i-1], samples[i]
			total, errored := providerClassifiedDelta(prev, cur, provider)
			var pct float64
			if total > 0 {
				pct = (float64(errored) / float64(total)) * 100
			}
			series.Points = append(series.Points, adminc.DashboardPoint{
				Timestamp: cur.At,
				Value:     pct,
			})
		}
		out = append(out, series)
	}
	return out
}

// totalsByProvider returns the total request count per provider across
// the window. Used to rank providers for the top-N panels.
func totalsByProvider(samples []observability.Sample) map[string]int64 {
	totals := map[string]int64{}
	if len(samples) < 2 {
		return totals
	}
	first, last := samples[0], samples[len(samples)-1]
	for key, v := range last.Counters[observability.MetricRequestsTotal] {
		provider := key.Get("provider")
		if provider == "" {
			continue
		}
		totals[provider] += v - first.Counters[observability.MetricRequestsTotal][key]
	}
	return totals
}

// topProvidersByTotal returns at most n provider names ordered by
// descending total. Stable secondary sort on name keeps the output
// deterministic when two providers tie.
func topProvidersByTotal(totals map[string]int64, n int) []string {
	type entry struct {
		name  string
		total int64
	}
	entries := make([]entry, 0, len(totals))
	for k, v := range totals {
		if v <= 0 {
			continue
		}
		entries = append(entries, entry{name: k, total: v})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].total != entries[j].total {
			return entries[i].total > entries[j].total
		}
		return entries[i].name < entries[j].name
	})
	if len(entries) > n {
		entries = entries[:n]
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.name)
	}
	return out
}

func providerRequestsDelta(prev, cur observability.Sample, provider string) int64 {
	var delta int64
	for key, v := range cur.Counters[observability.MetricRequestsTotal] {
		if key.Get("provider") != provider {
			continue
		}
		delta += v - prev.Counters[observability.MetricRequestsTotal][key]
	}
	return delta
}

func providerClassifiedDelta(prev, cur observability.Sample, provider string) (total, errored int64) {
	for key, v := range cur.Counters[observability.MetricRequestsTotal] {
		if key.Get("provider") != provider {
			continue
		}
		delta := v - prev.Counters[observability.MetricRequestsTotal][key]
		total += delta
		status := key.Get("status_code")
		if strings.HasPrefix(status, "4") || strings.HasPrefix(status, "5") {
			errored += delta
		}
	}
	return
}

func totalRequestsDelta(prev, cur observability.Sample) int64 {
	var delta int64
	for key, v := range cur.Counters[observability.MetricRequestsTotal] {
		delta += v - prev.Counters[observability.MetricRequestsTotal][key]
	}
	return delta
}

func classifyDeltaTotals(prev, cur observability.Sample) (total, success, errored int64) {
	for key, v := range cur.Counters[observability.MetricRequestsTotal] {
		delta := v - prev.Counters[observability.MetricRequestsTotal][key]
		total += delta
		status := key.Get("status_code")
		if strings.HasPrefix(status, "2") {
			success += delta
		} else if strings.HasPrefix(status, "4") || strings.HasPrefix(status, "5") {
			errored += delta
		}
	}
	return
}

// providerHistogramDelta sums delta histograms across every label-set
// that names provider, producing one merged HistogramSnapshot for that
// provider over the (prev, cur) interval.
func providerHistogramDelta(prev, cur observability.Sample, provider string) observability.HistogramSnapshot {
	var merged observability.HistogramSnapshot
	for key, e := range cur.Histograms[observability.MetricRequestDuration] {
		if key.Get("provider") != provider {
			continue
		}
		s := prev.HistogramValue(observability.MetricRequestDuration, key)
		delta := subtractHistogram(s, e)
		if len(merged.Counts) == 0 {
			merged = observability.HistogramSnapshot{
				Bounds: delta.Bounds,
				Counts: make([]uint64, len(delta.Counts)),
			}
		}
		merged.Sum += delta.Sum
		merged.Count += delta.Count
		for i, c := range delta.Counts {
			merged.Counts[i] += c
		}
	}
	return merged
}
