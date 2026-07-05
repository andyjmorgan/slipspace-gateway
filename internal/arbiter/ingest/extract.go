// Package ingest maps the feeds gateways push — gen_ai OTLP spans and metrics,
// and the HMAC-trusted Record webhook — into the telemetry store. This file is
// the pure extraction layer: a gen_ai OTLP span (plus its resource attributes)
// becomes a store.RequestEvent.
//
// Single-writer model (Telemetry Rearchitecture design note): the OTel span
// owns request_events outright. EventFromSpan stores the COMPLETE span verbatim
// into span_event — nothing is dropped — and projects the filter columns out of
// it. The Record feed (record.go) no longer writes the entity; it lands a lazy
// verbatim blob keyed by the same correlation_id. observed_at is the gateway
// request-start (the span START time), not ingest now().
package ingest

import (
	"encoding/json"
	"strconv"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/store"
)

const (
	// attrCorrelationID is the join key — the only attribute whose absence makes
	// a span unusable (no entity key, no record join).
	attrCorrelationID = "slipspace.correlation_id"

	// attrEnriched flags a span Arbiter itself emitted (mirrors
	// arbiter.AttrEnriched). The receiver declines re-admitting flagged spans so
	// an enriched span routed back into the collector cannot loop (ADR-002).
	attrEnriched = "slipspace.enriched"

	// gen_ai semconv attributes the columns project from.
	attrModel         = "gen_ai.request.model"
	attrGenAIProvider = "gen_ai.provider.name"
	attrStatusCode    = "http.response.status_code"

	attrInputTokens         = "gen_ai.usage.input_tokens"  //nolint:gosec // OTLP attribute key, not a credential
	attrOutputTokens        = "gen_ai.usage.output_tokens" //nolint:gosec // OTLP attribute key, not a credential
	attrCostUSD             = "slipspace.cost.usd"
	attrCacheReadTokens     = "gen_ai.usage.cache_read.input_tokens"     //nolint:gosec // OTLP attribute key, not a credential
	attrCacheCreationTokens = "gen_ai.usage.cache_creation.input_tokens" //nolint:gosec // OTLP attribute key, not a credential
	attrConversationID      = "gen_ai.conversation.id"
	attrAgentID             = "gen_ai.agent.id"
	attrEnduserID           = "enduser.id"
	attrRequestStream       = "gen_ai.request.stream"

	// attrSlipSpaceSessionID is the session bundle root (the gateway's own
	// attribute, since the semconv has no session-vs-thread split). It projects
	// to session_id; gen_ai.conversation.id is the fallback for spans predating
	// it (where conversation == session for the main agent).
	attrSlipSpaceSessionID = "slipspace.session_id"

	// attrSlipSpaceParentConversationID is the parent of a subagent thread — the
	// hierarchy edge with no semconv home. Projects to parent_conversation_id.
	attrSlipSpaceParentConversationID = "slipspace.parent_conversation_id"

	// Gateway facts the gateway now emits on the span (relaxed channel boundary).
	// These are the EXACT string keys the destination/reporter writes — the
	// Arbiter only consumes them, never mints them. configuration and
	// protocol project to columns; method / api_key_name / upstream_status ride
	// the blob under their own keys (read back via store.SpanFields), so they
	// need no dedicated const here; tags / rules_fired are normalised to JSON
	// arrays for the GIN filters.
	attrSlipSpaceConfiguration = "slipspace.configuration"
	attrSlipSpaceProtocol      = "slipspace.protocol"
	attrSlipSpaceTags          = "slipspace.tags"
	attrSlipSpaceRulesFired    = "slipspace.rules_fired"

	// attrResourceGatewayID is the resource attribute the gateway id is lifted
	// from; folded into span_event (no promoted column unless a filter needs it).
	attrResourceGatewayID = "gateway_id"

	// span_event JSON keys for the projected/derived values. These mirror the
	// store.SpanFields tags so a drill-down decode reads them back.
	keyGatewayID = "gateway_id"
	keyLatencyMs = "slipspace.latency_ms"
	keyTags      = "tags"
	keyRules     = "rules_fired"
	keyContent   = "gen_ai_content"
)

