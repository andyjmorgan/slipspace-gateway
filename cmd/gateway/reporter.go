package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/andyjmorgan/sluice-gateway/internal/bus"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/proxy"
)

// eventTypeRequest is the bus subject suffix for end-of-pipeline request
// records. The publisher will emit the full subject as `gateway.request`.
const eventTypeRequest = "request"

// requestEvent is the JSON-encoded payload for the v1.0 request envelope.
// The control-plane will eventually consume a richer schema; this shape
// covers the audit fields the dashboards need on day one.
type requestEvent struct {
	CorrelationID string `json:"correlation_id,omitempty"`

	Provider string `json:"provider,omitempty"`

	Endpoint string `json:"endpoint,omitempty"`

	// Model is the outbound model identifier — the model the gateway
	// forwarded to upstream, after routing and (in v1.1) any rule
	// mutations. Empty for endpoints that carry no model.
	Model string `json:"model,omitempty"`

	StatusCode int `json:"status_code"`

	DurationMs int64 `json:"duration_ms"`

	Streaming bool `json:"streaming,omitempty"`

	UpstreamError string `json:"upstream_error,omitempty"`
}

// modelLabelMaxLen caps the length of the `model` metric label value to
// keep cardinality bounded against a misbehaving client that injects long
// or unique strings. Real-world provider model names top out around 30
// characters; 80 leaves headroom for dated variants
// (gpt-4o-mini-2024-07-18, claude-haiku-4-5-20251001) without risking
// runaway label series.
const modelLabelMaxLen = 80

// modelLabelOther is the bucket label used when an inbound model string
// fails the length cap. We deliberately collapse pathological values into
// a single series rather than dropping the request — operators still see
// the request count, just without a useful breakdown.
const modelLabelOther = "other"

// sanitiseModelLabel normalises a raw model identifier into a value safe
// for use as a Prometheus label. Empty input is preserved (passthrough
// endpoints like /v1/models have no model in scope); overlong input
// collapses to modelLabelOther so cardinality stays bounded.
func sanitiseModelLabel(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}
	if len(v) > modelLabelMaxLen {
		return modelLabelOther
	}
	return v
}

// reporterObserver bridges proxy.Forwarder lifecycle hooks into bus.Envelope
// publishes for end-of-request reporting and OTel meters for live counters.
// All per-request state is read from reqState on the request context — a
// single observer instance serves every request.
type reporterObserver struct {
	publisher *bus.Publisher
	logger    *slog.Logger
	meters    *observability.Meters
}

func newReporterObserver(publisher *bus.Publisher, logger *slog.Logger, meters *observability.Meters) *reporterObserver {
	if logger == nil {
		logger = slog.Default()
	}
	return &reporterObserver{publisher: publisher, logger: logger, meters: meters}
}

func (o *reporterObserver) OnRequestStart(ctx context.Context, _ proxy.Destination) {
	if s := reqStateFromContext(ctx); s != nil {
		s.mu.Lock()
		s.started = time.Now()
		s.mu.Unlock()
	}
	if o.meters != nil && o.meters.ActiveRequests != nil {
		o.meters.ActiveRequests.Add(ctx, 1)
	}
}

func (o *reporterObserver) OnResponseHeaders(ctx context.Context, status int, _ http.Header, streaming bool) {
	var startedAt time.Time
	if s := reqStateFromContext(ctx); s != nil {
		s.mu.Lock()
		startedAt = s.started
		s.firstByte = time.Now()
		s.statusCode = status
		s.streaming = streaming
		s.mu.Unlock()
	}

	if !startedAt.IsZero() && o.meters != nil && o.meters.RequestTimeToFirstByte != nil {
		ttfb := time.Since(startedAt).Seconds()
		o.meters.RequestTimeToFirstByte.Record(ctx, ttfb, withProviderEndpointModel(ctx))
	}
}

