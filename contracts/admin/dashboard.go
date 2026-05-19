package admin

import "time"

// DashboardSummary is the response shape for GET /api/v1/dashboard/summary.
//
// The fields are chosen to match what the gateway can credibly compute
// from the in-process Prometheus registry today — token counters and
// per-api-key aggregates are deliberately absent because the underlying
// instruments aren't wired (see DonkeyWork project notes on the v1.1
// console slice). The schema mirrors the SPA's TypeScript shape
// (web/src/mock/data.ts) so the SPA decodes without translation.
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

	// Totals is the request-count headline. No token totals — they
	// remain unwired in v1.1.
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

	// ByModel breaks requests down by upstream model (the model
	// label on gateway.requests.total).
	ByModel []DashboardModelRow `json:"by_model"`

	// RulesFired counts matches per rule_name over Window, joined
	// against the configurations that reference each rule.
	RulesFired []DashboardRuleFiredRow `json:"rules_fired"`

	// ProviderHealth is the per-provider health snapshot. Today only
	// ErrorRate5m is computed from real metrics; the remaining fields
	// are populated when the provider-health probe lands. The shape
	// is fixed so the SPA does not need updating then.
	ProviderHealth []DashboardProviderHealth `json:"provider_health"`
}

// DashboardTotals headlines the volume + outcome split over Window.
type DashboardTotals struct {
	Requests        int64 `json:"requests"`
	RequestsSuccess int64 `json:"requests_success"`
	RequestsErrored int64 `json:"requests_errored"`
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

// DashboardModelRow is one row of ByModel.
type DashboardModelRow struct {
	Model    string `json:"model"`
	Provider string `json:"provider"`
	Requests int64  `json:"requests"`
}

// DashboardRuleFiredRow counts matches for one rule across the window
// and lists the configurations that reference the rule.
type DashboardRuleFiredRow struct {
	RuleName             string   `json:"rule_name"`
	FireCount            int64    `json:"fire_count"`
	UsedByConfigurations []string `json:"used_by_configurations"`
}

// DashboardProviderHealth is one row of ProviderHealth.
//
// Only the {Provider, Healthy, ErrorRate5m} fields are populated by
// v1.1 because that's all the in-process registry can compute from
// gateway.requests.total alone. Tail fields (consecutive errors, last
// error message, last success timestamp) require a probe goroutine
// that doesn't exist yet — they were removed from the contract rather
// than left at zero-value placeholders the SPA had to defensively
// hide.
type DashboardProviderHealth struct {
	Provider    string  `json:"provider"`
	Healthy     bool    `json:"healthy"`
	ErrorRate5m float64 `json:"error_rate_5m"`
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
