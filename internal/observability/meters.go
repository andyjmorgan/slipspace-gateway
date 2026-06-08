package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// circuitBreakerStateCallback closes over source + podID and returns
// the gauge's collection callback. Extracted so it stays readable next
// to the meter's option list and so the closure has a name in stack
// traces if a panic ever escapes the gauge reader.
func circuitBreakerStateCallback(source CircuitBreakerStateSource, podID string) metric.Int64Callback {
	return func(_ context.Context, o metric.Int64Observer) error {
		for _, snap := range source.Snapshot() {
			o.Observe(snap.State, metric.WithAttributes(
				attribute.String("policy", snap.Policy),
				attribute.String("target", snap.Target),
				attribute.String("pod", podID),
				attribute.String("state_name", snap.StateName),
			))
		}
		return nil
	}
}

// MeterName is the OpenTelemetry meter scope under which all gateway
// instruments are registered.
const MeterName = "sluice-gateway"

// Instrument names. The per-request inference signals follow the
// OpenTelemetry GenAI semantic conventions (gen_ai.*); Sluice-specific
// dimensions and convenience aggregates live under sluice.*; gateway.*
// is reserved for instruments the GenAI spec has no concept for (the
// rule engine, resilience orchestrator, circuit breaker, admin console).
// See the design note "OTel GenAI Conformance" for the naming discipline.
const (
	// MetricRequestsTotal is retained as a Sluice-namespaced convenience
	// counter. The GenAI spec derives request count from the duration
	// histogram's _count; we keep an explicit counter as a spec-legal
	// additive extra because the admin console's per-dimension request
	// counting is built on it (same emit-both rationale as cache tokens).
	MetricRequestsTotal = "sluice.requests.total"

	// MetricTokenUsage is the spec gen_ai.client.token.usage histogram,
	// keyed by gen_ai.token.type=input|output. There is no server-side
	// token metric in the spec, so this client metric is emitted from the
	// proxy by deliberate choice. It is the distribution view (Prometheus/
	// Grafana); the input/output COUNTERS below carry the same totals for the
	// central telemetry service, whose ingest does not read histograms.
	MetricTokenUsage = "gen_ai.client.token.usage" //nolint:gosec // G101 false positive: metric name, not a credential

	// Input/output token COUNTERS, emitted alongside the gen_ai.client.token.usage
	// histogram above. The histogram serves Prometheus/Grafana (buckets,
	// distributions); the central telemetry service ingests only counter/gauge
	// metric points (its OTLP ingest skips histograms by design), so the
	// dashboard's token-sum continuous aggregates need these counter mirrors.
	// Same dimensions + temporality as the cache counters below, which the
	// telemetry dashboard already sums.
	MetricTokensInputTotal  = "sluice.tokens.input.total"  //nolint:gosec // G101 false positive: metric name, not a credential
	MetricTokensOutputTotal = "sluice.tokens.output.total" //nolint:gosec // G101 false positive: metric name, not a credential

	// Cache tokens have no gen_ai.token.type value (the spec enum is
	// input|output only), so they ride Sluice-namespaced counters. Both
	// are billing-load-bearing: cached is the discounted cache-read,
	// cache_creation the chargeable cache-write premium.
	MetricTokensCachedTotal        = "sluice.tokens.cached.total"         //nolint:gosec // G101 false positive: metric name, not a credential
	MetricTokensCacheCreationTotal = "sluice.tokens.cache_creation.total" //nolint:gosec // G101 false positive: metric name, not a credential

	MetricTagsAppliedTotal    = "gateway.tags.applied.total"
	MetricUnmappedFieldsTotal = "gateway.unmapped_fields.total"

	// MetricTranslationFieldDropsTotal counts source features dropped during
	// cross-provider translation because the target protocol has no
	// equivalent, labelled by source/target protocol and dropped field. A
	// field's count climbing is an early warning that a provider shipped a
	// feature the translator does not yet carry. Cardinality is bounded by the
	// modelled field set.
	MetricTranslationFieldDropsTotal = "gateway.translation.field_drops.total"

	// MetricRuleFiredTotal counts rules that fired on a request, labelled
	// by rule_name + configuration. Distinct from gateway.rule.matches.total
	// (evaluator-emitted, labelled rule_name/rule_id/terminated/action_count
	// for engine introspection): this Sluice-namespaced counter carries the
	// configuration so the central telemetry service can roll up
	// "rule X fired N times under configuration Y" without the gateway's
	// rule→configuration map — it is the meter the rules-fired dashboard
	// panel reads (telemetry design channel 2, invariant #4: dashboards
	// read meters, never record scans).
	MetricRuleFiredTotal = "sluice.rule.fired"

	// MetricConfigHitsTotal counts requests resolved to a configuration,
	// labelled by configuration. The configuration-hit aggregate the
	// dashboard's by-configuration panel reads from the meter rollup rather
	// than scanning the per-request record store.
	MetricConfigHitsTotal     = "sluice.config.hit"
	MetricConfigReloadTotal   = "gateway.config_reload.total"
	MetricUpstreamErrorsTotal = "gateway.upstream_errors.total"
	MetricErrorResponsesTotal = "gateway.error_responses.total"

	// The inference latency signals use the GenAI spec's *client* vantage,
	// not the server one. Sluice is a client of the upstream provider — it
	// calls the model and observes wire timing; it does not generate tokens,
	// so it cannot produce the server metrics' defining quantity (model-
	// internal time-to-generate-first-token). What it measures is the
	// caller-observed round trip and time-to-first-chunk, which is exactly
	// what gen_ai.client.operation.* is defined as. This pairs with the
	// client-vantage gen_ai.client.token.usage above.
	MetricRequestDuration    = "gen_ai.client.operation.duration"
	MetricTimeToFirstChunk   = "gen_ai.client.operation.time_to_first_chunk"
	MetricTimePerOutputChunk = "gen_ai.client.operation.time_per_output_chunk"

	MetricActiveRequests = "gateway.active_requests"

	// Rule-engine instruments. Each is labelled by rule_name (always)
	// and rule_id (when the rule was minted by the control plane);
	// cardinality is bounded by the configured policy library, not by
	// inbound traffic.
	MetricRuleMatchesTotal       = "gateway.rule.matches.total"
	MetricRuleErrorsTotal        = "gateway.rule.errors.total"
	MetricRuleEvaluationDuration = "gateway.rule.evaluation.duration"

	// Body-rewrite instruments. applied counts successful field
	// mutations; dropped counts mutations skipped with a reason
	// (path_traverses_primitive, append_non_array, template_ref_miss,
	// apply_error). Labels: action_type, and (dropped only) reason.
	MetricRewriteAppliedTotal = "gateway.rewrite.applied.total"
	MetricRewriteDroppedTotal = "gateway.rewrite.dropped.total"

	// Crash-safety instruments. Both surface a panic that the
	// gateway's recover() wrappers converted to a logged error
	// rather than letting the process exit. A non-zero rate on
	// either is an operator-attention signal: the request-side
	// counter implies a buggy code path on the data plane; the
	// goroutine-side counter implies an unhandled edge in a
	// background worker (spool drain, segment writer, upload
	// worker, etc.).
	MetricGoroutinePanicsTotal = "gateway.goroutine.panics.total"
	MetricRequestPanicsTotal   = "gateway.request.panics.total"

	// MetricAdminRequestsTotal counts requests to the management
	// console's listener — the second http.Server bound to
	// gateway.admin.bind_addr. Separate from MetricRequestsTotal so
	// dashboards and SLOs for the data plane stay disjoint from
	// operator UI traffic.
	MetricAdminRequestsTotal = "gateway.admin.requests.total"

	// Resilience-orchestrator instruments. Labels are bounded by the
	// configured policy library (policy name + target name come from
	// the operator-authored YAML, never client-derived). Outcome is
	// a fixed-vocabulary string mirroring events.AttemptRecord.Outcome
	// so the metric label set and the wire event field stay aligned.
	MetricResilienceAttemptsTotal      = "gateway.resilience.attempts.total"
	MetricResilienceAttemptDuration    = "gateway.resilience.attempt.duration"
	MetricResilienceAttemptsPerRequest = "gateway.resilience.attempts_per_request"
	MetricResilienceOutcomeTotal       = "gateway.resilience.outcome.total"

	// Circuit-breaker instruments. The state gauge is an
	// ObservableGauge — the registered callback iterates the
	// BreakerStore at collection time and reports the current
	// numeric state (0=closed, 1=open, 2=half_open) per
	// (policy, target, pod). The transitions counter is bumped
	// synchronously from the breaker's StateListener on every state
	// change.
	MetricCircuitBreakerState           = "gateway.cb.state"
	MetricCircuitBreakerTransitionTotal = "gateway.cb.transitions.total"

	// MetricAdminConfigExportsTotal counts redacted-config bundle
	// downloads served by the admin console's export endpoint.
	// Labelled by status (HTTP status code) so a spike in failures
	// is visible without scanning logs.
	MetricAdminConfigExportsTotal = "gateway.admin.config_exports.total"
)

