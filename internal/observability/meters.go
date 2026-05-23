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

// Instrument names. All gateway metrics live under the gateway. prefix so
// they sort together in Prometheus and remain disjoint from any future
// sibling services (a2a., mcp., ...).
const (
	MetricRequestsTotal            = "gateway.requests.total"
	MetricTokensInputTotal         = "gateway.tokens.input.total"
	MetricTokensOutputTotal        = "gateway.tokens.output.total"
	MetricTokensCachedTotal        = "gateway.tokens.cached.total"
	MetricTokensCacheCreationTotal = "gateway.tokens.cache_creation.total"
	MetricTagsAppliedTotal         = "gateway.tags.applied.total"
	MetricUnmappedFieldsTotal      = "gateway.unmapped_fields.total"
	MetricConfigReloadTotal        = "gateway.config_reload.total"
	MetricUpstreamErrorsTotal      = "gateway.upstream_errors.total"
	MetricErrorResponsesTotal      = "gateway.error_responses.total"

	MetricRequestDuration        = "gateway.request.duration"
	MetricRequestTimeToFirstByte = "gateway.request.time_to_first_byte"

	MetricActiveRequests = "gateway.active_requests"

	// Rule-engine instruments. Each is labelled by rule_name (always)
	// and rule_id (when the rule was minted by the control plane);
	// cardinality is bounded by the configured policy library, not by
	// inbound traffic.
	MetricRuleMatchesTotal       = "gateway.rule.matches.total"
	MetricRuleErrorsTotal        = "gateway.rule.errors.total"
	MetricRuleEvaluationDuration = "gateway.rule.evaluation.duration"

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
	RequestDurationBuckets = []float64{0, 0.1, 0.5, 1, 2, 5, 10, 30, 60, 120}

	TimeToFirstByteBuckets = []float64{0, 0.05, 0.1, 0.25, 0.5, 1, 2, 5}

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

	TokensInputTotal         metric.Int64Counter
	TokensOutputTotal        metric.Int64Counter
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
	ConfigReloadTotal   metric.Int64Counter
	UpstreamErrorsTotal metric.Int64Counter

	// ErrorResponsesTotal counts JSON error responses written by the
	// gateway middleware chain. Labels: layer (routing|handler|...),
	// code (machine-readable error name), status_code (HTTP status).
	ErrorResponsesTotal metric.Int64Counter

	RequestDuration        metric.Float64Histogram
	RequestTimeToFirstByte metric.Float64Histogram

	ActiveRequests metric.Int64UpDownCounter

	// RuleMatchesTotal counts rules that matched a request. Labels:
	// rule_name, rule_id (optional), terminated, action_count.
	RuleMatchesTotal metric.Int64Counter

	// RuleErrorsTotal counts action execution failures during rule
	// evaluation. Labels: rule_name, rule_id (optional), error_kind.
	RuleErrorsTotal metric.Int64Counter

	// RuleEvaluationDuration records the full per-request rule
	// evaluation cycle. Labels: configuration.
	RuleEvaluationDuration metric.Float64Histogram

	// GoroutinePanicsTotal counts panics caught by safego.Go in
	// background goroutines. Label: site (the identifier the caller
	// passed to safego.Go — e.g. "bus.publisher.worker").
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
		{MetricTokensInputTotal, "Sum of prompt tokens reported by upstream providers.", "1", &m.TokensInputTotal},
		{MetricTokensOutputTotal, "Sum of completion tokens reported by upstream providers.", "1", &m.TokensOutputTotal},
		{MetricTokensCachedTotal, "Sum of provider-reported cached input tokens (cache reads, billed at the discounted rate).", "1", &m.TokensCachedTotal},
		{MetricTokensCacheCreationTotal, "Sum of provider-reported cache-write tokens (Anthropic's chargeable cache-creation premium).", "1", &m.TokensCacheCreationTotal},
		{MetricTagsAppliedTotal, "Count of AddTagAction applications labelled by tag name. Cardinality bounded by configured policy.", "1", &m.TagsAppliedTotal},
		{MetricUnmappedFieldsTotal, "Unknown fields detected on inbound provider payloads.", "1", &m.UnmappedFieldsTotal},
		{MetricConfigReloadTotal, "Configuration reload attempts.", "1", &m.ConfigReloadTotal},
		{MetricUpstreamErrorsTotal, "Errors returned by upstream providers.", "1", &m.UpstreamErrorsTotal},
		{MetricErrorResponsesTotal, "JSON error responses written by the gateway middleware chain.", "1", &m.ErrorResponsesTotal},
		{MetricRuleMatchesTotal, "Rules that matched on a request.", "1", &m.RuleMatchesTotal},
		{MetricRuleErrorsTotal, "Action execution failures during rule evaluation.", "1", &m.RuleErrorsTotal},
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

	ttfb, err := meter.Float64Histogram(MetricRequestTimeToFirstByte,
		metric.WithDescription("Time from request acceptance to first response byte (streaming only)."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(TimeToFirstByteBuckets...),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: create histogram %s: %w", MetricRequestTimeToFirstByte, err)
	}
	m.RequestTimeToFirstByte = ttfb

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
