package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
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

	StatusCode int `json:"status_code"`

	DurationMs int64 `json:"duration_ms"`

	Streaming bool `json:"streaming,omitempty"`

	UpstreamError string `json:"upstream_error,omitempty"`
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
		o.meters.RequestTimeToFirstByte.Record(ctx, ttfb, withProviderEndpoint(ctx))
	}
}

func (o *reporterObserver) OnUpstreamError(ctx context.Context, err error) {
	if s := reqStateFromContext(ctx); s != nil {
		s.mu.Lock()
		s.upstream = err
		s.mu.Unlock()
	}
	if o.meters != nil && o.meters.UpstreamErrorsTotal != nil {
		o.meters.UpstreamErrorsTotal.Add(ctx, 1, withProviderEndpoint(ctx))
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
		attrs := withProviderEndpointStatus(ev.Provider, ev.Endpoint, status)
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
		"streaming", ev.Streaming,
		"ttfb_ms", ttfbMs,
		"upstream_error", ev.UpstreamError,
	)
}

func withProviderEndpoint(ctx context.Context) metric.MeasurementOption {
	s := reqStateFromContext(ctx)
	if s == nil {
		return metric.WithAttributes()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return metric.WithAttributes(
		attribute.String("provider", s.provider),
		attribute.String("endpoint", s.endpoint),
	)
}

func withProviderEndpointStatus(provider, endpoint string, status int) metric.MeasurementOption {
	return metric.WithAttributes(
		attribute.String("provider", provider),
		attribute.String("endpoint", endpoint),
		attribute.Int("status_code", status),
	)
}
