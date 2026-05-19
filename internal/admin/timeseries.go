package admin

import (
	"net/http"
	"strings"
	"time"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

// Supported series names for /api/v1/dashboard/timeseries?series=...
const (
	SeriesRequestsPerSecond = "rps"
	SeriesErrorRate         = "error_rate"
	SeriesP95ByProvider     = "p95_by_provider"
)

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
		writeJSON(w, http.StatusOK, resp)
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
	default:
		return []adminc.DashboardSeries{}
	}
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