// Histogram bucket boundaries. Defined as package-level vars (not consts)
// because Go does not permit composite literal constants; each slice is
// only read by NewMeters and never mutated.
var (
	// genAIDurationBuckets are the GenAI-semconv recommended boundaries
	// (seconds) for gen_ai.client.operation.duration, .time_to_first_chunk,
	// and .time_per_output_chunk — a power-of-two sweep from 10ms to ~82s.
	// Shared so the three client latency histograms (and the resilience
	// attempt-duration histogram, which mirrors the request shape) use the
	// spec boundaries.
	genAIDurationBuckets = []float64{0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28, 2.56, 5.12, 10.24, 20.48, 40.96, 81.92}

	RequestDurationBuckets = genAIDurationBuckets

	TimeToFirstChunkBuckets = genAIDurationBuckets

	TimePerOutputChunkBuckets = genAIDurationBuckets

	// RuleEvaluationDurationBuckets gives sub-millisecond resolution
	// since the engine runs synchronously on the request path; the
	// long tail captures pathological policies (deep groups, regex
	// catastrophes) without polluting the common case.
	RuleEvaluationDurationBuckets = []float64{0, 0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1}

	// AttemptsPerRequestBuckets is tuned for the realistic spread of
	// resilience attempts: most requests are single-shot, multi-target
	// failover rarely exceeds 3 attempts, and the long-tail anchor at
	// 10 covers pathological policy fan-out without polluting the
	// common case.
	AttemptsPerRequestBuckets = []float64{1, 2, 3, 5, 10}
)

