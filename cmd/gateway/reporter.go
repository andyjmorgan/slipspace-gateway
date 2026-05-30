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
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
	"github.com/andyjmorgan/sluice-gateway/contracts/events"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/contentredact"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/auth"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/bodycapture"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/genaiattr"
	resiliencemw "github.com/andyjmorgan/sluice-gateway/internal/middleware/resilience"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/sseframe"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/tokens"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/observability/livefeed"
	"github.com/andyjmorgan/sluice-gateway/internal/observability/livefeed/accumulator"
	"github.com/andyjmorgan/sluice-gateway/internal/observability/unmapped"
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
// reporter observer. Spool is the destination side-channel; store drives
// per-configuration ConnectorBindings lookups via store.Snapshot at
// record-build time; the meters / liveFeed / bodyStore handles are
// process-lifetime values.
//
// instanceID + seq stamp every Record so consumers can sort by
// (TsNs, instance_id, seq) per the design note's sort key.
type reporterFactory struct {
	spool     *spool.Spool
	store     *config.Store
	logger    *slog.Logger
	meters    *observability.Meters
	liveFeed  *livefeed.Ring
	bodyStore *livefeed.BodyStore

	// tracer emits the per-request span at completion. May be nil (no
	// OTLP endpoint configured, or test contexts); emitTrace no-ops then.
	tracer trace.Tracer

	// eventLogger emits the GenAI events (operation details, exception) at
	// completion. May be nil (no OTLP endpoint, or test contexts); event
	// emission no-ops then.
	eventLogger otellog.Logger

	// captureContent gates emission of the bounded prompt/response content
	// on the operation-details event (SLUICE_OTEL_CAPTURE_CONTENT). Off by
	// default — content stays in the connector spool (invariant #4).
	captureContent bool

	// caps bounds the bytes the captured-content emitters include on the
	// span and operation-details event. Resolved at startup from
	// admin.yaml's telemetry block; the reporter reads it on every
	// content-bearing request when captureContent is on. A cap of 0 in
	// any field means "no cap" — see contracts/config/telemetry.go for
	// the absent-vs-explicit-zero semantics.
	caps contractsconfig.ResolvedContentCaps

	instanceID string
	seq        atomic.Uint64
}

func newReporterFactory(s *spool.Spool, store *config.Store, logger *slog.Logger, meters *observability.Meters, liveFeed *livefeed.Ring, bodyStore *livefeed.BodyStore, tracer trace.Tracer, eventLogger otellog.Logger, captureContent bool, caps contractsconfig.ResolvedContentCaps) *reporterFactory {
	if logger == nil {
		logger = slog.Default()
	}
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "local"
	}
	return &reporterFactory{
		spool:          s,
		store:          store,
		logger:         logger,
		meters:         meters,
		liveFeed:       liveFeed,
		bodyStore:      bodyStore,
		tracer:         tracer,
		eventLogger:    eventLogger,
		captureContent: captureContent,
		caps:           caps,
		instanceID:     hostname,
	}
}

// nextSeq returns the next per-instance monotonic counter. Stamped on
// every Record as the (TsNs, instance_id, seq) tiebreaker.
func (f *reporterFactory) nextSeq() uint64 { return f.seq.Add(1) }

// upstreamServerAddrPort extracts the server.address / server.port pair —
// the upstream host:port this request is forwarded to — from the resolved
// Destination. When the URL carries no explicit port the scheme default is
// used so server.port is populated alongside the address (the spec makes
// server.port conditionally required whenever server.address is set).
func upstreamServerAddrPort(dest proxy.Destination) (string, int) {
	if dest.UpstreamURL == nil {
		return "", 0
	}
	host := dest.UpstreamURL.Hostname()
	if host == "" {
		return "", 0
	}
	if p := dest.UpstreamURL.Port(); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			return host, n
		}
		return host, 0
	}
	switch dest.UpstreamURL.Scheme {
	case "https":
		return host, 443
	case "http":
		return host, 80
	}
	return host, 0
}