// EventFromSpan extracts a request event from a span and its resource
// attributes. Resource attributes are merged under the span's own (span wins).
// Returns ok=false only when the span carries no correlation id — the key
// everything joins on. The COMPLETE merged attribute set (every value, not an
// allowlist) plus the derived gateway id, latency, tags/rules arrays, and the
// gen_ai content lands in store.RequestEvent.SpanEvent; the scalar columns are
// projected from it. contentMaxBytes bounds the captured gen_ai content
// (<= 0 keeps it whole).
func EventFromSpan(resourceAttrs []*commonpb.KeyValue, span *tracepb.Span, contentMaxBytes int) (store.RequestEvent, bool) {
	if span == nil {
		return store.RequestEvent{}, false
	}
	attrs := mergeAttrs(resourceAttrs, span.GetAttributes())

	// Loop prevention: decline Arbiter's own enriched spans (ADR-002), so an
	// enriched span a customer routes back into the collector is not re-ingested.
	if boolAttr(attrs, attrEnriched) {
		return store.RequestEvent{}, false
	}

	corr := strAttr(attrs, attrCorrelationID)
	if corr == "" {
		return store.RequestEvent{}, false
	}

	return store.RequestEvent{
		CorrelationID:        corr,
		ObservedAt:           spanStart(span),
		SessionID:            firstNonEmpty(strAttr(attrs, attrSlipSpaceSessionID), strAttr(attrs, attrConversationID)),
		ConversationID:       strAttr(attrs, attrConversationID),
		ParentConversationID: strAttr(attrs, attrSlipSpaceParentConversationID),
		AgentID:              strAttr(attrs, attrAgentID),
		UserID:               strAttr(attrs, attrEnduserID),
		Provider:             strAttr(attrs, attrGenAIProvider),
		Model:                strAttr(attrs, attrModel),
		Configuration:        strAttr(attrs, attrSlipSpaceConfiguration),
		Protocol:             strAttr(attrs, attrSlipSpaceProtocol),
		StatusCode:           int(intAttr(attrs, attrStatusCode)),
		TokensIn:             intAttr(attrs, attrInputTokens),
		TokensOut:            intAttr(attrs, attrOutputTokens),
		CostUSD:              floatAttr(attrs, attrCostUSD),
		Tags:                 strSliceAttr(attrs, attrSlipSpaceTags),
		SpanEvent:            buildSpanEvent(attrs, span, contentMaxBytes),
	}, true
}

// buildSpanEvent serializes the complete span as the immutable JSONB blob: every
// merged attribute keyed by its OTLP name (so nothing is discarded — invariant
// #1's telemetry analogue), the derived slipspace.latency_ms, the tags / rules_fired
// arrays parsed from slipspace.tags / slipspace.rules_fired, and the bounded gen_ai
// content under gen_ai_content. The string keys the projection + drill-down
// (store.SpanFields) read are stamped here.
func buildSpanEvent(attrs map[string]*commonpb.AnyValue, span *tracepb.Span, contentMaxBytes int) []byte {
	out := make(map[string]any, len(attrs)+4)
	for k, v := range attrs {
		// slipspace.tags / slipspace.rules_fired are copied below as the derived,
		// normalised tags / rules_fired arrays (keyTags / keyRules) that every
		// reader (GIN indexes, facets, EventFilter, store.SpanFields) consumes.
		// Skip the verbatim raw attrs here so the blob carries each value once
		// under its canonical bare key, not a byte-identical duplicate.
		if k == attrSlipSpaceTags || k == attrSlipSpaceRulesFired {
			continue
		}
		out[k] = anyValueNative(v)
	}
	// Derived: request wall time from the span bounds (the gateway also emits it,
	// but deriving keeps a value even if the attribute is absent).
	out[keyLatencyMs] = spanLatencyMs(span)
	if gw := strAttr(attrs, attrResourceGatewayID); gw != "" {
		out[keyGatewayID] = gw
	}
	// tags / rules_fired are emitted as string arrays; normalise to []string so
	// the GIN @> filter and jsonb_array_elements_text facet see a JSON array.
	if tags := strSliceAttr(attrs, attrSlipSpaceTags); tags != nil {
		out[keyTags] = tags
	}
	if rules := strSliceAttr(attrs, attrSlipSpaceRulesFired); rules != nil {
		out[keyRules] = rules
	}
	if c := captureContent(span, contentMaxBytes); c != nil {
		out[keyContent] = json.RawMessage(c)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return []byte("{}")
	}
	return b
}

func mergeAttrs(resource, span []*commonpb.KeyValue) map[string]*commonpb.AnyValue {
	out := make(map[string]*commonpb.AnyValue, len(resource)+len(span))
	for _, kv := range resource {
		if kv != nil {
			out[kv.GetKey()] = kv.GetValue()
		}
	}
	for _, kv := range span {
		if kv != nil {
			out[kv.GetKey()] = kv.GetValue() // span attributes win over resource
		}
	}
	return out
}