func (o *reporterObserver) OnUpstreamError(ctx context.Context, err error) {
	if s := reqStateFromContext(ctx); s != nil {
		s.mu.Lock()
		s.upstream = err
		s.mu.Unlock()
	}
	if o.meters != nil && o.meters.UpstreamErrorsTotal != nil {
		o.meters.UpstreamErrorsTotal.Add(ctx, 1, withProviderEndpointModel(ctx))
	}
}

func (o *reporterObserver) OnComplete(ctx context.Context, status int, durationMs int64) {
	state := reqStateFromContext(ctx)
	var ev requestEvent
	ev.StatusCode = status
	ev.DurationMs = durationMs
	ev.CorrelationID = observability.CorrelationIDFromContext(ctx)
	var ttfbMs int64
	if state != nil {
		state.mu.Lock()
		ev.Provider = state.provider
		ev.Endpoint = state.endpoint
		ev.Model = state.model
		ev.Streaming = state.streaming
		if state.statusCode != 0 {
			ev.StatusCode = state.statusCode
		}
		if state.upstream != nil {
			ev.UpstreamError = state.upstream.Error()
		}
		if !state.started.IsZero() && !state.firstByte.IsZero() {
			ttfbMs = state.firstByte.Sub(state.started).Milliseconds()
		}
		state.mu.Unlock()
	}

	if o.meters != nil {
		attrs := withProviderEndpointModelStatus(ev.Provider, ev.Endpoint, ev.Model, status)
		if o.meters.RequestsTotal != nil {
			o.meters.RequestsTotal.Add(ctx, 1, attrs)
		}
		if o.meters.RequestDuration != nil {
			o.meters.RequestDuration.Record(ctx, float64(durationMs)/1000.0, attrs)
		}
		if o.meters.ActiveRequests != nil {
			o.meters.ActiveRequests.Add(ctx, -1)
		}
	}

	if o.publisher != nil {
		payload, err := json.Marshal(ev)
		if err != nil {
			o.logger.WarnContext(ctx, "reporter: marshal event", "err", err.Error())
		} else {
			o.publisher.Publish(bus.Envelope{
				EventID:       uuid.NewString(),
				EventType:     eventTypeRequest,
				Timestamp:     time.Now().UTC(),
				Mode:          bus.PayloadInline,
				InlinePayload: payload,
			})
		}
	}

	logger := observability.FromContext(ctx)
	logger.InfoContext(ctx, "request completed",
		"status_code", ev.StatusCode,
		"duration_ms", ev.DurationMs,
		"provider", ev.Provider,
		"endpoint", ev.Endpoint,
		"model", ev.Model,
		"streaming", ev.Streaming,
		"ttfb_ms", ttfbMs,
		"upstream_error", ev.UpstreamError,
	)
}

// withProviderEndpointModel builds the (provider, endpoint, model)
// attribute set used by per-request meters that fire mid-request
// (TimeToFirstByte, UpstreamErrors). Values are read off the reqState
// stashed by the handler at destination-finalisation time so they reflect
// the routed-and-rules-applied destination, not the inbound request.
func withProviderEndpointModel(ctx context.Context) metric.MeasurementOption {
	s := reqStateFromContext(ctx)
	if s == nil {
		return metric.WithAttributes()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return metric.WithAttributes(
		attribute.String("provider", s.provider),
		attribute.String("endpoint", s.endpoint),
		attribute.String("model", sanitiseModelLabel(s.model)),
	)
}

// withProviderEndpointModelStatus extends withProviderEndpointModel with
// the final response status code. Used at OnComplete where status is
// authoritative (post-ErrorHandler) so it can't be read from reqState.
func withProviderEndpointModelStatus(provider, endpoint, model string, status int) metric.MeasurementOption {
	return metric.WithAttributes(
		attribute.String("provider", provider),
		attribute.String("endpoint", endpoint),
		attribute.String("model", sanitiseModelLabel(model)),
		attribute.Int("status_code", status),
	)
}
