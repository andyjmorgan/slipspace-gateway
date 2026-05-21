package admin

import (
	"math"
	"sort"
	"strings"
	"time"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

// healthyErrorRate5mCeiling is the threshold above which a provider's
// 5-minute error rate flips it from healthy to unhealthy. The dashboard
// design considers anything above 5% "warn"; we round to 0.05 as the
// boolean cut.
const healthyErrorRate5mCeiling = 0.05

// BuildDashboardSummary computes a DashboardSummary from two Sample
// endpoints. start and end carry cumulative-since-process-start values;
// realised is the wall-clock duration between them.
//
// All math is delta-based: counter values subtract, histogram bucket
// counts subtract before quantile interpolation. Returns a fully-shaped
// response — empty slices when the metric is absent (a process that
// has never seen a request still returns a valid summary).
func BuildDashboardSummary(start, end observability.Sample, realised time.Duration, providers []string, ruleAttachments map[string][]string, fiveMinStart, fiveMinEnd *observability.Sample) adminc.DashboardSummary {
	const requestsMetric = observability.MetricRequestsTotal

	requestDeltas := counterDelta(start, end, requestsMetric)

	totalReqs, successReqs, erroredReqs := classifyTotals(requestDeltas)
	rps := perSecond(totalReqs, realised)
	errRate := safeRatio(erroredReqs, totalReqs)

	tokensInDeltas := counterDelta(start, end, observability.MetricTokensInputTotal)
	tokensOutDeltas := counterDelta(start, end, observability.MetricTokensOutputTotal)
	tokensInTotal := sumCounter(tokensInDeltas)
	tokensOutTotal := sumCounter(tokensOutDeltas)
	tokensCachedTotal := sumCounter(counterDelta(start, end, observability.MetricTokensCachedTotal))
	tokensCacheCreationTotal := sumCounter(counterDelta(start, end, observability.MetricTokensCacheCreationTotal))

	p50, p95, p99 := allLatencyQuantiles(start, end, observability.MetricRequestDuration)

	byProvider := computeByProvider(requestDeltas, start, end)
	byEndpoint := computeByEndpoint(requestDeltas, start, end)
	byConfiguration := computeByConfiguration(requestDeltas, start, end)
	byModel := computeByModel(requestDeltas, tokensInDeltas, tokensOutDeltas)
	rulesFired := computeRulesFired(start, end, ruleAttachments)
	providerHealth := computeProviderHealth(providers, fiveMinStart, fiveMinEnd)

	return adminc.DashboardSummary{
		Window:      formatWindow(realised),
		GeneratedAt: end.At,
		Totals: adminc.DashboardTotals{
			Requests:            totalReqs,
			RequestsSuccess:     successReqs,
			RequestsErrored:     erroredReqs,
			TokensIn:            tokensInTotal,
			TokensOut:           tokensOutTotal,
			TokensCached:        tokensCachedTotal,
			TokensCacheCreation: tokensCacheCreationTotal,
		},
		Rates: adminc.DashboardRates{
			RequestsPerSecond: rps,
			ErrorRate:         errRate,
		},
		LatencyMs: adminc.DashboardLatency{
			// Round to nearest ms — quantile interpolation is
			// float-precision-imperfect (0.95 * 100 stores as
			// 94.999... in IEEE-754, which would otherwise return
			// 1999ms instead of 2000ms for a clean p95-on-boundary).
			P50: int64(math.Round(p50 * 1000)),
			P95: int64(math.Round(p95 * 1000)),
			P99: int64(math.Round(p99 * 1000)),
		},
		ByProvider:      byProvider,
		ByEndpoint:      byEndpoint,
		ByConfiguration: byConfiguration,
		ByModel:         byModel,
		RulesFired:      rulesFired,
		ProviderHealth:  providerHealth,
	}
}

// counterDelta returns end - start per label key for the named counter
// metric. Absent series are treated as 0 on the start side; this lets
// counters that only appeared mid-window still contribute their full
// value.
func counterDelta(start, end observability.Sample, metric string) map[observability.LabelKey]int64 {
	out := map[observability.LabelKey]int64{}
	for key, v := range end.Counters[metric] {
		out[key] = v - start.Counters[metric][key]
	}
	return out
}

// sumCounter folds a counter-delta map into a single grand total.
// Token totals on the dashboard are reported across every label-set
// (provider, endpoint, configuration, model, status_code) without a
// per-bucket breakdown — operators see "X tokens in / Y tokens out
// over the window".
func sumCounter(deltas map[observability.LabelKey]int64) int64 {
	var total int64
	for _, v := range deltas {
		total += v
	}
	return total
}

// classifyTotals sums request counts in the {200-299, 400+} buckets and
// the grand total. Series with status_code outside 2xx/4xx/5xx (1xx, 3xx
// which the gateway shouldn't be emitting) are included in the grand
// total but not in success/errored — they'd show up as the residual.
func classifyTotals(deltas map[observability.LabelKey]int64) (total, success, errored int64) {
	for key, v := range deltas {
		total += v
		status := key.Get("status_code")
		if strings.HasPrefix(status, "2") {
			success += v
		} else if strings.HasPrefix(status, "4") || strings.HasPrefix(status, "5") {
			errored += v
		}
	}
	return
}

func perSecond(count int64, window time.Duration) float64 {
	if window <= 0 {
		return 0
	}
	return float64(count) / window.Seconds()
}

func safeRatio(num, denom int64) float64 {
	if denom == 0 {
		return 0
	}
	return float64(num) / float64(denom)
}

// allLatencyQuantiles sums histogram bucket deltas across all label
// combinations and returns p50/p95/p99 in seconds. Returns zeros when
// the histogram has no observations in the window.
func allLatencyQuantiles(start, end observability.Sample, metric string) (p50, p95, p99 float64) {
	deltas := histogramDelta(start, end, metric)
	merged := mergeHistograms(deltas)
	if merged.Count == 0 {
		return 0, 0, 0
	}
	return quantile(merged, 0.50), quantile(merged, 0.95), quantile(merged, 0.99)
}

func histogramDelta(start, end observability.Sample, metric string) []observability.HistogramSnapshot {
	out := make([]observability.HistogramSnapshot, 0, len(end.Histograms[metric]))
	for key, e := range end.Histograms[metric] {
		s := start.HistogramValue(metric, key)
		out = append(out, subtractHistogram(s, e))
	}
	return out
}

// subtractHistogram computes end - start element-wise. Bounds come from
// end; the SDK guarantees a stable bucket layout across a process
// lifetime so start.Bounds matches end.Bounds when both are populated.
func subtractHistogram(start, end observability.HistogramSnapshot) observability.HistogramSnapshot {
	out := observability.HistogramSnapshot{
		Sum:    end.Sum - start.Sum,
		Count:  end.Count - start.Count,
		Bounds: end.Bounds,
		Counts: make([]uint64, len(end.Counts)),
	}
	for i := range end.Counts {
		var s uint64
		if i < len(start.Counts) {
			s = start.Counts[i]
		}
		out.Counts[i] = end.Counts[i] - s
	}
	return out
}

// mergeHistograms folds a slice of per-label-set delta histograms into
// one aggregated histogram. Caller must ensure all entries share the
// same Bounds — the OTel SDK guarantees this for a single instrument.
func mergeHistograms(hs []observability.HistogramSnapshot) observability.HistogramSnapshot {
	if len(hs) == 0 {
		return observability.HistogramSnapshot{}
	}
	out := observability.HistogramSnapshot{Bounds: hs[0].Bounds, Counts: make([]uint64, len(hs[0].Counts))}
	for _, h := range hs {
		out.Sum += h.Sum
		out.Count += h.Count
		for i, c := range h.Counts {
			out.Counts[i] += c
		}
	}
	return out
}

// quantile interpolates the q-th quantile from a windowed histogram via
// the Prometheus algorithm: locate the bucket containing the target
// rank, then linearly interpolate between the bucket's bounds.
//
// The leftmost bucket (-Inf, Bounds[0]] returns Bounds[0] — the
// histogram doesn't carry sub-bucket-min resolution. The rightmost
// bucket (Bounds[last], +Inf) returns Bounds[last] — an explicit
// underestimate honest about the missing upper bound.
func quantile(h observability.HistogramSnapshot, q float64) float64 {
	if h.Count == 0 || len(h.Bounds) == 0 {
		return 0
	}
	target := q * float64(h.Count)
	var cum uint64
	for i, c := range h.Counts {
		newCum := cum + c
		if float64(newCum) >= target {
			// The Bounds slice is indexed by bucket UPPER bound;
			// bucket i covers (Bounds[i-1], Bounds[i]] except the
			// last which is (Bounds[last], +Inf).
			if i >= len(h.Bounds) {
				// +Inf bucket — return the max bound as a
				// known-underestimate ceiling.
				return h.Bounds[len(h.Bounds)-1]
			}
			if i == 0 {
				return h.Bounds[0]
			}
			lower := h.Bounds[i-1]
			upper := h.Bounds[i]
			frac := (target - float64(cum)) / float64(c)
			return lower + (upper-lower)*frac
		}
		cum = newCum
	}
	return h.Bounds[len(h.Bounds)-1]
}

// computeByProvider partitions request deltas + latency histogram
// deltas by the provider label, producing one row per provider that
// saw traffic.
func computeByProvider(requestDeltas map[observability.LabelKey]int64, start, end observability.Sample) []adminc.DashboardProviderRow {
	type acc struct {
		requests int64
		errored  int64
		hist     observability.HistogramSnapshot
	}
	perProvider := map[string]*acc{}
	for key, v := range requestDeltas {
		p := key.Get("provider")
		if p == "" {
			continue
		}
		a := perProvider[p]
		if a == nil {
			a = &acc{}
			perProvider[p] = a
		}
		a.requests += v
		status := key.Get("status_code")
		if strings.HasPrefix(status, "4") || strings.HasPrefix(status, "5") {
			a.errored += v
		}
	}

	// Latency histograms: group label-keys by provider, then sum.
	for key, eHist := range end.Histograms[observability.MetricRequestDuration] {
		p := key.Get("provider")
		if p == "" {
			continue
		}
		a := perProvider[p]
		if a == nil {
			continue
		}
		s := start.HistogramValue(observability.MetricRequestDuration, key)
		delta := subtractHistogram(s, eHist)
		if len(a.hist.Counts) == 0 {
			a.hist = observability.HistogramSnapshot{
				Bounds: delta.Bounds,
				Counts: make([]uint64, len(delta.Counts)),
			}
		}
		a.hist.Sum += delta.Sum
		a.hist.Count += delta.Count
		for i, c := range delta.Counts {
			a.hist.Counts[i] += c
		}
	}

	out := make([]adminc.DashboardProviderRow, 0, len(perProvider))
	for name, a := range perProvider {
		row := adminc.DashboardProviderRow{
			Provider:     name,
			Requests:     a.requests,
			ErrorRate:    safeRatio(a.errored, a.requests),
			P95LatencyMs: int64(quantile(a.hist, 0.95) * 1000),
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Requests > out[j].Requests })
	return out
}

// computeByEndpoint partitions request deltas + latency histogram
// deltas by the (provider, endpoint) pair. Endpoint is repeated on
// each provider that exposes it (openai.chat_completions vs
// anthropic.chat_completions are distinct rows) so model-keyed
// changeProvider rules don't collapse into a single bucket.
func computeByEndpoint(requestDeltas map[observability.LabelKey]int64, start, end observability.Sample) []adminc.DashboardEndpointRow {
	type acc struct {
		requests int64
		errored  int64
		hist     observability.HistogramSnapshot
	}
	type groupKey struct {
		provider, endpoint string
	}
	perEndpoint := map[groupKey]*acc{}
	for key, v := range requestDeltas {
		gk := groupKey{provider: key.Get("provider"), endpoint: key.Get("endpoint")}
		if gk.provider == "" || gk.endpoint == "" {
			continue
		}
		a := perEndpoint[gk]
		if a == nil {
			a = &acc{}
			perEndpoint[gk] = a
		}
		a.requests += v
		status := key.Get("status_code")
		if strings.HasPrefix(status, "4") || strings.HasPrefix(status, "5") {
			a.errored += v
		}
	}
	for key, eHist := range end.Histograms[observability.MetricRequestDuration] {
		gk := groupKey{provider: key.Get("provider"), endpoint: key.Get("endpoint")}
		a := perEndpoint[gk]
		if a == nil {
			continue
		}
		s := start.HistogramValue(observability.MetricRequestDuration, key)
		delta := subtractHistogram(s, eHist)
		if len(a.hist.Counts) == 0 {
			a.hist = observability.HistogramSnapshot{
				Bounds: delta.Bounds,
				Counts: make([]uint64, len(delta.Counts)),
			}
		}
		a.hist.Sum += delta.Sum
		a.hist.Count += delta.Count
		for i, c := range delta.Counts {
			a.hist.Counts[i] += c
		}
	}
	out := make([]adminc.DashboardEndpointRow, 0, len(perEndpoint))
	for gk, a := range perEndpoint {
		out = append(out, adminc.DashboardEndpointRow{
			Provider:     gk.provider,
			Endpoint:     gk.endpoint,
			Requests:     a.requests,
			ErrorRate:    safeRatio(a.errored, a.requests),
			P95LatencyMs: int64(quantile(a.hist, 0.95) * 1000),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Requests > out[j].Requests })
	return out
}

// computeByConfiguration partitions request deltas + latency histogram
// deltas by the resolved configuration name. Series with no
// configuration attribute (request lifecycle aborted before auth
// resolved one) are dropped.
func computeByConfiguration(requestDeltas map[observability.LabelKey]int64, start, end observability.Sample) []adminc.DashboardConfigurationRow {
	type acc struct {
		requests int64
		errored  int64
		hist     observability.HistogramSnapshot
	}
	perConfig := map[string]*acc{}
	for key, v := range requestDeltas {
		c := key.Get("configuration")
		if c == "" {
			continue
		}
		a := perConfig[c]
		if a == nil {
			a = &acc{}
			perConfig[c] = a
		}
		a.requests += v
		status := key.Get("status_code")
		if strings.HasPrefix(status, "4") || strings.HasPrefix(status, "5") {
			a.errored += v
		}
	}
	for key, eHist := range end.Histograms[observability.MetricRequestDuration] {
		c := key.Get("configuration")
		if c == "" {
			continue
		}
		a := perConfig[c]
		if a == nil {
			continue
		}
		s := start.HistogramValue(observability.MetricRequestDuration, key)
		delta := subtractHistogram(s, eHist)
		if len(a.hist.Counts) == 0 {
			a.hist = observability.HistogramSnapshot{
				Bounds: delta.Bounds,
				Counts: make([]uint64, len(delta.Counts)),
			}
		}
		a.hist.Sum += delta.Sum
		a.hist.Count += delta.Count
		for i, c := range delta.Counts {
			a.hist.Counts[i] += c
		}
	}
	out := make([]adminc.DashboardConfigurationRow, 0, len(perConfig))
	for name, a := range perConfig {
		out = append(out, adminc.DashboardConfigurationRow{
			Configuration: name,
			Requests:      a.requests,
			ErrorRate:     safeRatio(a.errored, a.requests),
			P95LatencyMs:  int64(quantile(a.hist, 0.95) * 1000),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Requests > out[j].Requests })
	return out
}

// computeByModel partitions request deltas by the model label and joins
// per-model token totals on the same key. Token meters carry the same
// model attribute as requests/duration so the deltas line up exactly;
// a model that saw traffic but no upstream usage block (interrupted
// stream) contributes a request count with zero tokens.
func computeByModel(requestDeltas, tokensInDeltas, tokensOutDeltas map[observability.LabelKey]int64) []adminc.DashboardModelRow {
	type acc struct {
		requests  int64
		tokensIn  int64
		tokensOut int64
		provider  string
	}
	perModel := map[string]*acc{}
	get := func(model, provider string) *acc {
		a := perModel[model]
		if a == nil {
			a = &acc{provider: provider}
			perModel[model] = a
		}
		return a
	}
	for key, v := range requestDeltas {
		m := key.Get("model")
		if m == "" {
			continue
		}
		get(m, key.Get("provider")).requests += v
	}
	for key, v := range tokensInDeltas {
		m := key.Get("model")
		if m == "" {
			continue
		}
		get(m, key.Get("provider")).tokensIn += v
	}
	for key, v := range tokensOutDeltas {
		m := key.Get("model")
		if m == "" {
			continue
		}
		get(m, key.Get("provider")).tokensOut += v
	}
	out := make([]adminc.DashboardModelRow, 0, len(perModel))
	for name, a := range perModel {
		out = append(out, adminc.DashboardModelRow{
			Model:     name,
			Provider:  a.provider,
			Requests:  a.requests,
			TokensIn:  a.tokensIn,
			TokensOut: a.tokensOut,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Requests > out[j].Requests })
	return out
}

// computeRulesFired groups gateway.rule.matches.total deltas by rule_name
// and joins against the configuration → rule_names map so each row knows
// which configurations reference it.
func computeRulesFired(start, end observability.Sample, ruleAttachments map[string][]string) []adminc.DashboardRuleFiredRow {
	deltas := counterDelta(start, end, observability.MetricRuleMatchesTotal)
	perRule := map[string]int64{}
	for key, v := range deltas {
		name := key.Get("rule_name")
		if name == "" {
			continue
		}
		perRule[name] += v
	}
	out := make([]adminc.DashboardRuleFiredRow, 0, len(perRule))
	for name, count := range perRule {
		attached := ruleAttachments[name]
		if attached == nil {
			attached = []string{}
		}
		out = append(out, adminc.DashboardRuleFiredRow{
			RuleName:             name,
			FireCount:            count,
			UsedByConfigurations: attached,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FireCount > out[j].FireCount })
	return out
}

// computeProviderHealth derives a 5-minute error rate per provider from
// a second window into the snapshotter. When fiveMinStart/fiveMinEnd
// are nil (insufficient samples) we still return one row per known
// provider, marked healthy with error_rate_5m = 0 — the SPA still
// renders the chips, it just can't colour by error.
//
// Only the {Provider, Healthy, ErrorRate5m} fields are populated.
// consecutive_errors, last_success_at, last_error_at, last_error_message
// require a probe that doesn't exist in v1.1 and are intentionally left
// at their zero value.
func computeProviderHealth(providers []string, start, end *observability.Sample) []adminc.DashboardProviderHealth {
	out := make([]adminc.DashboardProviderHealth, 0, len(providers))
	if start == nil || end == nil {
		for _, p := range providers {
			out = append(out, adminc.DashboardProviderHealth{Provider: p, Healthy: true})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
		return out
	}
	deltas := counterDelta(*start, *end, observability.MetricRequestsTotal)
	type acc struct {
		total, errored int64
	}
	perProvider := map[string]*acc{}
	for _, p := range providers {
		perProvider[p] = &acc{}
	}
	for key, v := range deltas {
		p := key.Get("provider")
		a := perProvider[p]
		if a == nil {
			continue
		}
		a.total += v
		status := key.Get("status_code")
		if strings.HasPrefix(status, "4") || strings.HasPrefix(status, "5") {
			a.errored += v
		}
	}
	for _, p := range providers {
		a := perProvider[p]
		rate := safeRatio(a.errored, a.total)
		out = append(out, adminc.DashboardProviderHealth{
			Provider:    p,
			Healthy:     rate <= healthyErrorRate5mCeiling,
			ErrorRate5m: rate,
			Requests5m:  a.total,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out
}

// formatWindow renders a Duration as the dashboard's window label.
// The SPA renders this verbatim ("Last 24h", "Last 12h", "Last 30m"),
// so the goal is an honest humanised form rather than a forced fit
// against the segmented control's preset values.
func formatWindow(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		days := int(d.Round(time.Hour) / (24 * time.Hour))
		if days > 1 {
			return formatIntUnit(days, "d")
		}
		return "24h"
	case d >= time.Hour:
		hrs := int(d.Round(time.Minute) / time.Hour)
		return formatIntUnit(hrs, "h")
	case d >= time.Minute:
		mins := int(d.Round(time.Minute) / time.Minute)
		return formatIntUnit(mins, "m")
	default:
		secs := int(d.Round(time.Second) / time.Second)
		return formatIntUnit(secs, "s")
	}
}

func formatIntUnit(n int, unit string) string {
	// Trivial helper to avoid the fmt dependency for this hot path.
	if n < 0 {
		n = 0
	}
	return itoa(n) + unit
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
