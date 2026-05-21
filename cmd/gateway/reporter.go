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
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/bodycapture"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/tokens"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/observability/livefeed"
	"github.com/andyjmorgan/sluice-gateway/internal/observability/livefeed/accumulator"
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

// reporterFactory captures the dependencies needed by every per-request
// reporter observer — the publisher, logger, and meter handles are
// process-lifetime values. Factory() satisfies proxy.ObserverFactory so the
// forwarder can mint one fresh reporterRun per Forward call.
type reporterFactory struct {
	publisher *bus.Publisher
	logger    *slog.Logger
	meters    *observability.Meters
	liveFeed  *livefeed.Ring
	bodyStore *livefeed.BodyStore
}

func newReporterFactory(publisher *bus.Publisher, logger *slog.Logger, meters *observability.Meters, liveFeed *livefeed.Ring, bodyStore *livefeed.BodyStore) *reporterFactory {
	if logger == nil {
		logger = slog.Default()
	}
	return &reporterFactory{
		publisher: publisher,
		logger:    logger,
		meters:    meters,
		liveFeed:  liveFeed,
		bodyStore: bodyStore,
	}
}

// Factory returns a proxy.ObserverFactory bound to this factory's
// dependencies. The closure form makes the wiring in cmd/gateway main.go
// read as a single expression.
func (f *reporterFactory) Factory() proxy.ObserverFactory {
	return func(ctx context.Context, _ proxy.Destination) proxy.Observer {
		labels := observability.RequestLabelsFromContext(ctx)
		return &reporterRun{
			factory:       f,
			provider:      labels.Provider,
			endpoint:      labels.Endpoint,
			model:         labels.Model,
			configuration: labels.Configuration,
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

	// provider, endpoint, model, configuration are the routed labels
	// captured at construction time from the request context. They are
	// emitted on every metric this observer fires and on the bus event at
	// completion. Configuration carries the named-policy bundle the
	// request resolved against and has bounded cardinality (handful of
	// operator-defined names, never client-derived).
	provider      string
	endpoint      string
	model         string
	configuration string

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
	// middleware via OnRuleMatched. Merged with the context MatchBuffer
	// at OnComplete and fanned out to the bus publisher and the
	// in-process live-feed ring.
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
		attrs := withCompletionAttrs(ev.Provider, ev.Endpoint, ev.Model, r.configuration, status)
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

	// Tags are drained from the post-rule MutableState before the bus
	// publish so the gateway.request payload carries them. Per-tag
	// counter bumps happen here too — keeps the side-channel counter
	// in lockstep with the event field. Empty rules-disabled paths
	// (no MutableState on ctx) leave ev.Tags nil.
	r.populateTags(ctx, &ev)

	// Tokens are extracted from the captured response and bumped onto
	// gateway.tokens.* — must run before the bus publish so the
	// gateway.request payload carries TokensIn/Out/Cached/CacheCreation.
	// Gated on the response buffer being present; when bodies are
	// disabled (env.LiveFeedBodiesEnabled=false) tokens stay zero.
	r.populateTokens(ctx, &ev)

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

	matches := r.drainRuleMatches(ctx, ev.CorrelationID)
	r.publishRuleMatches(ctx, matches)
	entryID := r.appendLiveFeed(ev, matches)
	r.captureBody(ctx, entryID, ev)

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

// drainRuleMatches merges the observer's OnRuleMatched buffer with the
// per-request MatchBuffer the rules middleware writes through, stamps
// the correlation ID onto any record that was authored without one,
// and returns the consolidated slice. The MatchBuffer is consumed.
func (r *reporterRun) drainRuleMatches(ctx context.Context, correlationID string) []events.RuleMatched {
	matches := r.ruleMatches
	if buf := rules.MatchBufferFromContext(ctx); buf != nil {
		matches = append(matches, buf.Drain()...)
	}
	for i := range matches {
		if matches[i].CorrelationID == "" {
			matches[i].CorrelationID = correlationID
		}
	}
	return matches
}

// publishRuleMatches emits one gateway.rule.matched envelope per
// record. A nil publisher is a no-op; this keeps the bus fan-out
// optional without forcing every caller to gate the call site.
func (r *reporterRun) publishRuleMatches(ctx context.Context, matches []events.RuleMatched) {
	if r.factory.publisher == nil || len(matches) == 0 {
		return
	}
	for _, m := range matches {
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

// appendLiveFeed tees the completed request + its rule matches into the
// in-process ring that backs the admin console's live-messages pane.
// A nil ring is a no-op (live feed disabled via env var). Returns the
// minted event ID so subsequent steps (body capture, etc.) can key
// against the same identifier.
func (r *reporterRun) appendLiveFeed(ev events.Request, matches []events.RuleMatched) string {
	if r.factory.liveFeed == nil {
		return ""
	}
	hits := make([]livefeed.RuleHit, 0, len(matches))
	for _, m := range matches {
		hits = append(hits, livefeed.RuleHit{
			RuleName:       m.RuleName,
			ActionsApplied: append([]string(nil), m.ActionsApplied...),
			Terminated:     m.Terminated,
			ErrorMessage:   m.ErrorMessage,
		})
	}
	id := uuid.NewString()
	var tags []string
	if len(ev.Tags) > 0 {
		tags = append(tags, ev.Tags...)
	}
	r.factory.liveFeed.Append(livefeed.Entry{
		EventID:             id,
		At:                  time.Now().UTC(),
		CorrelationID:       ev.CorrelationID,
		Provider:            ev.Provider,
		Endpoint:            ev.Endpoint,
		Model:               ev.Model,
		Configuration:       r.configuration,
		StatusCode:          ev.StatusCode,
		DurationMs:          ev.DurationMs,
		Streaming:           ev.Streaming,
		UpstreamError:       ev.UpstreamError,
		TokensIn:            ev.TokensIn,
		TokensOut:           ev.TokensOut,
		TokensCached:        ev.TokensCached,
		TokensCacheCreation: ev.TokensCacheCreation,
		Tags:                tags,
		RulesMatched:        hits,
	})
	return id
}

// captureBody writes the per-request body envelope to the in-process
// body store keyed by entryID. No-ops when:
//   - bodies are disabled (nil store)
//   - the ring entry was not created (empty entryID, caused by a nil
//     ring — same disabled-feature path)
//
// Streaming responses run through the per-provider accumulator so the
// modal can render the assembled text + structured tool calls. Raw
// bytes are kept alongside (subject to the per-body cap) for the
// "raw chunks" toggle.
func (r *reporterRun) captureBody(ctx context.Context, entryID string, ev events.Request) {
	if r.factory.bodyStore == nil || entryID == "" {
		return
	}
	env := livefeed.BodyEnvelope{}

	if captured, ok := bodycapture.FromContext(ctx); ok {
		if len(captured.Raw) > 0 {
			env.Request = captured.Raw
			env.RequestTotalBytes = int64(len(captured.Raw))
		}
		if len(captured.Headers) > 0 {
			env.RequestHeaders = captured.Headers
		}
	}

	if buf, ok := livefeed.ResponseBufferFromContext(ctx); ok && buf != nil {
		env.Response = buf.Bytes()
		env.ResponseTotalBytes = buf.Total()
		env.ResponseTruncated = buf.Truncated()
		if h := buf.Headers(); len(h) > 0 {
			env.ResponseHeaders = h
		}
		if ev.Streaming && len(env.Response) > 0 {
			res := accumulator.Accumulate(ev.Provider, ev.Endpoint, env.Response)
			if res.Recognised {
				env.ResponseAssembled = string(res.Assembled)
				env.AssemblyPartial = res.Partial
			}
		}
	}

	r.factory.bodyStore.Put(entryID, env)
}

// populateTags pulls the post-rule MutableState off the request
// context, copies its Tags onto ev, and increments
// gateway.tags.applied.total once per tag. No state on context (rules
// middleware bypassed for this request) is a no-op leaving ev.Tags
// nil. The counter is labelled by tag name only — provider /
// endpoint / configuration intentionally omitted so cardinality stays
// bounded by the operator-defined tag library, not by the cross-
// product with everything else.
func (r *reporterRun) populateTags(ctx context.Context, ev *events.Request) {
	state := rules.MutableStateFromContext(ctx)
	if state == nil || len(state.Tags) == 0 {
		return
	}
	ev.Tags = append(ev.Tags, state.Tags...)
	if r.factory.meters == nil || r.factory.meters.TagsAppliedTotal == nil {
		return
	}
	for _, tag := range state.Tags {
		r.factory.meters.TagsAppliedTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("tag", tag)))
	}
}

// populateTokens reads the captured response (when present) and writes
// the extracted token snapshot onto ev plus the four gateway.tokens.*
// counters. Token capture is gated on the live-feed response buffer
// being on the context — when bodies are disabled there is nothing to
// parse and tokens stay zero.
//
// Counters are bumped only for non-zero buckets so a streaming response
// that omits include_usage (Recognised=false) and a response that
// reports e.g. cached=0 produce the same null observation on the
// gateway.tokens.cached.total series. The status_code attribute mirrors
// the requests/duration meter labelset so dashboards can join token
// rate with traffic rate by the same dimensions.
func (r *reporterRun) populateTokens(ctx context.Context, ev *events.Request) {
	buf, ok := livefeed.ResponseBufferFromContext(ctx)
	if !ok || buf == nil {
		return
	}
	raw := buf.Bytes()
	if len(raw) == 0 {
		return
	}
	snap := tokens.Extract(ev.Provider, ev.Endpoint, raw)
	if !snap.Recognised {
		return
	}
	ev.TokensIn = snap.Input
	ev.TokensOut = snap.Output
	ev.TokensCached = snap.Cached
	ev.TokensCacheCreation = snap.CacheCreation

	if r.factory.meters == nil {
		return
	}
	attrs := withCompletionAttrs(ev.Provider, ev.Endpoint, ev.Model, r.configuration, ev.StatusCode)
	if snap.Input > 0 && r.factory.meters.TokensInputTotal != nil {
		r.factory.meters.TokensInputTotal.Add(ctx, int64(snap.Input), attrs)
	}
	if snap.Output > 0 && r.factory.meters.TokensOutputTotal != nil {
		r.factory.meters.TokensOutputTotal.Add(ctx, int64(snap.Output), attrs)
	}
	if snap.Cached > 0 && r.factory.meters.TokensCachedTotal != nil {
		r.factory.meters.TokensCachedTotal.Add(ctx, int64(snap.Cached), attrs)
	}
	if snap.CacheCreation > 0 && r.factory.meters.TokensCacheCreationTotal != nil {
		r.factory.meters.TokensCacheCreationTotal.Add(ctx, int64(snap.CacheCreation), attrs)
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

// withCompletionAttrs extends the per-request attribute set with the
// resolved configuration and the final response status code. Used at
// OnComplete where status is authoritative (post-ErrorHandler) and the
// configuration name has already been resolved by auth, so the dashboard
// aggregator can slice traffic by both endpoint and configuration without
// going to logs.
func withCompletionAttrs(provider, endpoint, model, configuration string, status int) metric.MeasurementOption {
	return metric.WithAttributes(
		attribute.String("provider", provider),
		attribute.String("endpoint", endpoint),
		attribute.String("model", sanitiseModelLabel(model)),
		attribute.String("configuration", configuration),
		attribute.Int("status_code", status),
	)
}
