package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
	"github.com/andyjmorgan/sluice-gateway/contracts/events"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/auth"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/bodycapture"
	resiliencemw "github.com/andyjmorgan/sluice-gateway/internal/middleware/resilience"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/tokens"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/observability/livefeed"
	"github.com/andyjmorgan/sluice-gateway/internal/observability/livefeed/accumulator"
	"github.com/andyjmorgan/sluice-gateway/internal/proxy"
	"github.com/andyjmorgan/sluice-gateway/internal/spool"
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
// reporter observer. Spool is the destination side-channel; resolved
// drives per-configuration ConnectorBindings lookups; the meters /
// liveFeed / bodyStore handles are process-lifetime values.
//
// instanceID + seq stamp every Record so consumers can sort by
// (TsNs, instance_id, seq) per the design note's sort key.
type reporterFactory struct {
	spool     *spool.Spool
	resolved  *config.ResolvedConfig
	logger    *slog.Logger
	meters    *observability.Meters
	liveFeed  *livefeed.Ring
	bodyStore *livefeed.BodyStore

	instanceID string
	seq        atomic.Uint64
}

func newReporterFactory(s *spool.Spool, resolved *config.ResolvedConfig, logger *slog.Logger, meters *observability.Meters, liveFeed *livefeed.Ring, bodyStore *livefeed.BodyStore) *reporterFactory {
	if logger == nil {
		logger = slog.Default()
	}
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "local"
	}
	return &reporterFactory{
		spool:      s,
		resolved:   resolved,
		logger:     logger,
		meters:     meters,
		liveFeed:   liveFeed,
		bodyStore:  bodyStore,
		instanceID: hostname,
	}
}

// nextSeq returns the next per-instance monotonic counter. Stamped on
// every Record as the (TsNs, instance_id, seq) tiebreaker.
func (f *reporterFactory) nextSeq() uint64 { return f.seq.Add(1) }