// Meters bundles every gateway instrument so that callers can pass a
// single value through the middleware chain rather than reaching into a
// global meter on every emit. All fields are populated by NewMeters.
type Meters struct {
	RequestsTotal metric.Int64Counter

	// TokenUsage is the gen_ai.client.token.usage histogram. One Record
	// per direction, tagged gen_ai.token.type=input|output. Replaces the
	// former input/output counters; per-window totals come from the
	// histogram sum.
	TokenUsage metric.Int64Histogram

	// TokensInputTotal / TokensOutputTotal mirror the TokenUsage histogram as
	// counters so the central telemetry service (which ingests no histograms)
	// can sum per-window token totals in its dashboard continuous aggregates.
	TokensInputTotal  metric.Int64Counter
	TokensOutputTotal metric.Int64Counter

	TokensCachedTotal        metric.Int64Counter
	TokensCacheCreationTotal metric.Int64Counter

	// TagsAppliedTotal counts AddTagAction applications. Labelled
	// by tag name; cardinality bounded by the configured policy
	// library (operator-defined, never client-derived). Side-channel
	// to gateway.requests.total — keeps the request counter's
	// labelset bounded against the multiplicative blow-up that would
	// follow from joining tags onto the request series.
	TagsAppliedTotal metric.Int64Counter

	UnmappedFieldsTotal metric.Int64Counter

	// TranslationFieldDropsTotal counts cross-provider translation field drops
	// (see MetricTranslationFieldDropsTotal).
	TranslationFieldDropsTotal metric.Int64Counter

	ConfigReloadTotal   metric.Int64Counter
	UpstreamErrorsTotal metric.Int64Counter

	// ErrorResponsesTotal counts JSON error responses written by the
	// gateway middleware chain. Labels: layer (routing|handler|...),
	// code (machine-readable error name), status_code (HTTP status).
	ErrorResponsesTotal metric.Int64Counter

	RequestDuration  metric.Float64Histogram
	TimeToFirstChunk metric.Float64Histogram

	// TimePerOutputChunk is the gen_ai.client.operation.time_per_output_chunk
	// histogram: the gap between consecutive streamed response chunks, one
	// observation per chunk after the first. Streaming requests only.
	TimePerOutputChunk metric.Float64Histogram

	ActiveRequests metric.Int64UpDownCounter

	// RuleFiredTotal counts rules that fired, labelled rule_name +
	// configuration. Emitted by the reporter (where the configuration is
	// resolved) so the central telemetry service can roll up rule hits per
	// configuration without the gateway's rule→configuration map.
	RuleFiredTotal metric.Int64Counter

	// ConfigHitsTotal counts requests per resolved configuration. Backs the
	// by-configuration dashboard aggregate via the meter rollup.
	ConfigHitsTotal metric.Int64Counter

	// RuleMatchesTotal counts rules that matched a request. Labels:
	// rule_name, rule_id (optional), terminated, action_count.
	RuleMatchesTotal metric.Int64Counter

	// RuleErrorsTotal counts action execution failures during rule
	// evaluation. Labels: rule_name, rule_id (optional), error_kind.
	RuleErrorsTotal metric.Int64Counter

	// RuleEvaluationDuration records the full per-request rule
	// evaluation cycle. Labels: configuration.
	RuleEvaluationDuration metric.Float64Histogram

	// RewriteAppliedTotal counts body-field mutations that changed the
	// request body. Label: action_type (rewriteField|removeField|
	// appendField).
	RewriteAppliedTotal metric.Int64Counter

	// RewriteDroppedTotal counts body-field mutations skipped without
	// changing the body. Labels: action_type, reason.
	RewriteDroppedTotal metric.Int64Counter

	// GoroutinePanicsTotal counts panics caught by safego.Go in
	// background goroutines. Label: site (the identifier the caller
	// passed to safego.Go — e.g. "spool.uploader", "spool.drain").
	GoroutinePanicsTotal metric.Int64Counter

	// RequestPanicsTotal counts panics caught by the request-path
	// recovery middleware. Labels: provider, endpoint (where
	// resolvable — best-effort from context). A non-zero rate
	// implies a buggy middleware or handler is leaking panics that
	// the recovery filter is converting to 500s.
	RequestPanicsTotal metric.Int64Counter

	// AdminRequestsTotal counts requests handled by the management-
	// console listener. Labels: route (the matched route — e.g.
	// "/api/v1/auth/me", "static" for SPA assets, "fallback" for
	// index.html SPA fallbacks), status (HTTP status code).
	AdminRequestsTotal metric.Int64Counter

	// ResilienceAttemptsTotal counts every per-attempt outcome the
	// resilience orchestrator records. Labels: policy, target,
	// outcome (success|failure_status|transport_error|cb_blocked).
	// One increment per AttemptRecord; cb_blocked entries fire
	// without any per-attempt forwarder call.
	ResilienceAttemptsTotal metric.Int64Counter

	// ResilienceAttemptDuration is the per-attempt wall-clock
	// duration histogram. Labels: policy, target. cb_blocked attempts
	// are not observed (no attempt actually ran). Buckets share the
	// RequestDuration shape so dashboards can correlate.
	ResilienceAttemptDuration metric.Float64Histogram

	// ResilienceAttemptsPerRequest records the count of attempts each
	// inbound request consumed. Labels: policy. Recorded once per
	// request at FireTerminal time. Buckets are integer-tuned (1, 2,
	// 3, 5, 10) since attempt counts in practice are very small.
	ResilienceAttemptsPerRequest metric.Int64Histogram

	// ResilienceOutcomeTotal counts the orchestrator-level outcome of
	// each request: success when one attempt committed, all_failed
	// when every attempt errored or returned a retryable status,
	// all_open when every target was filtered by the circuit
	// breaker before any attempt ran. Labels: policy, outcome.
	ResilienceOutcomeTotal metric.Int64Counter

	// CircuitBreakerTransitionTotal counts state transitions on each
	// breaker. Labels: policy, target, to_state (closed|open|half_open).
	// Bumped synchronously from the breaker's StateListener — one
	// increment per closed→open, open→half_open, half_open→closed,
	// half_open→open edge.
	CircuitBreakerTransitionTotal metric.Int64Counter

	// AdminConfigExportsTotal counts redacted-config bundle downloads
	// served by the admin console's export endpoint. Labels: status
	// (HTTP status code) — a spike on non-200 surfaces export failures
	// (e.g. malformed YAML on disk) without scanning logs.
	AdminConfigExportsTotal metric.Int64Counter
}

