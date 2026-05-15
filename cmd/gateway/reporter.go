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

	"github.com/andyjmorgan/sluice-gateway/contracts/events"
	"github.com/andyjmorgan/sluice-gateway/internal/bus"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/proxy"
)

// Bus subject suffixes; the publisher emits the full subject as
// `gateway.<type>`.
const (
	eventTypeRequest     = "request"
	eventTypeRuleMatched = "rule.matched"
)

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

// requestLabels carries the routed (provider, endpoint, model) the handler
// resolves at destination-finalisation time so the per-request observer
// can label its metrics. It rides on context as an immutable value — a
// single writer (the handler) sets it before invoking the forwarder, and
// a single reader (the observer factory) consumes it once.
type requestLabels struct {
	provider string
	endpoint string
	model    string
}

type requestLabelsKey struct{}

func withRequestLabels(ctx context.Context, l requestLabels) context.Context {
	return context.WithValue(ctx, requestLabelsKey{}, l)
}

func requestLabelsFromContext(ctx context.Context) requestLabels {
	if ctx == nil {
		return requestLabels{}
	}
	l, _ := ctx.Value(requestLabelsKey{}).(requestLabels)
	return l
}

// reporterFactory captures the dependencies needed by every per-request
// reporter observer — the publisher, logger, and meter handles are
// process-lifetime values. Factory() satisfies proxy.ObserverFactory so the
// forwarder can mint one fresh reporterRun per Forward call.
type reporterFactory struct {
	publisher *bus.Publisher
	logger    *slog.Logger
	meters    *observability.Meters
}

func newReporterFactory(publisher *bus.Publisher, logger *slog.Logger, meters *observability.Meters) *reporterFactory {
	if logger == nil {
		logger = slog.Default()
	}
	return &reporterFactory{publisher: publisher, logger: logger, meters: meters}
}

// Factory returns a proxy.ObserverFactory bound to this factory's
// dependencies. The closure form makes the wiring in cmd/gateway main.go
// read as a single expression.
func (f *reporterFactory) Factory() proxy.ObserverFactory {
	return func(ctx context.Context, _ proxy.Destination) proxy.Observer {
		labels := requestLabelsFromContext(ctx)
		return &reporterRun{
			factory:  f,
			provider: labels.provider,
			endpoint: labels.endpoint,
			model:    labels.model,
		}
	}
}

// reporterRun is the per-request proxy.Observer produced by reporterFactory.
//
// All four lifecycle methods (OnRequestStart, OnResponseHeaders,
// OnUpstreamError, OnComplete) fire from the same goroutine: the request
// handler invoking proxy.Forwarder.Forward. OnResponseHeaders runs inside
// httputil.ReverseProxy.ModifyResponse and OnUpstreamError inside
// ErrorHandler — both are called from rp.ServeHTTP on the same goroutine.
// No internal locking is required to coordinate writes across the fields
// below.
type reporterRun struct {
	factory *reporterFactory

	// provider, endpoint, model are the routed labels captured at
	// construction time from the request context. They are emitted on
	// every metric this observer fires and on the bus event at completion.
	provider string
	endpoint string
	model    string

	// started is set in OnRequestStart and used both as the base for the
	// overall duration log and as the reference for the TTFB measurement.
	started time.Time

	// firstByte is set in OnResponseHeaders. Zero when the upstream
	// returned no headers (transport failure).
	firstByte time.Time

	// statusCode captures the upstream-reported status from
	// OnResponseHeaders. OnComplete prefers this over the final-written
	// status when non-zero so the event records what the upstream sent
	// even if ErrorHandler later overwrote the body.
	statusCode int

	streaming bool

	// upstream captures the transport error from OnUpstreamError; nil on
	// the success path.
	upstream error

	// ruleMatches buffers events.RuleMatched records driven by the rules
	// middleware via OnRuleMatched. Drained at OnComplete in the
	// telemetry PR; held here in PR 1 so callers can construct the
	// observer against the final lifecycle surface without behavioural
	// churn later.
	ruleMatches []events.RuleMatched
}

func (r *reporterRun) OnRequestStart(ctx context.Context, _ proxy.Destination) {
	r.started = time.Now()
	if r.factory.meters != nil && r.factory.meters.ActiveRequests != nil {
		r.factory.meters.ActiveRequests.Add(ctx, 1)
	}
}

func (r *reporterRun) OnResponseHeaders(ctx context.Context, status int, _ http.Header, streaming bool) {
	r.firstByte = time.Now()
	r.statusCode = status
	r.streaming = streaming

	if !r.started.IsZero() && r.factory.meters != nil && r.factory.meters.RequestTimeToFirstByte != nil {
		ttfb := time.Since(r.started).Seconds()
		r.factory.meters.RequestTimeToFirstByte.Record(ctx, ttfb, r.providerEndpointModelAttrs())
	}
}

