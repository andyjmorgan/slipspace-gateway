package admin

import "time"

// DashboardSummary is the response shape for GET /api/v1/dashboard/summary.
//
// Fields match what the gateway can credibly compute from the in-process
// Prometheus registry. Per-api-key aggregates are still deliberately
// absent — the Top API Keys card is a follow-up. The schema mirrors the
// SPA's TypeScript shape (web/src/lib/dashboard.ts) so the SPA decodes
// without translation.
type DashboardSummary struct {
	// Window is the time range the summary covers (e.g. "24h"). The
	// SPA renders this verbatim.
	Window string `json:"window"`

	// GeneratedAt is the wall-clock instant the summary was computed.
	GeneratedAt time.Time `json:"generated_at"`

	// GatewayStartedAt is the wall-clock instant the gateway process
	// began. The SPA renders "Gateway up Xh Ym" beneath the dashboard
	// header and ticks it client-side off this static value; the SPA
	// does not need to re-poll for the running duration to update.
	GatewayStartedAt time.Time `json:"gateway_started_at"`

	// Totals is the request-count headline plus the four token sums.
	Totals DashboardTotals `json:"totals"`

	// Rates carries the gauge-style aggregates: requests-per-second
	// and the error rate over Window.
	Rates DashboardRates `json:"rates"`

	// LatencyMs is the duration-histogram quantile set.
	LatencyMs DashboardLatency `json:"latency_ms"`

	// ByProvider breaks requests + p95 + error rate down by provider.
	// Each entry corresponds to one configured provider that saw at
	// least one request in Window.
	ByProvider []DashboardProviderRow `json:"by_provider"`

	// ByProtocol breaks requests + p95 + error rate down by the resolved
	// (provider, protocol) pair — e.g. openai.chat vs anthropic.messages.
	// Cardinality is bounded by the providers map (a handful of protocols
	// per provider).
	ByProtocol []DashboardProtocolRow `json:"by_protocol"`

	// ByConfiguration breaks requests + p95 + error rate down by the
	// resolved configuration name. Cardinality is bounded by the
	// operator-defined configurations YAML; never client-derived.
	ByConfiguration []DashboardConfigurationRow `json:"by_configuration"`

	// ByModel breaks requests down by upstream model (the model
	// label on gateway.requests.total).
	ByModel []DashboardModelRow `json:"by_model"`

	// RulesFired counts matches per rule_name over Window, joined
	// against the configurations that reference each rule.
	RulesFired []DashboardRuleFiredRow `json:"rules_fired"`

	// TagsFired counts AddTagAction applications per tag over Window,
	// joined against the configurations whose rule chain attaches each
	// tag. Mirrors RulesFired so the SPA can render a near-identical
	// panel.
	TagsFired []DashboardTagFiredRow `json:"tags_fired"`

	// ProviderHealth is the per-provider health snapshot. Today only
	// ErrorRate5m is computed from real metrics; the remaining fields
	// are populated when the provider-health probe lands. The shape
	// is fixed so the SPA does not need updating then.
	ProviderHealth []DashboardProviderHealth `json:"provider_health"`
}

// DashboardTotals headlines the volume + outcome split over Window plus
// the aggregate token counts. Token totals sum across every label-set
// (provider, endpoint, configuration, model, status_code) so they match
// the gateway.tokens.* counter rollup exactly.
type DashboardTotals struct {
	Requests int64 `json:"requests"`

	RequestsSuccess int64 `json:"requests_success"`

	RequestsErrored int64 `json:"requests_errored"`

	// TokensIn is the sum of prompt tokens billed across every
	// completed request in Window. Includes any cached portion
	// (same gross-input semantic as on contracts/events/Request).
	TokensIn int64 `json:"tokens_in"`

	// TokensOut is the sum of generated tokens billed in Window.
	// Includes reasoning / thoughts tokens where the provider
	// reports them.
	TokensOut int64 `json:"tokens_out"`

	// TokensCached is the share of TokensIn the provider served
	// from cache (discounted read price). Informational.
	TokensCached int64 `json:"tokens_cached"`

	// TokensCacheCreation is the share of TokensIn billed at the
	// cache-write premium. Anthropic-only on today's surface.
	TokensCacheCreation int64 `json:"tokens_cache_creation"`
}

// DashboardRates carries the rate-style aggregates.
type DashboardRates struct {
	RequestsPerSecond float64 `json:"requests_per_second"`
	ErrorRate         float64 `json:"error_rate"`
}