// firstNonEmpty returns the first non-empty string in vals, or "". Used to
// fall back from slipspace.session_id to gen_ai.conversation.id for spans that
// predate the session-root attribute (where conversation == session).
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func strAttr(attrs map[string]*commonpb.AnyValue, key string) string {
	v := attrs[key]
	if v == nil {
		return ""
	}
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return x.StringValue
	case *commonpb.AnyValue_IntValue:
		return strconv.FormatInt(x.IntValue, 10)
	default:
		return ""
	}
}

func intAttr(attrs map[string]*commonpb.AnyValue, key string) int64 {
	v := attrs[key]
	if v == nil {
		return 0
	}
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_IntValue:
		return x.IntValue
	case *commonpb.AnyValue_DoubleValue:
		return int64(x.DoubleValue)
	case *commonpb.AnyValue_StringValue:
		n, _ := strconv.ParseInt(x.StringValue, 10, 64)
		return n
	default:
		return 0
	}
}

// floatAttr reads a float-valued attribute (slipspace.cost.usd). intAttr is
// deliberately not reused — it truncates DoubleValue to int64, which would
// zero every sub-dollar cost.
func floatAttr(attrs map[string]*commonpb.AnyValue, key string) float64 {
	v := attrs[key]
	if v == nil {
		return 0
	}
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_DoubleValue:
		return x.DoubleValue
	case *commonpb.AnyValue_IntValue:
		return float64(x.IntValue)
	case *commonpb.AnyValue_StringValue:
		f, _ := strconv.ParseFloat(x.StringValue, 64)
		return f
	default:
		return 0
	}
}

// strSliceAttr reads a string-array attribute (slipspace.tags / slipspace.rules_fired)
// into a []string. A single string value is accepted as a one-element slice.
// Returns nil when absent so the key is omitted from the blob entirely.
func strSliceAttr(attrs map[string]*commonpb.AnyValue, key string) []string {
	v := attrs[key]
	if v == nil {
		return nil
	}
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_ArrayValue:
		vals := x.ArrayValue.GetValues()
		out := make([]string, 0, len(vals))
		for _, el := range vals {
			if s, ok := el.GetValue().(*commonpb.AnyValue_StringValue); ok && s.StringValue != "" {
				out = append(out, s.StringValue)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case *commonpb.AnyValue_StringValue:
		if x.StringValue == "" {
			return nil
		}
		return []string{x.StringValue}
	default:
		return nil
	}
}

// anyValueNative converts an OTLP AnyValue to a Go value json.Marshal renders
// as the natural JSON type, so the blob round-trips the wire shape (strings as
// strings, ints as numbers, arrays as arrays). An unsupported kind yields nil.
func anyValueNative(v *commonpb.AnyValue) any {
	if v == nil {
		return nil
	}
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return x.StringValue
	case *commonpb.AnyValue_IntValue:
		return x.IntValue
	case *commonpb.AnyValue_DoubleValue:
		return x.DoubleValue
	case *commonpb.AnyValue_BoolValue:
		return x.BoolValue
	case *commonpb.AnyValue_ArrayValue:
		vals := x.ArrayValue.GetValues()
		out := make([]any, 0, len(vals))
		for _, el := range vals {
			out = append(out, anyValueNative(el))
		}
		return out
	case *commonpb.AnyValue_KvlistValue:
		kvs := x.KvlistValue.GetValues()
		out := make(map[string]any, len(kvs))
		for _, kv := range kvs {
			out[kv.GetKey()] = anyValueNative(kv.GetValue())
		}
		return out
	default:
		return nil
	}
}

// boolAttr reads a bool-valued attribute. A string "true" is also accepted —
// some SDKs serialise booleans as strings — but any other kind is false.
func boolAttr(attrs map[string]*commonpb.AnyValue, key string) bool {
	v := attrs[key]
	if v == nil {
		return false
	}
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_BoolValue:
		return x.BoolValue
	case *commonpb.AnyValue_StringValue:
		return x.StringValue == "true"
	default:
		return false
	}
}

// spanStart returns the span's start time as the event's observed_at. A zero
// start time yields the zero time, which the store defaults to now().
func spanStart(span *tracepb.Span) time.Time {
	ns := span.GetStartTimeUnixNano()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, int64(ns)).UTC() //nolint:gosec // a span start unix-nano never overflows int64
}

func spanLatencyMs(span *tracepb.Span) int64 {
	start, end := span.GetStartTimeUnixNano(), span.GetEndTimeUnixNano()
	if end <= start {
		return 0
	}
	return int64((end - start) / 1_000_000) //nolint:gosec // a span duration in ms never overflows int64
}
