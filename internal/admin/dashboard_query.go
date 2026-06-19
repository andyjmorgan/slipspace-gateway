package admin

import (
	"sort"
	"strings"
	"time"

	adminc "github.com/andyjmorgan/slipspace-gateway/contracts/admin"
	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
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
func BuildDashboardSummary(start, end observability.Sample, realised time.Duration, providers []string, ruleAttachments, tagAttachments map[string][]string, fiveMinStart, fiveMinEnd *observability.Sample) adminc.DashboardSummary {
	const requestsMetric = observability.MetricRequestsTotal

	requestDeltas := counterDelta(start, end, requestsMetric)

	totalReqs, successReqs, erroredReqs := classifyTotals(requestDeltas)
	rps := perSecond(totalReqs, realised)
	errRate := safeRatio(erroredReqs, totalReqs)

	tokensInDeltas := tokenUsageDelta(start, end, observability.TokenTypeInput)
	tokensOutDeltas := tokenUsageDelta(start, end, observability.TokenTypeOutput)
	tokensInTotal := sumCounter(tokensInDeltas)
	tokensOutTotal := sumCounter(tokensOutDeltas)
	tokensCachedTotal := sumCounter(counterDelta(start, end, observability.MetricTokensCachedTotal))
	tokensCacheCreationTotal := sumCounter(counterDelta(start, end, observability.MetricTokensCacheCreationTotal))

	byProvider := computeByProvider(requestDeltas)
	byProtocol := computeByProtocol(requestDeltas)
	byConfiguration := computeByConfiguration(requestDeltas)
	byModel := computeByModel(requestDeltas, tokensInDeltas, tokensOutDeltas)
	rulesFired := computeRulesFired(start, end, ruleAttachments)
	tagsFired := computeTagsFired(start, end, tagAttachments)
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
		ByProvider:      byProvider,
		ByProtocol:      byProtocol,
		ByConfiguration: byConfiguration,
		ByModel:         byModel,
		RulesFired:      rulesFired,
		TagsFired:       tagsFired,
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

// tokenUsageDelta returns end - start of the gen_ai.client.token.usage
// histogram SUM per label key, restricted to series whose
// gen_ai.token.type matches tokenType. Returns the same map shape as
// counterDelta so the by-model join and grand-total fold consume it
// unchanged — the histogram sum is the token count the former counter
// carried.
func tokenUsageDelta(start, end observability.Sample, tokenType string) map[observability.LabelKey]int64 {
	out := map[observability.LabelKey]int64{}
	for key, e := range end.Histograms[observability.MetricTokenUsage] {
		if key.Get(observability.AttrGenAITokenType) != tokenType {
			continue
		}
		s := start.HistogramValue(observability.MetricTokenUsage, key)
		out[key] = int64(e.Sum - s.Sum)
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
		status := key.Get(observability.AttrHTTPResponseStatusCode)
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

// computeByProvider partitions request deltas by the provider label,
// producing one row per provider that saw traffic. Latency percentiles
// were dropped (MVP: the dashboard contract no longer carries them).
func computeByProvider(requestDeltas map[observability.LabelKey]int64) []adminc.DashboardProviderRow {
	type acc struct {
		requests int64
		errored  int64
	}
	perProvider := map[string]*acc{}
	for key, v := range requestDeltas {
		p := key.Get(observability.AttrGenAIProviderName)
		if p == "" {
			continue
		}
		a := perProvider[p]
		if a == nil {
			a = &acc{}
			perProvider[p] = a
		}
		a.requests += v
		status := key.Get(observability.AttrHTTPResponseStatusCode)
		if strings.HasPrefix(status, "4") || strings.HasPrefix(status, "5") {
			a.errored += v
		}
	}

	out := make([]adminc.DashboardProviderRow, 0, len(perProvider))
	for name, a := range perProvider {
		out = append(out, adminc.DashboardProviderRow{
			Provider:  name,
			Requests:  a.requests,
			ErrorRate: safeRatio(a.errored, a.requests),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Requests > out[j].Requests })
	return out
}

// computeByProtocol partitions request deltas by the (provider,
// protocol) pair. Protocol is repeated on each provider that exposes it
// (openai.chat vs anthropic.chat are distinct rows) so model-keyed
// changeProvider rules don't collapse into a single bucket. Latency
// percentiles were dropped (MVP: the contract no longer carries them).
func computeByProtocol(requestDeltas map[observability.LabelKey]int64) []adminc.DashboardProtocolRow {
	type acc struct {
		requests int64
		errored  int64
	}
	type groupKey struct {
		provider, protocol string
	}
	perProtocol := map[groupKey]*acc{}
	for key, v := range requestDeltas {
		gk := groupKey{provider: key.Get(observability.AttrGenAIProviderName), protocol: key.Get(observability.AttrSlipSpaceProtocol)}
		if gk.provider == "" || gk.protocol == "" {
			continue
		}
		a := perProtocol[gk]
		if a == nil {
			a = &acc{}
			perProtocol[gk] = a
		}
		a.requests += v
		status := key.Get(observability.AttrHTTPResponseStatusCode)
		if strings.HasPrefix(status, "4") || strings.HasPrefix(status, "5") {
			a.errored += v
		}
	}
	out := make([]adminc.DashboardProtocolRow, 0, len(perProtocol))
	for gk, a := range perProtocol {
		out = append(out, adminc.DashboardProtocolRow{
			Provider:  gk.provider,
			Protocol:  gk.protocol,
			Requests:  a.requests,
			ErrorRate: safeRatio(a.errored, a.requests),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Requests > out[j].Requests })
	return out
}

// computeByConfiguration partitions request deltas by the resolved
// configuration name. Series with no configuration attribute (request
// lifecycle aborted before auth resolved one) are dropped. Latency
// percentiles were dropped (MVP: the contract no longer carries them).
func computeByConfiguration(requestDeltas map[observability.LabelKey]int64) []adminc.DashboardConfigurationRow {
	type acc struct {
		requests int64
		errored  int64
	}
	perConfig := map[string]*acc{}
	for key, v := range requestDeltas {
		c := key.Get(observability.AttrSlipSpaceConfiguration)
		if c == "" {
			continue
		}
		a := perConfig[c]
		if a == nil {
			a = &acc{}
			perConfig[c] = a
		}
		a.requests += v
		status := key.Get(observability.AttrHTTPResponseStatusCode)
		if strings.HasPrefix(status, "4") || strings.HasPrefix(status, "5") {
			a.errored += v
		}
	}
	out := make([]adminc.DashboardConfigurationRow, 0, len(perConfig))
	for name, a := range perConfig {
		out = append(out, adminc.DashboardConfigurationRow{
			Configuration: name,
			Requests:      a.requests,
			ErrorRate:     safeRatio(a.errored, a.requests),
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
		m := key.Get(observability.AttrGenAIRequestModel)
		if m == "" {
			continue
		}
		get(m, key.Get(observability.AttrGenAIProviderName)).requests += v
	}
	for key, v := range tokensInDeltas {
		m := key.Get(observability.AttrGenAIRequestModel)
		if m == "" {
			continue
		}
		get(m, key.Get(observability.AttrGenAIProviderName)).tokensIn += v
	}
	for key, v := range tokensOutDeltas {
		m := key.Get(observability.AttrGenAIRequestModel)
		if m == "" {
			continue
		}
		get(m, key.Get(observability.AttrGenAIProviderName)).tokensOut += v
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

// computeTagsFired groups gateway.tags.applied.total deltas by tag and
// joins against the configuration → tags map so each row knows which
// configurations attach the tag. Mirrors computeRulesFired so the SPA
// can render a near-identical panel.
func computeTagsFired(start, end observability.Sample, tagAttachments map[string][]string) []adminc.DashboardTagFiredRow {
	deltas := counterDelta(start, end, observability.MetricTagsAppliedTotal)
	perTag := map[string]int64{}
	for key, v := range deltas {
		name := key.Get("tag")
		if name == "" {
			continue
		}
		perTag[name] += v
	}
	out := make([]adminc.DashboardTagFiredRow, 0, len(perTag))
	for name, count := range perTag {
		attached := tagAttachments[name]
		if attached == nil {
			attached = []string{}
		}
		out = append(out, adminc.DashboardTagFiredRow{
			Tag:                  name,
			ApplyCount:           count,
			UsedByConfigurations: attached,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ApplyCount > out[j].ApplyCount })
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
		p := key.Get(observability.AttrGenAIProviderName)
		a := perProvider[p]
		if a == nil {
			continue
		}
		a.total += v
		status := key.Get(observability.AttrHTTPResponseStatusCode)
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