// Factory returns a proxy.ObserverFactory bound to this factory's
// dependencies. The closure form makes the wiring in cmd/gateway main.go
// read as a single expression.
func (f *reporterFactory) Factory() proxy.ObserverFactory {
	return func(ctx context.Context, _ proxy.Destination) proxy.Observer {
		labels := observability.RequestLabelsFromContext(ctx)
		apiKeyName := ""
		if ar, ok := auth.FromContext(ctx); ok && ar.APIKey != nil {
			apiKeyName = ar.APIKey.Name
		}
		return &reporterRun{
			factory:       f,
			provider:      labels.Provider,
			endpoint:      labels.Endpoint,
			model:         labels.Model,
			method:        labels.Method,
			configuration: labels.Configuration,
			apiKeyName:    apiKeyName,
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

	// provider, endpoint, model, configuration, apiKeyName are the
	// routed labels captured at construction time from the request
	// context. Emitted on every metric this observer fires and on the
	// Record at completion.
	provider      string
	endpoint      string
	model         string
	configuration string
	apiKeyName    string

	// method is the inbound HTTP verb captured at construction time.
	// Stamped on the event / Record / live-feed Entry but never on a
	// metric label.
	method string

	// started is set in OnRequestStart and used both as the base for
	// the overall duration log and as the reference for the TTFB
	// measurement and Record.TsNs.
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

	// upstream captures the transport error from OnUpstreamError; nil
	// on the success path.
	upstream error

	// ruleMatches buffers events.RuleMatched records driven by the
	// rules middleware via OnRuleMatched. Merged with the context
	// MatchBuffer at OnComplete and folded into Record.RulesFired.
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

// OnRuleMatched buffers a per-rule match record for batched emission at
// OnComplete.
func (r *reporterRun) OnRuleMatched(_ context.Context, match events.RuleMatched) {
	r.ruleMatches = append(r.ruleMatches, match)
}

func (r *reporterRun) OnComplete(ctx context.Context, status int, durationMs int64) {
	ev := events.Request{
		CorrelationID: observability.CorrelationIDFromContext(ctx),
		Provider:      r.provider,
		Endpoint:      r.endpoint,
		Model:         r.model,
		Method:        r.method,
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

	if r.factory.meters != nil && r.factory.meters.ActiveRequests != nil {
		r.factory.meters.ActiveRequests.Add(ctx, -1)
	}

	r.populateTags(ctx, &ev)
	r.populateTokens(ctx, &ev)

	// Multi-attempt orchestration suppresses the per-attempt terminal
	// emit: the resilience orchestrator installs an AttemptBuffer on
	// ctx, drives FireTerminal at the end of the run, and the closure
	// we register here produces the single consolidated emit.
	if abuf := resiliencemw.AttemptBufferFromContext(ctx); abuf != nil {
		abuf.SetTerminalPublish(func(overallDurationMs int64, finalStatus int) {
			ev.DurationMs = overallDurationMs
			ev.StatusCode = finalStatus
			ev.PolicyRef = abuf.PolicyRef()
			ev.Attempts = abuf.Drain()
			r.recordPerRequestMetrics(ctx, ev)
			r.publishTerminalEvent(ctx, ev, ttfbMs)
		})
		return
	}

	r.recordPerRequestMetrics(ctx, ev)
	r.publishTerminalEvent(ctx, ev, ttfbMs)
}

func (r *reporterRun) recordPerRequestMetrics(ctx context.Context, ev events.Request) {
	if r.factory.meters == nil {
		return
	}
	attrs := withCompletionAttrs(ev.Provider, ev.Endpoint, ev.Model, r.configuration, ev.StatusCode)
	if r.factory.meters.RequestsTotal != nil {
		r.factory.meters.RequestsTotal.Add(ctx, 1, attrs)
	}
	if r.factory.meters.RequestDuration != nil {
		r.factory.meters.RequestDuration.Record(ctx, float64(ev.DurationMs)/1000.0, attrs)
	}
}

// publishTerminalEvent is the terminal half of OnComplete:
//  1. Drain the per-request rule-match buffer + ctx MatchBuffer.
//  2. Tee the completed request + matches into the in-process
//     live-feed ring that backs the admin console's messages pane.
//  3. Capture the request + response bodies for the admin body endpoint.
//  4. Enqueue a Record to the spool for every connector binding the
//     resolved configuration declares.
//  5. Emit the structured request-completed log line.
func (r *reporterRun) publishTerminalEvent(ctx context.Context, ev events.Request, ttfbMs int64) {
	matches := r.drainRuleMatches(ctx, ev.CorrelationID)
	entryID := r.appendLiveFeed(ev, matches)
	r.captureBody(ctx, entryID, ev)
	r.enqueueRecord(ctx, ev, matches)

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
		"policy_ref", ev.PolicyRef,
		"attempts", len(ev.Attempts),
	)
}

// enqueueRecord builds a contracts/connector.Record from the
// captured request/response + the rule + token snapshots, and
// evaluates each binding the resolved configuration declares:
// sampling, filter, oversize. Records that survive land on the
// connector's spool track. No-ops when the spool is nil (no
// connectors configured) or the configuration is unknown.
func (r *reporterRun) enqueueRecord(ctx context.Context, ev events.Request, matches []events.RuleMatched) {
	if r.factory.spool == nil {
		return
	}
	cfg := r.factory.resolved.ConfigurationIndex[r.configuration]
	if cfg == nil || len(cfg.ConnectorBindings) == 0 {
		return
	}
	rec := r.buildRecord(ctx, ev, matches)
	for _, b := range cfg.ConnectorBindings {
		modified, ship := evaluateBinding(rec, b)
		if !ship {
			continue
		}
		r.factory.spool.Enqueue(modified, b.Connector)
	}
}

// buildRecord maps the in-flight reporter state into the connector
// wire format. Body bytes are pulled from the live-feed capture so a
// configuration with bindings + no live-feed capture would lose body
// payloads — Validate at config-load could enforce this if the
// follow-up shows it matters; today the gateway always wires the
// live-feed bodyStore unless explicitly disabled.
func (r *reporterRun) buildRecord(ctx context.Context, ev events.Request, matches []events.RuleMatched) cc.Record {
	tsNs := r.started.UnixNano()
	if r.started.IsZero() {
		tsNs = time.Now().UnixNano()
	}

	rec := cc.Record{
		V:             1,
		ID:            uuid.NewString(),
		TsNs:          tsNs,
		Seq:           r.factory.nextSeq(),
		InstanceID:    r.factory.instanceID,
		CorrelationID: ev.CorrelationID,
		Configuration: r.configuration,
		APIKeyName:    r.apiKeyName,
		Provider:      ev.Provider,
		Endpoint:      ev.Endpoint,
		Model:         ev.Model,
		Tags:          ev.Tags,
		Request: cc.RequestPart{
			Method: ev.Method,
		},
		Response: cc.ResponsePart{
			Status: ev.StatusCode,
		},
		UpstreamStatus: ev.StatusCode,
		UpstreamError:  ev.UpstreamError,
		PolicyRef:      ev.PolicyRef,
		SchemaVersion:  cc.SchemaVersion,
	}
	if len(ev.Attempts) > 0 {
		rec.Attempts = make([]cc.Attempt, 0, len(ev.Attempts))
		for _, a := range ev.Attempts {
			rec.Attempts = append(rec.Attempts, cc.Attempt{
				Target:      a.Target,
				StartedAtNs: a.StartedAt.UnixNano(),
				DurationMs:  a.DurationMs,
				StatusCode:  a.StatusCode,
				Error:       a.Error,
				Outcome:     a.Outcome,
			})
		}
	}

	if ev.TokensIn > 0 || ev.TokensOut > 0 || ev.TokensCached > 0 || ev.TokensCacheCreation > 0 {
		rec.Tokens = &cc.Tokens{
			Input:         ev.TokensIn,
			Output:        ev.TokensOut,
			Cached:        ev.TokensCached,
			CacheCreation: ev.TokensCacheCreation,
		}
	}

	if len(matches) > 0 {
		rec.RulesFired = make([]cc.RuleFired, 0, len(matches))
		for _, m := range matches {
			rec.RulesFired = append(rec.RulesFired, cc.RuleFired{
				Name:           m.RuleName,
				ActionsApplied: append([]string(nil), m.ActionsApplied...),
				Terminated:     m.Terminated,
				ErrorMessage:   m.ErrorMessage,
			})
		}
	}

	if captured, ok := bodycapture.FromContext(ctx); ok {
		if len(captured.Raw) > 0 {
			rec.Request.Body = jsonBodyOrEscaped(captured.Raw)
			rec.Request.BodyBytes = len(captured.Raw)
			sum := sha256.Sum256(captured.Raw)
			rec.Request.BodySha256 = hex.EncodeToString(sum[:])
		}
		if len(captured.Headers) > 0 {
			rec.Request.Headers = map[string]string{}
			for k, vs := range captured.Headers {
				if len(vs) > 0 {
					rec.Request.Headers[k] = vs[0]
				}
			}
		}
	}

	if buf, ok := livefeed.ResponseBufferFromContext(ctx); ok && buf != nil {
		body := buf.Bytes()
		if len(body) > 0 {
			rec.Response.Body = jsonBodyOrEscaped(body)
			rec.Response.BodyBytes = int(buf.Total())
			sum := sha256.Sum256(body)
			rec.Response.BodySha256 = hex.EncodeToString(sum[:])
		}
		if h := buf.Headers(); len(h) > 0 {
			rec.Response.Headers = map[string]string{}
			for k, vs := range h {
				if len(vs) > 0 {
					rec.Response.Headers[k] = vs[0]
				}
			}
		}
		if !r.firstByte.IsZero() {
			rec.Response.FirstByteNs = r.firstByte.UnixNano()
		}
	}
	if r.streaming {
		// We don't track per-chunk count yet; setting to 1 is enough
		// for downstream consumers (and the e2e harness) to recognise
		// the response as streaming. A follow-up can plumb the actual
		// count out of the proxy's stream writer wrapper.
		rec.Response.StreamChunks = 1
	}
	if !r.started.IsZero() {
		rec.Response.LastByteNs = r.started.Add(time.Duration(ev.DurationMs) * time.Millisecond).UnixNano()
	}

	return rec
}

// drainRuleMatches merges the observer's OnRuleMatched buffer with the
// per-request MatchBuffer the rules middleware writes through, stamps
// the correlation ID onto any record authored without one, and
// returns the consolidated slice. The MatchBuffer is consumed.
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

// appendLiveFeed tees the completed request + its rule matches into
// the in-process ring that backs the admin console's live-messages
// pane. A nil ring is a no-op (live feed disabled via env var).
// Returns the minted event ID so subsequent steps (body capture)
// can key against the same identifier.
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
	var attempts []livefeed.AttemptHit
	if len(ev.Attempts) > 0 {
		attempts = make([]livefeed.AttemptHit, 0, len(ev.Attempts))
		for _, a := range ev.Attempts {
			attempts = append(attempts, livefeed.AttemptHit{
				Target:     a.Target,
				StartedAt:  a.StartedAt,
				DurationMs: a.DurationMs,
				StatusCode: a.StatusCode,
				Error:      a.Error,
				Outcome:    a.Outcome,
			})
		}
	}
	r.factory.liveFeed.Append(livefeed.Entry{
		EventID:             id,
		At:                  time.Now().UTC(),
		CorrelationID:       ev.CorrelationID,
		Provider:            ev.Provider,
		Endpoint:            ev.Endpoint,
		Model:               ev.Model,
		Method:              ev.Method,
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
		PolicyRef:           ev.PolicyRef,
		Attempts:            attempts,
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
// nil.
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
// the extracted token snapshot onto ev, the gen_ai.client.token.usage
// histogram (input/output, keyed by gen_ai.token.type), and the two
// sluice.tokens.* cache counters.
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
	// base carries the request dimensions without status. The full-slice
	// expression caps base's capacity so each token.type append below
	// allocates a fresh backing array rather than aliasing base.
	base := requestDimensionAttrs(ev.Provider, ev.Endpoint, ev.Model, r.configuration)
	base = base[:len(base):len(base)]
	if snap.Input > 0 && r.factory.meters.TokenUsage != nil {
		r.factory.meters.TokenUsage.Record(ctx, int64(snap.Input),
			metric.WithAttributes(append(base, attribute.String(observability.AttrGenAITokenType, observability.TokenTypeInput))...))
	}
	if snap.Output > 0 && r.factory.meters.TokenUsage != nil {
		r.factory.meters.TokenUsage.Record(ctx, int64(snap.Output),
			metric.WithAttributes(append(base, attribute.String(observability.AttrGenAITokenType, observability.TokenTypeOutput))...))
	}
	if snap.Cached > 0 && r.factory.meters.TokensCachedTotal != nil {
		r.factory.meters.TokensCachedTotal.Add(ctx, int64(snap.Cached), metric.WithAttributes(base...))
	}
	if snap.CacheCreation > 0 && r.factory.meters.TokensCacheCreationTotal != nil {
		r.factory.meters.TokensCacheCreationTotal.Add(ctx, int64(snap.CacheCreation), metric.WithAttributes(base...))
	}
}

// providerEndpointModelAttrs builds the gen_ai dimension attributes used
// by per-request meters that fire mid-request (TimeToFirstByte). Status
// is unknown at this point, so it carries only the request dimensions.
func (r *reporterRun) providerEndpointModelAttrs() metric.MeasurementOption {
	return metric.WithAttributes(
		attribute.String(observability.AttrGenAIProviderName, r.provider),
		attribute.String(observability.AttrGenAIRequestModel, sanitiseModelLabel(r.model)),
		attribute.String(observability.AttrGenAIOperationName, observability.OperationNameForEndpoint(r.endpoint)),
		attribute.String(observability.AttrSluiceEndpoint, r.endpoint),
	)
}

// jsonBodyOrEscaped marshals body bytes onto a Record's inline
// `body` field. JSON content passes through as-is (the consumer sees
// a native JSON object/array); non-JSON content (SSE streams, plain
// text) is wrapped as a JSON string so json.RawMessage's
// MarshalJSON validator accepts it. The shape mirrors what a CDN
// audit pipeline would expect: `body` is "the request body, decoded
// as JSON if possible, otherwise the raw bytes as a JSON string".
func jsonBodyOrEscaped(b []byte) json.RawMessage {
	if json.Valid(b) {
		return json.RawMessage(b)
	}
	encoded, err := json.Marshal(string(b))
	if err != nil {
		// json.Marshal of a string never fails for valid UTF-8; for
		// non-UTF-8 it fails gracefully and we fall back to the
		// empty-body case.
		return nil
	}
	return json.RawMessage(encoded)
}

// requestDimensionAttrs builds the gen_ai + sluice dimension attributes
// shared by every per-request instrument: provider, request model, the
// coarse gen_ai.operation.name, the precise sluice.endpoint route, and
// the resolved configuration. Status is layered on separately because
// token instruments don't carry it.
func requestDimensionAttrs(provider, endpoint, model, configuration string) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String(observability.AttrGenAIProviderName, provider),
		attribute.String(observability.AttrGenAIRequestModel, sanitiseModelLabel(model)),
		attribute.String(observability.AttrGenAIOperationName, observability.OperationNameForEndpoint(endpoint)),
		attribute.String(observability.AttrSluiceEndpoint, endpoint),
		attribute.String(observability.AttrSluiceConfiguration, configuration),
	}
}

// withCompletionAttrs extends the request dimensions with the HTTP status
// code, and on the failure path the spec error.type. error.type is an
// open enum, so the status code string is a spec-legal value; it is set
// only for 4xx/5xx so the attribute's presence itself marks a failure.
func withCompletionAttrs(provider, endpoint, model, configuration string, status int) metric.MeasurementOption {
	attrs := requestDimensionAttrs(provider, endpoint, model, configuration)
	attrs = append(attrs, attribute.Int(observability.AttrHTTPResponseStatusCode, status))
	if status >= 400 {
		attrs = append(attrs, attribute.String(observability.AttrErrorType, strconv.Itoa(status)))
	}
	return metric.WithAttributes(attrs...)
}
