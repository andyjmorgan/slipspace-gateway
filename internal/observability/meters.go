package observability

import (
	"fmt"

	"go.opentelemetry.io/otel/metric"
)

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
	MetricEventsPublishedTotal     = "gateway.events_published.total"
	MetricEventsDroppedTotal       = "gateway.events_dropped.total"
	MetricEventsStashedTotal       = "gateway.events_stashed.total"
	MetricUnmappedFieldsTotal      = "gateway.unmapped_fields.total"
	MetricConfigReloadTotal        = "gateway.config_reload.total"
	MetricUpstreamErrorsTotal      = "gateway.upstream_errors.total"
	MetricErrorResponsesTotal      = "gateway.error_responses.total"

	MetricRequestDuration        = "gateway.request.duration"
	MetricRequestTimeToFirstByte = "gateway.request.time_to_first_byte"
	MetricEventsInlineBytes      = "gateway.events.inline_bytes"

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
	// background worker (NATS dispatch, drain handler, etc.).
	MetricGoroutinePanicsTotal = "gateway.goroutine.panics.total"
	MetricRequestPanicsTotal   = "gateway.request.panics.total"

	// MetricAdminRequestsTotal counts requests to the management
	// console's listener — the second http.Server bound to
	// gateway.admin.bind_addr. Separate from MetricRequestsTotal so
	// dashboards and SLOs for the data plane stay disjoint from
	// operator UI traffic.
	MetricAdminRequestsTotal = "gateway.admin.requests.total"
)

// Histogram bucket boundaries. Defined as package-level vars (not consts)
// because Go does not permit composite literal constants; each slice is
// only read by NewMeters and never mutated.
var (
	RequestDurationBuckets = []float64{0, 0.1, 0.5, 1, 2, 5, 10, 30, 60, 120}

	TimeToFirstByteBuckets = []float64{0, 0.05, 0.1, 0.25, 0.5, 1, 2, 5}

	InlineBytesBuckets = []float64{1024, 4096, 16384, 65536, 262144, 786432}

	// RuleEvaluationDurationBuckets gives sub-millisecond resolution
	// since the engine runs synchronously on the request path; the
	// long tail captures pathological policies (deep groups, regex
	// catastrophes) without polluting the common case.
	RuleEvaluationDurationBuckets = []float64{0, 0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1}
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

	EventsPublishedTotal metric.Int64Counter
	EventsDroppedTotal   metric.Int64Counter
	EventsStashedTotal   metric.Int64Counter

	UnmappedFieldsTotal metric.Int64Counter
	ConfigReloadTotal   metric.Int64Counter
	UpstreamErrorsTotal metric.Int64Counter

	// ErrorResponsesTotal counts JSON error responses written by the
	// gateway middleware chain. Labels: layer (routing|handler|...),
	// code (machine-readable error name), status_code (HTTP status).
	ErrorResponsesTotal metric.Int64Counter

	RequestDuration        metric.Float64Histogram
	RequestTimeToFirstByte metric.Float64Histogram
	EventsInlineBytes      metric.Int64Histogram

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
		{MetricEventsPublishedTotal, "NATS bus publishes accepted.", "1", &m.EventsPublishedTotal},
		{MetricEventsDroppedTotal, "NATS bus events dropped because the queue was full or the bus was down.", "1", &m.EventsDroppedTotal},
		{MetricEventsStashedTotal, "Envelopes whose payload exceeded the inline threshold and was stashed in object storage.", "1", &m.EventsStashedTotal},
		{MetricUnmappedFieldsTotal, "Unknown fields detected on inbound provider payloads.", "1", &m.UnmappedFieldsTotal},
		{MetricConfigReloadTotal, "Configuration reload attempts.", "1", &m.ConfigReloadTotal},
		{MetricUpstreamErrorsTotal, "Errors returned by upstream providers.", "1", &m.UpstreamErrorsTotal},
		{MetricErrorResponsesTotal, "JSON error responses written by the gateway middleware chain.", "1", &m.ErrorResponsesTotal},
		{MetricRuleMatchesTotal, "Rules that matched on a request.", "1", &m.RuleMatchesTotal},
		{MetricRuleErrorsTotal, "Action execution failures during rule evaluation.", "1", &m.RuleErrorsTotal},
		{MetricGoroutinePanicsTotal, "Panics caught by safego.Go in background goroutines (process kept alive).", "1", &m.GoroutinePanicsTotal},
		{MetricRequestPanicsTotal, "Panics caught by the request-path recovery middleware (client got 500, process kept alive).", "1", &m.RequestPanicsTotal},
		{MetricAdminRequestsTotal, "Requests handled by the management-console listener (separate from data-plane requests).", "1", &m.AdminRequestsTotal},
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

	inlineBytes, err := meter.Int64Histogram(MetricEventsInlineBytes,
		metric.WithDescription("Size of inline event payloads on the NATS bus."),
		metric.WithUnit("By"),
		metric.WithExplicitBucketBoundaries(InlineBytesBuckets...),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: create histogram %s: %w", MetricEventsInlineBytes, err)
	}
	m.EventsInlineBytes = inlineBytes

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

	return m, nil
}