// NewMeters constructs the Meters bundle from the supplied meter. The
// caller typically obtains the meter via MeterProvider.Meter(MeterName).
// All instruments are eagerly created so that a misconfigured meter
// surfaces immediately at startup rather than mid-request.
func NewMeters(meter metric.Meter) (*Meters, error) {
	if meter == nil {
		return nil, fmt.Errorf("observability: meter is required")
	}

	m := &Meters{}

	int64Counter := func(name, desc, unit string, dst *metric.Int64Counter) error {
		c, err := meter.Int64Counter(name,
			metric.WithDescription(desc),
			metric.WithUnit(unit),
		)
		if err != nil {
			return fmt.Errorf("observability: create counter %s: %w", name, err)
		}
		*dst = c
		return nil
	}

	for _, c := range []struct {
		name, desc, unit string
		dst              *metric.Int64Counter
	}{
		{MetricRequestsTotal, "Total requests completed.", "1", &m.RequestsTotal},
		{MetricTokensInputTotal, "Sum of provider-reported input tokens (counter mirror of the token-usage histogram for the telemetry service's dashboard rollups).", "1", &m.TokensInputTotal},
		{MetricTokensOutputTotal, "Sum of provider-reported output tokens (counter mirror of the token-usage histogram for the telemetry service's dashboard rollups).", "1", &m.TokensOutputTotal},
		{MetricTokensCachedTotal, "Sum of provider-reported cached input tokens (cache reads, billed at the discounted rate).", "1", &m.TokensCachedTotal},
		{MetricTokensCacheCreationTotal, "Sum of provider-reported cache-write tokens (Anthropic's chargeable cache-creation premium).", "1", &m.TokensCacheCreationTotal},
		{MetricTagsAppliedTotal, "Count of AddTagAction applications labelled by tag name. Cardinality bounded by configured policy.", "1", &m.TagsAppliedTotal},
		{MetricUnmappedFieldsTotal, "Provider fields this build does not model, detected on request and response payloads and labelled by field path and direction.", "1", &m.UnmappedFieldsTotal},
		{MetricTranslationFieldDropsTotal, "Source features dropped during cross-provider translation, labelled by source/target protocol and dropped field.", "1", &m.TranslationFieldDropsTotal},
		{MetricConfigReloadTotal, "Configuration reload attempts.", "1", &m.ConfigReloadTotal},
		{MetricUpstreamErrorsTotal, "Errors returned by upstream providers.", "1", &m.UpstreamErrorsTotal},
		{MetricErrorResponsesTotal, "JSON error responses written by the gateway middleware chain.", "1", &m.ErrorResponsesTotal},
		{MetricRuleFiredTotal, "Rules that fired on a request, labelled by rule_name and configuration. Backs the telemetry rules-fired dashboard rollup.", "1", &m.RuleFiredTotal},
		{MetricConfigHitsTotal, "Requests resolved to a configuration, labelled by configuration. Backs the telemetry by-configuration dashboard rollup.", "1", &m.ConfigHitsTotal},
		{MetricRuleMatchesTotal, "Rules that matched on a request.", "1", &m.RuleMatchesTotal},
		{MetricRuleErrorsTotal, "Action execution failures during rule evaluation.", "1", &m.RuleErrorsTotal},
		{MetricRewriteAppliedTotal, "Body-field mutations that changed the request body, by action_type.", "1", &m.RewriteAppliedTotal},
		{MetricRewriteDroppedTotal, "Body-field mutations skipped without changing the body, by action_type and reason.", "1", &m.RewriteDroppedTotal},
		{MetricGoroutinePanicsTotal, "Panics caught by safego.Go in background goroutines (process kept alive).", "1", &m.GoroutinePanicsTotal},
		{MetricRequestPanicsTotal, "Panics caught by the request-path recovery middleware (client got 500, process kept alive).", "1", &m.RequestPanicsTotal},
		{MetricAdminRequestsTotal, "Requests handled by the management-console listener (separate from data-plane requests).", "1", &m.AdminRequestsTotal},
		{MetricResilienceAttemptsTotal, "Per-attempt outcomes recorded by the resilience orchestrator (success, failure_status, transport_error, cb_blocked).", "1", &m.ResilienceAttemptsTotal},
		{MetricResilienceOutcomeTotal, "Per-request orchestrator outcome (success, all_failed, all_open). Bumped once per inbound request that ran through a resilience policy.", "1", &m.ResilienceOutcomeTotal},
		{MetricCircuitBreakerTransitionTotal, "Circuit-breaker state transitions per (policy, target, to_state). One increment per state change.", "1", &m.CircuitBreakerTransitionTotal},
		{MetricAdminConfigExportsTotal, "Redacted-config bundle downloads served by the admin export endpoint.", "1", &m.AdminConfigExportsTotal},
	} {
		if err := int64Counter(c.name, c.desc, c.unit, c.dst); err != nil {
			return nil, err
		}
	}

	reqDuration, err := meter.Float64Histogram(MetricRequestDuration,
		metric.WithDescription("End-to-end request duration."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(RequestDurationBuckets...),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: create histogram %s: %w", MetricRequestDuration, err)
	}
	m.RequestDuration = reqDuration

	ttfb, err := meter.Float64Histogram(MetricTimeToFirstChunk,
		metric.WithDescription("Time from request acceptance to the first response chunk received from upstream. Streaming requests only — the GenAI spec scopes this metric to streaming calls; a non-streaming response has no first chunk distinct from the full body."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(TimeToFirstChunkBuckets...),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: create histogram %s: %w", MetricTimeToFirstChunk, err)
	}
	m.TimeToFirstChunk = ttfb

	tpoc, err := meter.Float64Histogram(MetricTimePerOutputChunk,
		metric.WithDescription("Time between consecutive streamed response chunks, one observation per chunk after the first. Streaming requests only."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(TimePerOutputChunkBuckets...),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: create histogram %s: %w", MetricTimePerOutputChunk, err)
	}
	m.TimePerOutputChunk = tpoc

	tokenUsage, err := meter.Int64Histogram(MetricTokenUsage,
		metric.WithDescription("Number of input/output tokens reported by upstream providers, keyed by gen_ai.token.type."),
		metric.WithUnit("{token}"),
		metric.WithExplicitBucketBoundaries(TokenUsageBuckets...),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: create histogram %s: %w", MetricTokenUsage, err)
	}
	m.TokenUsage = tokenUsage

	active, err := meter.Int64UpDownCounter(MetricActiveRequests,
		metric.WithDescription("Requests currently in flight."),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: create up-down counter %s: %w", MetricActiveRequests, err)
	}
	m.ActiveRequests = active

	ruleEval, err := meter.Float64Histogram(MetricRuleEvaluationDuration,
		metric.WithDescription("Per-request rule evaluation cycle duration."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(RuleEvaluationDurationBuckets...),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: create histogram %s: %w", MetricRuleEvaluationDuration, err)
	}
	m.RuleEvaluationDuration = ruleEval

	resAttemptDuration, err := meter.Float64Histogram(MetricResilienceAttemptDuration,
		metric.WithDescription("Per-attempt wall-clock duration recorded by the resilience orchestrator."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(RequestDurationBuckets...),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: create histogram %s: %w", MetricResilienceAttemptDuration, err)
	}
	m.ResilienceAttemptDuration = resAttemptDuration

	resAttemptsPerRequest, err := meter.Int64Histogram(MetricResilienceAttemptsPerRequest,
		metric.WithDescription("Distribution of attempts consumed per request running through a resilience policy."),
		metric.WithUnit("1"),
		metric.WithExplicitBucketBoundaries(AttemptsPerRequestBuckets...),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: create histogram %s: %w", MetricResilienceAttemptsPerRequest, err)
	}
	m.ResilienceAttemptsPerRequest = resAttemptsPerRequest

	return m, nil
}

// CircuitBreakerStateSource is the read interface NewCircuitBreakerStateGauge
// needs to populate the cb.state ObservableGauge at collection time. The
// resilience BreakerStore implements this; the indirection keeps this
// package free of the resilience import cycle.
type CircuitBreakerStateSource interface {
	// Snapshot returns the current breaker state of every observed
	// (policy, target). Implementations must be safe to call from
	// the snapshot collection goroutine.
	Snapshot() []CircuitBreakerSnapshot
}

// CircuitBreakerSnapshot mirrors resilience.BreakerSnapshot at the
// observability boundary so the gauge callback can encode the state
// label without importing the resilience package.
type CircuitBreakerSnapshot struct {
	// Policy is the resilience policy name.
	Policy string
	// Target is the resilience target name.
	Target string
	// State is the numeric breaker state (0=closed, 1=open,
	// 2=half_open).
	State int64
	// StateName is the human-readable form for the to_state metric
	// label / dashboard tooltip.
	StateName string
}

// RegisterCircuitBreakerStateGauge wires the gateway.cb.state
// ObservableGauge against source. Returns no error when source is nil
// — the gauge is silently omitted, useful in test contexts that don't
// run the orchestrator. The pod label uses the supplied hostname so
// multi-pod deployments can disambiguate which replica's view a series
// reflects.
func RegisterCircuitBreakerStateGauge(meter metric.Meter, source CircuitBreakerStateSource, podID string) error {
	if source == nil {
		return nil
	}
	if meter == nil {
		return fmt.Errorf("observability: meter is required for cb.state gauge")
	}
	_, err := meter.Int64ObservableGauge(MetricCircuitBreakerState,
		metric.WithDescription("Per-(policy, target, pod) circuit-breaker state: 0=closed, 1=open, 2=half_open. Reported via callback so the gauge always reflects current state at scrape time."),
		metric.WithUnit("1"),
		metric.WithInt64Callback(circuitBreakerStateCallback(source, podID)),
	)
	if err != nil {
		return fmt.Errorf("observability: register %s: %w", MetricCircuitBreakerState, err)
	}
	return nil
}