func (r *reporterRun) OnUpstreamError(ctx context.Context, err error) {
	r.upstream = err
	if r.factory.meters != nil && r.factory.meters.UpstreamErrorsTotal != nil {
		r.factory.meters.UpstreamErrorsTotal.Add(ctx, 1, r.providerEndpointModelAttrs())
	}
}

func (r *reporterRun) OnComplete(ctx context.Context, status int, durationMs int64) {
	ev := events.Request{
		CorrelationID: observability.CorrelationIDFromContext(ctx),
		Provider:      r.provider,
		Endpoint:      r.endpoint,
		Model:         r.model,
		StatusCode:    status,
		DurationMs:    durationMs,
		Streaming:     r.streaming,
	}
	if r.statusCode != 0 {
		ev.StatusCode = r.statusCode
	}
	if r.upstream != nil {
		ev.UpstreamError = r.upstream.Error()
	}

	var ttfbMs int64
	if !r.started.IsZero() && !r.firstByte.IsZero() {
		ttfbMs = r.firstByte.Sub(r.started).Milliseconds()
	}

	if r.factory.meters != nil {
		attrs := withProviderEndpointModelStatus(ev.Provider, ev.Endpoint, ev.Model, status)
		if r.factory.meters.RequestsTotal != nil {
			r.factory.meters.RequestsTotal.Add(ctx, 1, attrs)
		}
		if r.factory.meters.RequestDuration != nil {
			r.factory.meters.RequestDuration.Record(ctx, float64(durationMs)/1000.0, attrs)
		}
		if r.factory.meters.ActiveRequests != nil {
			r.factory.meters.ActiveRequests.Add(ctx, -1)
		}
	}

	if r.factory.publisher != nil {
		payload, err := json.Marshal(ev)
		if err != nil {
			r.factory.logger.WarnContext(ctx, "reporter: marshal event", "err", err.Error())
		} else {
			r.factory.publisher.Publish(bus.Envelope{
				EventID:       uuid.NewString(),
				EventType:     eventTypeRequest,
				Timestamp:     time.Now().UTC(),
				Mode:          bus.PayloadInline,
				InlinePayload: payload,
			})
		}
	}

	r.flushRuleMatches(ctx, ev.CorrelationID)

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

// OnRuleMatched buffers a per-rule match record for batched emission
// at OnComplete. The rules middleware writes through this hook in
// addition to the context-stashed rules.MatchBuffer; both paths
// converge at flushRuleMatches.
func (r *reporterRun) OnRuleMatched(_ context.Context, match events.RuleMatched) {
	r.ruleMatches = append(r.ruleMatches, match)
}

// flushRuleMatches drains the per-request MatchBuffer (the rules
// middleware writes there directly) and merges it with the
// observer's OnRuleMatched buffer, then publishes one
// gateway.rule.matched envelope per record. CorrelationID is
// stamped here so the evaluator can stay ignorant of observability
// plumbing.
func (r *reporterRun) flushRuleMatches(ctx context.Context, correlationID string) {
	matches := r.ruleMatches
	if buf := rules.MatchBufferFromContext(ctx); buf != nil {
		matches = append(matches, buf.Drain()...)
	}
	if len(matches) == 0 {
		return
	}
	if r.factory.publisher == nil {
		return
	}
	for _, m := range matches {
		if m.CorrelationID == "" {
			m.CorrelationID = correlationID
		}
		payload, err := json.Marshal(m)
		if err != nil {
			r.factory.logger.WarnContext(ctx, "reporter: marshal rule match", "err", err.Error())
			continue
		}
		r.factory.publisher.Publish(bus.Envelope{
			EventID:       uuid.NewString(),
			EventType:     eventTypeRuleMatched,
			Timestamp:     time.Now().UTC(),
			Mode:          bus.PayloadInline,
			InlinePayload: payload,
		})
	}
}

// providerEndpointModelAttrs builds the (provider, endpoint, model)
// attribute set used by per-request meters that fire mid-request
// (TimeToFirstByte, UpstreamErrors). Values are owned by the observer
// struct itself; see the OnComplete helper for the post-completion variant
// that also carries the final status code.
func (r *reporterRun) providerEndpointModelAttrs() metric.MeasurementOption {
	return metric.WithAttributes(
		attribute.String("provider", r.provider),
		attribute.String("endpoint", r.endpoint),
		attribute.String("model", sanitiseModelLabel(r.model)),
	)
}

// withProviderEndpointModelStatus extends the per-request attribute set
// with the final response status code. Used at OnComplete where status is
// authoritative (post-ErrorHandler) so it can't be read from the observer
// struct fields alone.
func withProviderEndpointModelStatus(provider, endpoint, model string, status int) metric.MeasurementOption {
	return metric.WithAttributes(
		attribute.String("provider", provider),
		attribute.String("endpoint", endpoint),
		attribute.String("model", sanitiseModelLabel(model)),
		attribute.Int("status_code", status),
	)
}
