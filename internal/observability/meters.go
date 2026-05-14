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
	MetricRequestsTotal        = "gateway.requests.total"
	MetricTokensInputTotal     = "gateway.tokens.input.total"
	MetricTokensOutputTotal    = "gateway.tokens.output.total"
	MetricTokensCachedTotal    = "gateway.tokens.cached.total"
	MetricEventsPublishedTotal = "gateway.events_published.total"
	MetricEventsDroppedTotal   = "gateway.events_dropped.total"
	MetricEventsStashedTotal   = "gateway.events_stashed.total"
	MetricUnmappedFieldsTotal  = "gateway.unmapped_fields.total"
	MetricConfigReloadTotal    = "gateway.config_reload.total"
	MetricUpstreamErrorsTotal  = "gateway.upstream_errors.total"

	MetricRequestDuration        = "gateway.request.duration"
	MetricRequestTimeToFirstByte = "gateway.request.time_to_first_byte"
	MetricEventsInlineBytes      = "gateway.events.inline_bytes"

	MetricActiveRequests = "gateway.active_requests"
)

// Histogram bucket boundaries. Defined as package-level vars (not consts)
// because Go does not permit composite literal constants; each slice is
// only read by NewMeters and never mutated.
var (
	RequestDurationBuckets = []float64{0, 0.1, 0.5, 1, 2, 5, 10, 30, 60, 120}

	TimeToFirstByteBuckets = []float64{0, 0.05, 0.1, 0.25, 0.5, 1, 2, 5}

	InlineBytesBuckets = []float64{1024, 4096, 16384, 65536, 262144, 786432}
)

// Meters bundles every gateway instrument so that callers can pass a
// single value through the middleware chain rather than reaching into a
// global meter on every emit. All fields are populated by NewMeters.
type Meters struct {
	RequestsTotal metric.Int64Counter

	TokensInputTotal  metric.Int64Counter
	TokensOutputTotal metric.Int64Counter
	TokensCachedTotal metric.Int64Counter

	EventsPublishedTotal metric.Int64Counter
	EventsDroppedTotal   metric.Int64Counter
	EventsStashedTotal   metric.Int64Counter

	UnmappedFieldsTotal metric.Int64Counter
	ConfigReloadTotal   metric.Int64Counter
	UpstreamErrorsTotal metric.Int64Counter

	RequestDuration        metric.Float64Histogram
	RequestTimeToFirstByte metric.Float64Histogram
	EventsInlineBytes      metric.Int64Histogram

	ActiveRequests metric.Int64UpDownCounter
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
		{MetricTokensCachedTotal, "Sum of provider-reported cached input tokens.", "1", &m.TokensCachedTotal},
		{MetricEventsPublishedTotal, "NATS bus publishes accepted.", "1", &m.EventsPublishedTotal},
		{MetricEventsDroppedTotal, "NATS bus events dropped because the queue was full or the bus was down.", "1", &m.EventsDroppedTotal},
		{MetricEventsStashedTotal, "Envelopes whose payload exceeded the inline threshold and was stashed in object storage.", "1", &m.EventsStashedTotal},
		{MetricUnmappedFieldsTotal, "Unknown fields detected on inbound provider payloads.", "1", &m.UnmappedFieldsTotal},
		{MetricConfigReloadTotal, "Configuration reload attempts.", "1", &m.ConfigReloadTotal},
		{MetricUpstreamErrorsTotal, "Errors returned by upstream providers.", "1", &m.UpstreamErrorsTotal},
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

	return m, nil
}