// DashboardLatency is the request-duration quantile set, in
// milliseconds — Prometheus stores seconds, the handler converts so
// the SPA can use ms throughout.
type DashboardLatency struct {
	P50 int64 `json:"p50"`
	P95 int64 `json:"p95"`
	P99 int64 `json:"p99"`
}

// DashboardProviderRow is one row of ByProvider.
type DashboardProviderRow struct {
	Provider     string  `json:"provider"`
	Requests     int64   `json:"requests"`
	P95LatencyMs int64   `json:"p95_latency_ms"`
	ErrorRate    float64 `json:"error_rate"`
}

// DashboardProtocolRow is one row of ByProtocol. Provider is repeated
// alongside Protocol so the SPA can render a single readable label
// (e.g. "openai · chat") without joining tables.
type DashboardProtocolRow struct {
	Provider     string  `json:"provider"`
	Protocol     string  `json:"protocol"`
	Requests     int64   `json:"requests"`
	P95LatencyMs int64   `json:"p95_latency_ms"`
	ErrorRate    float64 `json:"error_rate"`
}

// DashboardConfigurationRow is one row of ByConfiguration. The
// Configuration name matches the keys in configurations.yaml.
type DashboardConfigurationRow struct {
	Configuration string  `json:"configuration"`
	Requests      int64   `json:"requests"`
	P95LatencyMs  int64   `json:"p95_latency_ms"`
	ErrorRate     float64 `json:"error_rate"`
}

// DashboardModelRow is one row of ByModel. Token counts are per-model
// totals across Window — useful for spotting which model burned the
// most spend even when its request count is modest.
type DashboardModelRow struct {
	Model string `json:"model"`

	Provider string `json:"provider"`

	Requests int64 `json:"requests"`

	TokensIn int64 `json:"tokens_in"`

	TokensOut int64 `json:"tokens_out"`
}

// DashboardRuleFiredRow counts matches for one rule across the window
// and lists the configurations that reference the rule.
type DashboardRuleFiredRow struct {
	RuleName             string   `json:"rule_name"`
	FireCount            int64    `json:"fire_count"`
	UsedByConfigurations []string `json:"used_by_configurations"`
}

// DashboardTagFiredRow counts AddTagAction applications for one tag
// across the window and lists the configurations whose rule chain
// attaches the tag.
type DashboardTagFiredRow struct {
	Tag                  string   `json:"tag"`
	ApplyCount           int64    `json:"apply_count"`
	UsedByConfigurations []string `json:"used_by_configurations"`
}

// DashboardProviderHealth is one row of ProviderHealth.
//
// Only {Provider, Healthy, ErrorRate5m, Requests5m} are populated by
// v1.1 because that's what the in-process registry can compute from
// gateway.requests.total alone. Tail fields (consecutive errors, last
// error message, last success timestamp) require a probe goroutine
// that doesn't exist yet — they were removed from the contract rather
// than left at zero-value placeholders the SPA had to defensively
// hide.
//
// Requests5m lets the SPA distinguish "0% errors over many requests"
// (healthy + traffic) from "0% errors because no traffic" (healthy
// but uninformative). Without it the user can't tell idle from green.
type DashboardProviderHealth struct {
	Provider    string  `json:"provider"`
	Healthy     bool    `json:"healthy"`
	ErrorRate5m float64 `json:"error_rate_5m"`
	Requests5m  int64   `json:"requests_5m"`
}

// DashboardTimeseries is the response shape for
// GET /api/v1/dashboard/timeseries. The endpoint returns one Series per
// labelled curve — a single-series query (RPS, overall error rate) is
// a slice of length one; multi-series queries (p95 by provider) return
// one entry per group key.
type DashboardTimeseries struct {
	Series []DashboardSeries `json:"series"`
}

// DashboardSeries names one curve plotted on a dashboard chart panel.
type DashboardSeries struct {
	// Name is the SPA-rendered label for this curve (legend text).
	Name string `json:"name"`

	// Unit annotates the value axis (e.g. "req/s", "%", "ms").
	Unit string `json:"unit,omitempty"`

	// Labels carries the grouping key for multi-series queries —
	// e.g. {"provider": "openai"}. Empty for single-series queries.
	Labels map[string]string `json:"labels,omitempty"`

	// Points is the ordered series, oldest first. Each point's
	// timestamp is the end of the interval it represents.
	Points []DashboardPoint `json:"points"`
}

// DashboardPoint is one (timestamp, value) tuple on a Series.
type DashboardPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}