// Factory returns a proxy.ObserverFactory bound to this factory's
// dependencies. The closure form makes the wiring in cmd/gateway main.go
// read as a single expression.
func (f *reporterFactory) Factory() proxy.ObserverFactory {
	return func(ctx context.Context, dest proxy.Destination) proxy.Observer {
		labels := observability.RequestLabelsFromContext(ctx)
		apiKeyName := ""
		if ar, ok := auth.FromContext(ctx); ok && ar.APIKey != nil {
			apiKeyName = ar.APIKey.Name
		}
		serverAddress, serverPort := upstreamServerAddrPort(dest)
		return &reporterRun{
			factory:         f,
			provider:        labels.Provider,
			endpoint:        labels.Endpoint,
			model:           labels.Model,
			method:          labels.Method,
			configuration:   labels.Configuration,
			apiKeyName:      apiKeyName,
			serverAddress:   serverAddress,
			serverPort:      serverPort,
			sessionID:       observability.SessionIDFromContext(ctx),
			sessionIDSource: observability.SessionIDSourceFromContext(ctx),
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

	// serverAddress + serverPort name the upstream this request was
	// forwarded to — the GenAI server this client called — sourced from the
	// resolved Destination. Emitted as server.address / server.port on the
	// gen_ai.client.* metrics and the span (server.port rides along whenever
	// server.address is set, per the spec's conditional-requirement rule).
	serverAddress string
	serverPort    int

	// sessionID + sessionIDSource are the resolved bundle id and its
	// provenance header, captured from context at construction time.
	// Stamped on the Record, the span (gen_ai.conversation.id), and the
	// live-feed entry — never on a metric label (unbounded cardinality).
	sessionID       string
	sessionIDSource string

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

	// chunkTimes records the flush timestamp of each streamed response
	// chunk. OnResponseChunk only appends (no work on the streaming hot
	// path); the time_to_first_chunk and time_per_output_chunk histograms
	// are emitted from these at OnComplete, where the response model is
	// known so it can ride as gen_ai.response.model.
	chunkTimes []time.Time

	// upstream captures the transport error from OnUpstreamError; nil
	// on the success path.
	upstream error

	// responseFrames is the captured response collated exactly once (the
	// JSON body as a single frame, or one frame per SSE data event) so the
	// token-usage extractor and the response-attribute extractor share a
	// single split rather than each re-scanning the body.
	responseFrames [][]byte

	// resp* hold the GenAI response descriptors extracted once from the
	// captured response in OnComplete (response body or reassembled SSE):
	// the provider-reported id/model, finish reasons, and reasoning-token
	// count. Read by both the per-request metrics (gen_ai.response.model)
	// and the span (id / model / finish_reasons / reasoning).
	respID            string
	respModel         string
	respFinishReasons []string
	respReasoning     *int

	// respServiceTier + respSystemFingerprint are the OpenAI-specific
	// response descriptors, emitted on the span only (not metrics —
	// system_fingerprint is high-cardinality). Empty for non-OpenAI.
	respServiceTier       string
	respSystemFingerprint string

	// respOutputParts is the model's response as structured message parts
	// (reassembled text plus tool_call parts), extracted once from the
	// collated response frames (genaiattr) — works for both streaming and
	// non-streaming. Used as the bounded output content on both the span and
	// the operation-details event; a tool-only response keeps its tool_call
	// parts rather than vanishing.
	respOutputParts []genaiattr.Part

	// reqContent is the bounded request content (system instructions,
	// latest user turn, tool definitions) parsed once from the request body
	// and shared by the span and event emitters. contentExtracted guards
	// the lazy single parse (only done when content capture is enabled).
	reqContent       genaiattr.Content
	contentExtracted bool

	// reqParams is the request sampling parameters parsed once from the
	// request body and shared by the span and the operation-details event.
	reqParams      genaiattr.RequestAttrs
	reqParamsKnown bool

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

func (r *reporterRun) OnResponseHeaders(_ context.Context, status int, _ http.Header, streaming bool) {
	r.firstByte = time.Now()
	r.statusCode = status
	r.streaming = streaming
	// The time_to_first_chunk / time_per_output_chunk histograms are emitted
	// at OnComplete (recordStreamingMetrics) rather than here, so they can
	// carry gen_ai.response.model — unknown until the response is parsed.
	// firstByte is stamped above; chunk gaps come from chunkTimes.
}

// OnResponseChunk appends the flush timestamp of one streamed chunk. It does
// no work beyond the append — the time_per_output_chunk observations are
// derived from chunkTimes at OnComplete, keeping the streaming write path
// free of metric recording.
func (r *reporterRun) OnResponseChunk(_ context.Context, at time.Time) {
	r.chunkTimes = append(r.chunkTimes, at)
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
	r.captureResponseAttrs(ctx)
	r.populateTokens(ctx, &ev)
	r.recordStreamingMetrics(ctx)

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
	attrs := r.withCompletionAttrs(ev.Provider, ev.Endpoint, ev.Model, r.configuration, ev.StatusCode)
	if r.factory.meters.RequestsTotal != nil {
		r.factory.meters.RequestsTotal.Add(ctx, 1, attrs)
	}
	if r.factory.meters.RequestDuration != nil {
		r.factory.meters.RequestDuration.Record(ctx, float64(ev.DurationMs)/1000.0, attrs)
	}
	r.recordUnmappedFields(ctx, ev)
}

// recordUnmappedFields walks the typed request body and the reconstructed
// typed response for provider fields this build does not model, recording
// gateway.unmapped_fields.total once per (direction, field) and logging the
// full set. This is the provider-drift early-warning signal: a non-zero count
// means the provider shipped a field we silently round-trip via
// DynamicProperties but never decode, so a contract update is due. Reporting
// stays separate from telemetry (CLAUDE.md invariant #4) — the field paths
// ride the meter and the log, never the connector record.
func (r *reporterRun) recordUnmappedFields(ctx context.Context, ev events.Request) {
	counter := r.factory.meters.UnmappedFieldsTotal
	if counter == nil {
		return
	}
	var reqFields []string
	if captured, ok := bodycapture.FromContext(ctx); ok {
		reqFields = unmapped.RequestFields(captured.Body)
	}
	respFields := unmapped.ResponseFields(ev.Endpoint, r.responseForUnmapped(ctx, ev))
	r.emitUnmappedFields(ctx, counter, "request", reqFields)
	r.emitUnmappedFields(ctx, counter, "response", respFields)
}

// responseForUnmapped returns the response as a complete, non-streaming-shaped
// body suitable for typed unmarshalling: the captured bytes for a
// non-streaming response, or the accumulator's reassembly for a streamed one.
// The captured buffer is already byte-bounded, so a truncated body simply
// fails the typed parse and yields no fields rather than a false positive.
func (r *reporterRun) responseForUnmapped(ctx context.Context, ev events.Request) []byte {
	buf, ok := livefeed.ResponseBufferFromContext(ctx)
	if !ok || buf == nil {
		return nil
	}
	raw := buf.Bytes()
	if len(raw) == 0 {
		return nil
	}
	if !ev.Streaming {
		return raw
	}
	if res := accumulator.Accumulate(ev.Provider, ev.Endpoint, raw); res.Recognised && len(res.Assembled) > 0 {
		return res.Assembled
	}
	return nil
}

// emitUnmappedFields records one counter increment per unmapped field path —
// the field set is bounded by the provider API surface, so field is a safe
// metric dimension — and logs the full set once for the request.
func (r *reporterRun) emitUnmappedFields(ctx context.Context, counter metric.Int64Counter, direction string, fields []string) {
	if len(fields) == 0 {
		return
	}
	base := []attribute.KeyValue{
		attribute.String(observability.AttrGenAIProviderName, observability.GenAIProviderName(r.provider)),
		attribute.String(observability.AttrSluiceEndpoint, r.endpoint),
		attribute.String(observability.AttrSluiceUnmappedDirection, direction),
	}
	for _, f := range fields {
		counter.Add(ctx, 1, metric.WithAttributes(append(base[:len(base):len(base)], attribute.String(observability.AttrSluiceUnmappedField, f))...))
	}
	observability.FromContext(ctx).Warn("unmapped provider fields detected",
		"direction", direction,
		"provider", r.provider,
		"endpoint", r.endpoint,
		"fields", fields,
	)
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
	traceCtx := r.emitTrace(ctx, ev)
	r.emitEvents(traceCtx, ev)

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
	if r.factory.store == nil {
		return
	}
	snap := r.factory.store.Snapshot()
	if snap == nil {
		return
	}
	cfg := snap.ConfigurationIndex[r.configuration]
	if cfg == nil || len(cfg.ConnectorBindings) == 0 {
		return
	}
	rec := r.buildRecord(ctx, ev, matches)
	for _, b := range cfg.ConnectorBindings {
		var connectorType string
		if c := snap.ConnectorIndex[b.Connector]; c != nil {
			connectorType = c.Type
		}
		modified, ship, ov := evaluateBinding(rec, b, connectorType)
		if ov.Triggered {
			msg := "connector record body exceeded max_body_bytes; bodies stripped"
			if ov.Dropped {
				msg = "connector record body exceeded max_body_bytes; record dropped"
			}
			observability.FromContext(ctx).Error(msg,
				"connector", b.Connector,
				"connector_type", connectorType,
				"max_body_bytes", ov.CapBytes,
				"body_bytes", ov.BodyBytes,
			)
		}
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
		V:               1,
		ID:              uuid.NewString(),
		TsNs:            tsNs,
		Seq:             r.factory.nextSeq(),
		InstanceID:      r.factory.instanceID,
		CorrelationID:   ev.CorrelationID,
		SessionID:       r.sessionID,
		SessionIDSource: r.sessionIDSource,
		Configuration:   r.configuration,
		APIKeyName:      r.apiKeyName,
		Provider:        ev.Provider,
		Endpoint:        ev.Endpoint,
		Model:           ev.Model,
		Tags:            ev.Tags,
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
		// Per-chunk count from the captured flush timestamps; fall back to 1
		// so a stream that produced no observed flush still reads as
		// streaming to downstream consumers and the e2e harness.
		if n := len(r.chunkTimes); n > 0 {
			rec.Response.StreamChunks = n
		} else {
			rec.Response.StreamChunks = 1
		}
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
		SessionID:           r.sessionID,
		SessionIDSource:     r.sessionIDSource,
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
	if len(r.responseFrames) == 0 {
		return
	}
	snap := tokens.ExtractFrames(ev.Provider, ev.Endpoint, r.responseFrames)
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
	base := r.requestDimensionAttrs(ev.Provider, ev.Endpoint, ev.Model, r.configuration)
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
	attrs := []attribute.KeyValue{
		attribute.String(observability.AttrGenAIProviderName, observability.GenAIProviderName(r.provider)),
		attribute.String(observability.AttrGenAIRequestModel, sanitiseModelLabel(r.model)),
		attribute.String(observability.AttrGenAIOperationName, observability.OperationNameForEndpoint(r.endpoint)),
		attribute.String(observability.AttrSluiceEndpoint, r.endpoint),
	}
	return metric.WithAttributes(append(attrs, r.serverAttrs()...)...)
}

// streamingMetricAttrs is the attribute set for the streaming latency
// histograms (time_to_first_chunk, time_per_output_chunk), emitted at
// OnComplete. Unlike providerEndpointModelAttrs it includes
// gen_ai.response.model, which is known by completion time.
func (r *reporterRun) streamingMetricAttrs() metric.MeasurementOption {
	attrs := []attribute.KeyValue{
		attribute.String(observability.AttrGenAIProviderName, observability.GenAIProviderName(r.provider)),
		attribute.String(observability.AttrGenAIRequestModel, sanitiseModelLabel(r.model)),
		attribute.String(observability.AttrGenAIOperationName, observability.OperationNameForEndpoint(r.endpoint)),
		attribute.String(observability.AttrSluiceEndpoint, r.endpoint),
	}
	if r.respModel != "" {
		attrs = append(attrs, attribute.String(observability.AttrGenAIResponseModel, sanitiseModelLabel(r.respModel)))
	}
	return metric.WithAttributes(append(attrs, r.serverAttrs()...)...)
}

// recordStreamingMetrics emits the time_to_first_chunk and
// time_per_output_chunk histograms from the captured flush timestamps.
// Streaming only, and only at OnComplete so the response model is available
// as an attribute. time_per_output_chunk records one observation per chunk
// after the first.
func (r *reporterRun) recordStreamingMetrics(ctx context.Context) {
	if !r.streaming || r.factory.meters == nil {
		return
	}
	attrs := r.streamingMetricAttrs()
	if r.factory.meters.TimeToFirstChunk != nil && !r.firstByte.IsZero() && !r.started.IsZero() {
		r.factory.meters.TimeToFirstChunk.Record(ctx, r.firstByte.Sub(r.started).Seconds(), attrs)
	}
	if r.factory.meters.TimePerOutputChunk != nil {
		for i := 1; i < len(r.chunkTimes); i++ {
			r.factory.meters.TimePerOutputChunk.Record(ctx, r.chunkTimes[i].Sub(r.chunkTimes[i-1]).Seconds(), attrs)
		}
	}
}

// serverAttrs returns the server.address / server.port pair naming the
// upstream this client called, or nil when no upstream host is known.
// server.port rides along whenever server.address is set, per the spec's
// conditional-requirement rule.
func (r *reporterRun) serverAttrs() []attribute.KeyValue {
	if r.serverAddress == "" {
		return nil
	}
	attrs := []attribute.KeyValue{attribute.String(observability.AttrServerAddress, r.serverAddress)}
	if r.serverPort > 0 {
		attrs = append(attrs, attribute.Int(observability.AttrServerPort, r.serverPort))
	}
	return attrs
}

// emitTrace synthesises the request's OTel span at completion. The span
// is built post-flight with a backdated start (OnRequestStart time) and
// ended immediately, so nothing on the request path waits on the tracer;
// the batch span processor exports out of band. A resilience run that
// made multiple upstream attempts becomes a parent request span with one
// gen_ai child span per attempt, reconstructed from each attempt's
// StartedAt + DurationMs. No-ops when no tracer is configured or the
// request start time was never recorded.
// emitTrace records the request span (and any resilience attempt children)
// and returns the span-carrying context so the caller can emit the GenAI log
// events within it — the log records then pick up the span's trace_id/span_id
// for native trace↔logs correlation. Returns the original ctx unchanged when
// no span is recorded.
func (r *reporterRun) emitTrace(ctx context.Context, ev events.Request) context.Context {
	if r.factory.tracer == nil || r.started.IsZero() {
		return ctx
	}
	op := observability.OperationNameForEndpoint(ev.Endpoint)
	model := sanitiseModelLabel(ev.Model)

	attrs := []attribute.KeyValue{
		attribute.String(observability.AttrGenAIOperationName, op),
		attribute.String(observability.AttrGenAIProviderName, observability.GenAIProviderName(ev.Provider)),
		attribute.String(observability.AttrGenAIRequestModel, model),
		attribute.String(observability.AttrSluiceEndpoint, ev.Endpoint),
		attribute.String(observability.AttrSluiceConfiguration, r.configuration),
		attribute.Int(observability.AttrHTTPResponseStatusCode, ev.StatusCode),
	}
	attrs = append(attrs, r.serverAttrs()...)
	// gen_ai.request.stream is conditionally required iff the request is
	// streaming; gen_ai.response.time_to_first_chunk is the streaming-only
	// span companion to the TTFC metric.
	if r.streaming {
		attrs = append(attrs, attribute.Bool(observability.AttrGenAIRequestStream, true))
		if !r.firstByte.IsZero() && !r.started.IsZero() {
			attrs = append(attrs, attribute.Float64(observability.AttrGenAIResponseTimeToFirstChunk, r.firstByte.Sub(r.started).Seconds()))
		}
	}
	if ev.CorrelationID != "" {
		attrs = append(attrs, attribute.String(observability.AttrSluiceCorrelationID, ev.CorrelationID))
	}
	if r.sessionID != "" {
		attrs = append(attrs, attribute.String(observability.AttrGenAIConversationID, r.sessionID))
	}
	if ev.TokensIn > 0 {
		attrs = append(attrs, attribute.Int(observability.AttrGenAIUsageInputTokens, ev.TokensIn))
	}
	if ev.TokensOut > 0 {
		attrs = append(attrs, attribute.Int(observability.AttrGenAIUsageOutputTokens, ev.TokensOut))
	}
	if ev.TokensCacheCreation > 0 {
		attrs = append(attrs, attribute.Int(observability.AttrGenAIUsageCacheCreationInputTokens, ev.TokensCacheCreation))
	}
	if ev.TokensCached > 0 {
		attrs = append(attrs, attribute.Int(observability.AttrGenAIUsageCacheReadInputTokens, ev.TokensCached))
	}
	attrs = r.appendRequestParamAttrs(ctx, attrs)
	attrs = r.appendResponseAttrs(attrs)
	// openai.api.type distinguishes the Chat Completions vs Responses API
	// surface. The v2 endpoint label is the protocol ("chat"/"responses"); the
	// emitted api.type keeps the spec's well-known value ("chat_completions").
	if ev.Provider == "openai" && (ev.Endpoint == "chat" || ev.Endpoint == "responses") {
		attrs = append(attrs, attribute.String(observability.AttrOpenAIAPIType, openAIAPIType(ev.Endpoint)))
	}
	if r.factory.captureContent {
		attrs = r.appendSpanContent(ctx, attrs)
	}
	// error.type: the HTTP status on a 4xx/5xx, else a stable
	// transport_error token when the upstream call failed before a status
	// (connection refused, EOF before headers, header timeout).
	if ev.StatusCode >= 400 {
		attrs = append(attrs, attribute.String(observability.AttrErrorType, strconv.Itoa(ev.StatusCode)))
	} else if ev.UpstreamError != "" {
		attrs = append(attrs, attribute.String(observability.AttrErrorType, "transport_error"))
	}

	parentCtx, span := r.factory.tracer.Start(ctx, op+" "+model,
		trace.WithTimestamp(r.started),
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)
	if ev.StatusCode >= 400 {
		span.SetStatus(codes.Error, http.StatusText(ev.StatusCode))
	} else if ev.UpstreamError != "" {
		span.SetStatus(codes.Error, ev.UpstreamError)
	}

	for _, a := range ev.Attempts {
		r.emitAttemptSpan(parentCtx, op, model, a)
	}

	span.End(trace.WithTimestamp(r.started.Add(time.Duration(ev.DurationMs) * time.Millisecond)))
	// The SpanContext remains valid in parentCtx after End; emitting the log
	// events with it attaches trace_id/span_id to the records.
	return parentCtx
}

// emitAttemptSpan synthesises one child span for a resilience attempt,
// backdated from the attempt's StartedAt + DurationMs so a failover walk
// renders as a waterfall under the request span. cb_blocked entries
// (zero duration, never ran) still appear as a zero-width marker.
func (r *reporterRun) emitAttemptSpan(ctx context.Context, op, model string, a events.AttemptRecord) {
	if a.StartedAt.IsZero() {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String(observability.AttrGenAIOperationName, op),
		attribute.String(observability.AttrGenAIProviderName, observability.GenAIProviderName(r.provider)),
		attribute.String(observability.AttrGenAIRequestModel, model),
		attribute.String(observability.AttrSluiceResilienceTarget, a.Target),
		attribute.String(observability.AttrSluiceResilienceOutcome, a.Outcome),
	}
	if a.StatusCode != 0 {
		attrs = append(attrs, attribute.Int(observability.AttrHTTPResponseStatusCode, a.StatusCode))
	}
	failed := a.StatusCode >= 400 || a.Error != ""
	if failed {
		errType := a.Error
		if errType == "" {
			errType = strconv.Itoa(a.StatusCode)
		}
		attrs = append(attrs, attribute.String(observability.AttrErrorType, errType))
	}

	_, span := r.factory.tracer.Start(ctx, op+" "+model+" ["+a.Target+"]",
		trace.WithTimestamp(a.StartedAt),
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)
	if failed {
		span.SetStatus(codes.Error, a.Error)
	}
	span.End(trace.WithTimestamp(a.StartedAt.Add(time.Duration(a.DurationMs) * time.Millisecond)))
}

// emitEvents emits the GenAI log-record events at completion. The
// gen_ai.client.operation.exception event fires on any failure (the spec's
// error-recording mechanism). The gen_ai.client.inference.operation.details
// event carries the bounded prompt/response content and fires only when
// content capture is enabled — without content it would merely duplicate
// the span's metadata. No-ops without an OTel event logger. Runs
// post-flight; the batch log processor exports out of band.
func (r *reporterRun) emitEvents(ctx context.Context, ev events.Request) {
	if r.factory.eventLogger == nil {
		return
	}
	ts := r.started
	if ts.IsZero() {
		ts = time.Now()
	}
	op := observability.OperationNameForEndpoint(ev.Endpoint)

	if ev.StatusCode >= 400 || ev.UpstreamError != "" {
		excType := strconv.Itoa(ev.StatusCode)
		excMsg := http.StatusText(ev.StatusCode)
		if ev.StatusCode < 400 && ev.UpstreamError != "" {
			excType = "transport_error"
			excMsg = ev.UpstreamError
		}
		var exc otellog.Record
		exc.SetTimestamp(ts)
		exc.SetEventName(observability.EventOperationException)
		exc.SetSeverity(otellog.SeverityWarn)
		exc.AddAttributes(
			otellog.String(observability.AttrExceptionType, excType),
			otellog.String(observability.AttrExceptionMessage, excMsg),
			otellog.String(observability.AttrGenAIOperationName, op),
			otellog.String(observability.AttrGenAIProviderName, observability.GenAIProviderName(ev.Provider)),
		)
		if ev.CorrelationID != "" {
			exc.AddAttributes(otellog.String(observability.AttrSluiceCorrelationID, ev.CorrelationID))
		}
		r.factory.eventLogger.Emit(ctx, exc)
	}

	if r.factory.captureContent {
		r.emitOperationDetails(ctx, ev, ts, op)
	}
}

// emitOperationDetails emits the gen_ai.client.inference.operation.details
// event carrying the bounded prompt/response content: the latest user turn,
// the model's response, the system instructions, and the tool definitions.
// Messages and tool definitions are recorded as structured log values (the
// event's "MUST be structured" requirement); system_instructions is plain
// text. Only fires when content capture is enabled.
func (r *reporterRun) emitOperationDetails(ctx context.Context, ev events.Request, ts time.Time, op string) {
	attrs := []otellog.KeyValue{
		otellog.String(observability.AttrGenAIOperationName, op),
		otellog.String(observability.AttrGenAIProviderName, observability.GenAIProviderName(ev.Provider)),
		otellog.String(observability.AttrGenAIRequestModel, sanitiseModelLabel(ev.Model)),
		otellog.String(observability.AttrSluiceEndpoint, ev.Endpoint),
		otellog.String(observability.AttrSluiceConfiguration, r.configuration),
		otellog.Int(observability.AttrHTTPResponseStatusCode, ev.StatusCode),
	}
	if r.respModel != "" {
		attrs = append(attrs, otellog.String(observability.AttrGenAIResponseModel, sanitiseModelLabel(r.respModel)))
	}
	if ev.CorrelationID != "" {
		attrs = append(attrs, otellog.String(observability.AttrSluiceCorrelationID, ev.CorrelationID))
	}
	if r.sessionID != "" {
		attrs = append(attrs, otellog.String(observability.AttrGenAIConversationID, r.sessionID))
	}

	// The event is "operation details": carry the same request/response/
	// usage attribute set as the span so a log-only consumer is self-served.
	if ev.TokensIn > 0 {
		attrs = append(attrs, otellog.Int(observability.AttrGenAIUsageInputTokens, ev.TokensIn))
	}
	if ev.TokensOut > 0 {
		attrs = append(attrs, otellog.Int(observability.AttrGenAIUsageOutputTokens, ev.TokensOut))
	}
	if ev.TokensCacheCreation > 0 {
		attrs = append(attrs, otellog.Int(observability.AttrGenAIUsageCacheCreationInputTokens, ev.TokensCacheCreation))
	}
	if ev.TokensCached > 0 {
		attrs = append(attrs, otellog.Int(observability.AttrGenAIUsageCacheReadInputTokens, ev.TokensCached))
	}
	if r.respReasoning != nil && *r.respReasoning > 0 {
		attrs = append(attrs, otellog.Int(observability.AttrGenAIUsageReasoningOutputTokens, *r.respReasoning))
	}
	if r.respID != "" {
		attrs = append(attrs, otellog.String(observability.AttrGenAIResponseID, r.respID))
	}
	if len(r.respFinishReasons) > 0 {
		attrs = append(attrs, logStringSlice(observability.AttrGenAIResponseFinishReasons, r.respFinishReasons))
	}
	if r.serverAddress != "" {
		attrs = append(attrs, otellog.String(observability.AttrServerAddress, r.serverAddress))
		if r.serverPort > 0 {
			attrs = append(attrs, otellog.Int(observability.AttrServerPort, r.serverPort))
		}
	}
	if r.streaming {
		attrs = append(attrs, otellog.Bool(observability.AttrGenAIRequestStream, true))
		if !r.firstByte.IsZero() && !r.started.IsZero() {
			attrs = append(attrs, otellog.Float64(observability.AttrGenAIResponseTimeToFirstChunk, r.firstByte.Sub(r.started).Seconds()))
		}
	}
	p := r.requestParams(ctx)
	if p.Temperature != nil {
		attrs = append(attrs, otellog.Float64(observability.AttrGenAIRequestTemperature, *p.Temperature))
	}
	if p.TopP != nil {
		attrs = append(attrs, otellog.Float64(observability.AttrGenAIRequestTopP, *p.TopP))
	}
	if p.TopK != nil {
		attrs = append(attrs, otellog.Float64(observability.AttrGenAIRequestTopK, *p.TopK))
	}
	if p.MaxTokens != nil {
		attrs = append(attrs, otellog.Int(observability.AttrGenAIRequestMaxTokens, *p.MaxTokens))
	}
	if p.FrequencyPenalty != nil {
		attrs = append(attrs, otellog.Float64(observability.AttrGenAIRequestFrequencyPenalty, *p.FrequencyPenalty))
	}
	if p.PresencePenalty != nil {
		attrs = append(attrs, otellog.Float64(observability.AttrGenAIRequestPresencePenalty, *p.PresencePenalty))
	}
	if len(p.StopSequences) > 0 {
		attrs = append(attrs, logStringSlice(observability.AttrGenAIRequestStopSequences, p.StopSequences))
	}
	if p.Seed != nil {
		attrs = append(attrs, otellog.Int(observability.AttrGenAIRequestSeed, *p.Seed))
	}
	if p.ChoiceCount != nil && *p.ChoiceCount != 1 {
		attrs = append(attrs, otellog.Int(observability.AttrGenAIRequestChoiceCount, *p.ChoiceCount))
	}
	if p.OutputType != "" {
		attrs = append(attrs, otellog.String(observability.AttrGenAIOutputType, p.OutputType))
	}
	if ev.StatusCode >= 400 {
		attrs = append(attrs, otellog.String(observability.AttrErrorType, strconv.Itoa(ev.StatusCode)))
	} else if ev.UpstreamError != "" {
		attrs = append(attrs, otellog.String(observability.AttrErrorType, "transport_error"))
	}
	if p.ServiceTier != "" && p.ServiceTier != "auto" {
		attrs = append(attrs, otellog.String(observability.AttrOpenAIRequestServiceTier, p.ServiceTier))
	}
	if r.respServiceTier != "" {
		attrs = append(attrs, otellog.String(observability.AttrOpenAIResponseServiceTier, r.respServiceTier))
	}
	if r.respSystemFingerprint != "" {
		attrs = append(attrs, otellog.String(observability.AttrOpenAIResponseSystemFingerprint, r.respSystemFingerprint))
	}
	if ev.Provider == "openai" && (ev.Endpoint == "chat" || ev.Endpoint == "responses") {
		attrs = append(attrs, otellog.String(observability.AttrOpenAIAPIType, openAIAPIType(ev.Endpoint)))
	}

	caps := r.factory.caps
	content := r.boundedContent(ctx)
	if parts := sanitiseParts(content.SystemInstructions, caps.SystemInstructions, caps.Messages); len(parts) > 0 {
		attrs = append(attrs, otellog.KeyValue{Key: observability.AttrGenAISystemInstructions, Value: logPartsValue(parts)})
	}
	if content.InputMessage != nil {
		if parts := sanitiseParts(content.InputMessage.Parts, caps.Messages, caps.Messages); len(parts) > 0 {
			attrs = append(attrs, otellog.KeyValue{Key: observability.AttrGenAIInputMessages, Value: logMessagesValue(content.InputMessage.Role, parts)})
		}
	}
	if parts := sanitiseParts(r.respOutputParts, caps.Messages, caps.Messages); len(parts) > 0 {
		attrs = append(attrs, otellog.KeyValue{Key: observability.AttrGenAIOutputMessages, Value: logMessagesValue("assistant", parts)})
	}
	if v, ok := logToolDefsValue(content.ToolDefinitions, caps.Messages, caps.ToolDefinitions); ok {
		attrs = append(attrs, otellog.KeyValue{Key: observability.AttrGenAIToolDefinitions, Value: v})
	}

	var rec otellog.Record
	rec.SetTimestamp(ts)
	rec.SetEventName(observability.EventInferenceDetails)
	rec.SetSeverity(otellog.SeverityInfo)
	rec.AddAttributes(attrs...)
	r.factory.eventLogger.Emit(ctx, rec)
}

// emitPart is a part whose text fields are already redacted and capped, ready
// to render to either an otellog structured value (events) or a JSON map
// (spans). Sanitising once keeps the two renderings byte-identical and avoids
// re-redacting per signal.
type emitPart struct {
	typ       string
	content   string
	id        string
	name      string
	arguments json.RawMessage
	result    string
}

// sanitiseParts redacts and caps each part's free text once. Tool-call
// arguments are redacted as raw JSON (dropped wholesale when over
// toolArgsCap or when redaction breaks well-formedness); text and tool-
// response result fields are redacted then capped at textCap (redact
// before cap so a secret can't hide across the truncation boundary). A
// cap of 0 in either argument means "no cap" — the field rides through
// unbounded.
func sanitiseParts(parts []genaiattr.Part, textCap, toolArgsCap int) []emitPart {
	out := make([]emitPart, 0, len(parts))
	for _, p := range parts {
		e := emitPart{typ: p.Type, id: p.ID, name: p.Name}
		switch p.Type {
		case observability.PartTypeToolCall:
			if len(p.Arguments) > 0 && (toolArgsCap <= 0 || len(p.Arguments) <= toolArgsCap) {
				red := contentredact.Redact(string(p.Arguments))
				if json.Valid([]byte(red)) {
					e.arguments = json.RawMessage(red)
				} else if b, err := json.Marshal(red); err == nil {
					e.arguments = json.RawMessage(b)
				}
			}
		case observability.PartTypeToolCallResponse:
			e.result = capText(contentredact.Redact(p.Result), textCap)
		default:
			e.content = capText(contentredact.Redact(p.Content), textCap)
		}
		out = append(out, e)
	}
	return out
}

// logPartValue renders one sanitised part to a structured otellog map,
// emitting only the fields relevant to its type.
func (e emitPart) logPartValue() otellog.Value {
	kvs := []otellog.KeyValue{otellog.String("type", e.typ)}
	switch e.typ {
	case observability.PartTypeToolCall:
		if e.id != "" {
			kvs = append(kvs, otellog.String("id", e.id))
		}
		if e.name != "" {
			kvs = append(kvs, otellog.String("name", e.name))
		}
		if len(e.arguments) > 0 {
			if v, ok := jsonToLogValue(e.arguments); ok {
				kvs = append(kvs, otellog.KeyValue{Key: "arguments", Value: v})
			}
		}
	case observability.PartTypeToolCallResponse:
		if e.id != "" {
			kvs = append(kvs, otellog.String("id", e.id))
		}
		kvs = append(kvs, otellog.String("result", e.result))
	default:
		if e.content != "" {
			kvs = append(kvs, otellog.String("content", e.content))
		}
	}
	return otellog.MapValue(kvs...)
}

// logPartsValue renders a parts list to an otellog slice value — the
// structured form gen_ai.system_instructions takes (a bare parts array).
func logPartsValue(parts []emitPart) otellog.Value {
	vals := make([]otellog.Value, len(parts))
	for i, p := range parts {
		vals[i] = p.logPartValue()
	}
	return otellog.SliceValue(vals...)
}

// logMessagesValue renders a single role-tagged turn to the messages-array
// form gen_ai.input.messages / gen_ai.output.messages take: [{role, parts}].
func logMessagesValue(role string, parts []emitPart) otellog.Value {
	return otellog.SliceValue(otellog.MapValue(
		otellog.String("role", role),
		otellog.KeyValue{Key: "parts", Value: logPartsValue(parts)},
	))
}

// logToolDefsValue renders gen_ai.tool.definitions to a structured otellog
// slice of {type,name,description,parameters}. Parameter schemas are
// dropped wholesale when their combined size exceeds toolDefsCap (kept
// legible via name/description); descCap caps each individual tool's
// description text. A toolDefsCap or descCap of 0 means "no cap".
// Returns ok=false when there are no tools.
func logToolDefsValue(defs []genaiattr.ToolDefinition, descCap, toolDefsCap int) (otellog.Value, bool) {
	if len(defs) == 0 {
		return otellog.Value{}, false
	}
	withParams := toolDefParamsFit(defs, toolDefsCap)
	vals := make([]otellog.Value, 0, len(defs))
	for _, d := range defs {
		kvs := []otellog.KeyValue{otellog.String("type", d.Type)}
		if d.Name != "" {
			kvs = append(kvs, otellog.String("name", d.Name))
		}
		if d.Description != "" {
			kvs = append(kvs, otellog.String("description", capText(d.Description, descCap)))
		}
		if withParams && len(d.Parameters) > 0 {
			if v, ok := jsonToLogValue(d.Parameters); ok {
				kvs = append(kvs, otellog.KeyValue{Key: "parameters", Value: v})
			}
		}
		vals = append(vals, otellog.MapValue(kvs...))
	}
	return otellog.SliceValue(vals...), true
}

// toolDefParamsFit reports whether the combined parameter-schema bytes
// are within toolDefsCap, i.e. small enough to include the parameters.
// A cap of 0 means "no cap" and the result is always true.
func toolDefParamsFit(defs []genaiattr.ToolDefinition, toolDefsCap int) bool {
	if toolDefsCap <= 0 {
		return true
	}
	total := 0
	for _, d := range defs {
		total += len(d.Parameters)
	}
	return total <= toolDefsCap
}

// jsonToLogValue decodes a JSON document into a structured otellog.Value so
// tool definitions ride the event in structured form. Returns ok=false on
// unparseable input.
func jsonToLogValue(raw json.RawMessage) (otellog.Value, bool) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return otellog.Value{}, false
	}
	return anyToLogValue(v), true
}

func anyToLogValue(v any) otellog.Value {
	switch t := v.(type) {
	case bool:
		return otellog.BoolValue(t)
	case float64:
		return otellog.Float64Value(t)
	case string:
		return otellog.StringValue(contentredact.Redact(t))
	case []any:
		vals := make([]otellog.Value, len(t))
		for i, e := range t {
			vals[i] = anyToLogValue(e)
		}
		return otellog.SliceValue(vals...)
	case map[string]any:
		kvs := make([]otellog.KeyValue, 0, len(t))
		for k, e := range t {
			kvs = append(kvs, otellog.KeyValue{Key: k, Value: anyToLogValue(e)})
		}
		return otellog.MapValue(kvs...)
	default:
		// nil and any unmodelled type collapse to an empty string value.
		return otellog.StringValue("")
	}
}

// capText truncates s at cap bytes with a "…[truncated]" marker. A cap
// of 0 (or negative) means "no cap" and returns s unchanged. The byte-
// boundary truncation is the same as before — callers pass already-
// redacted text so a partial UTF-8 sequence at the cut point is not a
// new exposure surface.
func capText(s string, cap int) string {
	if cap <= 0 || len(s) <= cap {
		return s
	}
	return s[:cap] + "…[truncated]"
}

// boundedContent parses the request body for the bounded prompt content
// (system instructions, latest user turn, tool definitions) exactly once,
// caching it so the span and event emitters share a single parse rather
// than each re-parsing the body. Zero Content when no body was captured.
func (r *reporterRun) boundedContent(ctx context.Context) genaiattr.Content {
	if r.contentExtracted {
		return r.reqContent
	}
	r.contentExtracted = true
	if captured, ok := bodycapture.FromContext(ctx); ok && len(captured.Raw) > 0 {
		r.reqContent = genaiattr.ExtractContent(r.endpoint, captured.Raw)
	}
	return r.reqContent
}

// appendSpanContent attaches the bounded prompt/response content to the
// span. Span attributes can't hold structured maps, so messages ride as
// JSON strings (spec-permitted on spans, unlike events). Redacted + capped.
func (r *reporterRun) appendSpanContent(ctx context.Context, attrs []attribute.KeyValue) []attribute.KeyValue {
	caps := r.factory.caps
	content := r.boundedContent(ctx)
	if parts := sanitiseParts(content.SystemInstructions, caps.SystemInstructions, caps.Messages); len(parts) > 0 {
		if s := jsonPartsString(parts); s != "" {
			attrs = append(attrs, attribute.String(observability.AttrGenAISystemInstructions, s))
		}
	}
	if content.InputMessage != nil {
		if parts := sanitiseParts(content.InputMessage.Parts, caps.Messages, caps.Messages); len(parts) > 0 {
			if s := jsonMessagesString(content.InputMessage.Role, parts); s != "" {
				attrs = append(attrs, attribute.String(observability.AttrGenAIInputMessages, s))
			}
		}
	}
	if parts := sanitiseParts(r.respOutputParts, caps.Messages, caps.Messages); len(parts) > 0 {
		if s := jsonMessagesString("assistant", parts); s != "" {
			attrs = append(attrs, attribute.String(observability.AttrGenAIOutputMessages, s))
		}
	}
	if s := jsonToolDefsString(content.ToolDefinitions, caps.Messages, caps.ToolDefinitions); s != "" {
		attrs = append(attrs, attribute.String(observability.AttrGenAIToolDefinitions, s))
	}
	return attrs
}

// jsonMap renders one sanitised part to a JSON map for a span attribute
// (spans cannot carry structured map/slice values; the spec permits a JSON
// string there). Tool-call arguments ride as raw JSON, already redacted.
func (e emitPart) jsonMap() map[string]any {
	m := map[string]any{"type": e.typ}
	switch e.typ {
	case observability.PartTypeToolCall:
		if e.id != "" {
			m["id"] = e.id
		}
		if e.name != "" {
			m["name"] = e.name
		}
		if len(e.arguments) > 0 {
			m["arguments"] = e.arguments
		}
	case observability.PartTypeToolCallResponse:
		if e.id != "" {
			m["id"] = e.id
		}
		m["result"] = e.result
	default:
		if e.content != "" {
			m["content"] = e.content
		}
	}
	return m
}

func partMaps(parts []emitPart) []map[string]any {
	ms := make([]map[string]any, len(parts))
	for i, p := range parts {
		ms[i] = p.jsonMap()
	}
	return ms
}

// jsonPartsString renders a parts list as a JSON-array string — the span
// form of gen_ai.system_instructions (a bare parts array).
func jsonPartsString(parts []emitPart) string {
	return marshalSpanJSON(partMaps(parts))
}

// jsonMessagesString renders a single role-tagged turn as a JSON string for a
// span message attribute: [{role, parts}].
func jsonMessagesString(role string, parts []emitPart) string {
	return marshalSpanJSON([]map[string]any{{"role": role, "parts": partMaps(parts)}})
}

// jsonToolDefsString renders gen_ai.tool.definitions as a JSON-array
// string for the span, dropping parameter schemas wholesale when their
// combined size exceeds toolDefsCap and capping each tool's description
// at descCap. A cap of 0 in either argument means "no cap". Returns ""
// when there are no tools.
func jsonToolDefsString(defs []genaiattr.ToolDefinition, descCap, toolDefsCap int) string {
	if len(defs) == 0 {
		return ""
	}
	withParams := toolDefParamsFit(defs, toolDefsCap)
	ms := make([]map[string]any, 0, len(defs))
	for _, d := range defs {
		m := map[string]any{"type": d.Type}
		if d.Name != "" {
			m["name"] = d.Name
		}
		if d.Description != "" {
			m["description"] = capText(d.Description, descCap)
		}
		if withParams && len(d.Parameters) > 0 {
			m["parameters"] = d.Parameters
		}
		ms = append(ms, m)
	}
	return marshalSpanJSON(ms)
}

func marshalSpanJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// logStringSlice builds an otellog slice-of-strings attribute (e.g. for
// finish_reasons / stop_sequences on the operation-details event).
func logStringSlice(key string, vals []string) otellog.KeyValue {
	vs := make([]otellog.Value, len(vals))
	for i, s := range vals {
		vs[i] = otellog.StringValue(s)
	}
	return otellog.Slice(key, vs...)
}

// requestParams parses the request body for the GenAI sampling parameters
// exactly once, caching the result so the span and the operation-details
// event share a single parse. Zero RequestAttrs when no body was captured.
func (r *reporterRun) requestParams(ctx context.Context) genaiattr.RequestAttrs {
	if r.reqParamsKnown {
		return r.reqParams
	}
	r.reqParamsKnown = true
	if captured, ok := bodycapture.FromContext(ctx); ok && len(captured.Raw) > 0 {
		r.reqParams = genaiattr.ExtractRequest(r.endpoint, captured.Raw)
	}
	return r.reqParams
}

// appendRequestParamAttrs appends the present GenAI request parameters to
// the span. choice.count is emitted only when present and != 1, and
// output.type only when a format was requested, matching the spec's
// conditional-requirement rules.
func (r *reporterRun) appendRequestParamAttrs(ctx context.Context, attrs []attribute.KeyValue) []attribute.KeyValue {
	p := r.requestParams(ctx)
	if p.Temperature != nil {
		attrs = append(attrs, attribute.Float64(observability.AttrGenAIRequestTemperature, *p.Temperature))
	}
	if p.TopP != nil {
		attrs = append(attrs, attribute.Float64(observability.AttrGenAIRequestTopP, *p.TopP))
	}
	if p.TopK != nil {
		attrs = append(attrs, attribute.Float64(observability.AttrGenAIRequestTopK, *p.TopK))
	}
	if p.MaxTokens != nil {
		attrs = append(attrs, attribute.Int(observability.AttrGenAIRequestMaxTokens, *p.MaxTokens))
	}
	if p.FrequencyPenalty != nil {
		attrs = append(attrs, attribute.Float64(observability.AttrGenAIRequestFrequencyPenalty, *p.FrequencyPenalty))
	}
	if p.PresencePenalty != nil {
		attrs = append(attrs, attribute.Float64(observability.AttrGenAIRequestPresencePenalty, *p.PresencePenalty))
	}
	if len(p.StopSequences) > 0 {
		attrs = append(attrs, attribute.StringSlice(observability.AttrGenAIRequestStopSequences, p.StopSequences))
	}
	if p.Seed != nil {
		attrs = append(attrs, attribute.Int(observability.AttrGenAIRequestSeed, *p.Seed))
	}
	if p.ChoiceCount != nil && *p.ChoiceCount != 1 {
		attrs = append(attrs, attribute.Int(observability.AttrGenAIRequestChoiceCount, *p.ChoiceCount))
	}
	if p.OutputType != "" {
		attrs = append(attrs, attribute.String(observability.AttrGenAIOutputType, p.OutputType))
	}
	// openai.request.service_tier is Conditionally Required only when the
	// request carries a tier other than "auto".
	if p.ServiceTier != "" && p.ServiceTier != "auto" {
		attrs = append(attrs, attribute.String(observability.AttrOpenAIRequestServiceTier, p.ServiceTier))
	}
	return attrs
}

// captureResponseAttrs extracts the GenAI response descriptors from the
// captured response — the JSON body or the reassembled SSE stream — exactly
// once, stashing them on the run for the metric and span emitters. No-ops
// when no response buffer is present (bodies disabled, or a transport
// failure with no response).
func (r *reporterRun) captureResponseAttrs(ctx context.Context) {
	buf, ok := livefeed.ResponseBufferFromContext(ctx)
	if !ok || buf == nil {
		return
	}
	raw := buf.Bytes()
	if len(raw) == 0 {
		return
	}
	// Split the response once; populateTokens and the span both read these
	// frames rather than re-scanning the body.
	r.responseFrames = sseframe.Collate(raw)
	resp := genaiattr.ExtractResponseFrames(r.endpoint, r.responseFrames)
	r.respID = resp.ID
	r.respModel = resp.Model
	r.respFinishReasons = resp.FinishReasons
	r.respReasoning = resp.ReasoningTokens
	r.respServiceTier = resp.ServiceTier
	r.respSystemFingerprint = resp.SystemFingerprint
	r.respOutputParts = resp.OutputParts
}

// appendResponseAttrs appends the Recommended response descriptors captured
// by captureResponseAttrs. Span-side companion to the gen_ai.response.model
// already carried on the per-request metrics via requestDimensionAttrs.
func (r *reporterRun) appendResponseAttrs(attrs []attribute.KeyValue) []attribute.KeyValue {
	if r.respID != "" {
		attrs = append(attrs, attribute.String(observability.AttrGenAIResponseID, r.respID))
	}
	if r.respModel != "" {
		attrs = append(attrs, attribute.String(observability.AttrGenAIResponseModel, r.respModel))
	}
	if len(r.respFinishReasons) > 0 {
		attrs = append(attrs, attribute.StringSlice(observability.AttrGenAIResponseFinishReasons, r.respFinishReasons))
	}
	if r.respReasoning != nil && *r.respReasoning > 0 {
		attrs = append(attrs, attribute.Int(observability.AttrGenAIUsageReasoningOutputTokens, *r.respReasoning))
	}
	if r.respServiceTier != "" {
		attrs = append(attrs, attribute.String(observability.AttrOpenAIResponseServiceTier, r.respServiceTier))
	}
	if r.respSystemFingerprint != "" {
		attrs = append(attrs, attribute.String(observability.AttrOpenAIResponseSystemFingerprint, r.respSystemFingerprint))
	}
	return attrs
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
// shared by every per-request instrument: gen_ai.provider.name (mapped to
// the spec enum value), request model, the coarse gen_ai.operation.name,
// the precise sluice.endpoint route, the resolved configuration, and the
// upstream server.address/server.port. Status is layered on separately
// because token instruments don't carry it.
func (r *reporterRun) requestDimensionAttrs(provider, endpoint, model, configuration string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(observability.AttrGenAIProviderName, observability.GenAIProviderName(provider)),
		attribute.String(observability.AttrGenAIRequestModel, sanitiseModelLabel(model)),
		attribute.String(observability.AttrGenAIOperationName, observability.OperationNameForEndpoint(endpoint)),
		attribute.String(observability.AttrSluiceEndpoint, endpoint),
		attribute.String(observability.AttrSluiceConfiguration, configuration),
	}
	// gen_ai.response.model is Recommended on the client metrics; emitted
	// when the response carried a model (captured in OnComplete before the
	// metrics fire). Bounded cardinality — a provider model id, like the
	// request model.
	if r.respModel != "" {
		attrs = append(attrs, attribute.String(observability.AttrGenAIResponseModel, sanitiseModelLabel(r.respModel)))
	}
	return append(attrs, r.serverAttrs()...)
}

// withCompletionAttrs extends the request dimensions with the HTTP status
// code, and on the failure path the spec error.type. error.type is an
// open enum, so the status code string is a spec-legal value; it is set
// only for 4xx/5xx so the attribute's presence itself marks a failure.
func (r *reporterRun) withCompletionAttrs(provider, endpoint, model, configuration string, status int) metric.MeasurementOption {
	attrs := r.requestDimensionAttrs(provider, endpoint, model, configuration)
	attrs = append(attrs, attribute.Int(observability.AttrHTTPResponseStatusCode, status))
	if status >= 400 {
		attrs = append(attrs, attribute.String(observability.AttrErrorType, strconv.Itoa(status)))
	}
	return metric.WithAttributes(attrs...)
}
