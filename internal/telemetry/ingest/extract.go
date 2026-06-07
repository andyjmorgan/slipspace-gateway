// Package ingest maps the feeds gateways push — gen_ai OTLP spans and metrics,
// and the HMAC-trusted Record webhook — into the telemetry store. This file is
// the pure extraction layer: a gen_ai OTLP span (plus its resource attributes)
// becomes the gen_ai half of a store.RequestEvent.
//
// Channel boundary (telemetry design): a gen_ai span carries GenAI semconv only
// (model / provider / usage / duration) plus the single Sluice-specific
// attribute correlation_id — the stitch join key. Every other gateway fact
// (configuration, protocol, method, rule chain, attempts, api-key, ...) arrives
// on the Record feed (see record.go), not the span. The two feeds converge on
// one request_events row by correlation_id: the OTLP upsert owns the gen_ai
// columns, the Record upsert owns the gateway columns, either order (no clobber).
package ingest

import (
	"strconv"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

const (
	// attrCorrelationID is the only Sluice-specific attribute read off a
	// gen_ai span — the join key that stitches the OTLP feed to the Record.
	attrCorrelationID = "sluice.correlation_id"

	attrModel         = "gen_ai.request.model"
	attrGenAIProvider = "gen_ai.provider.name"
	attrInputTokens   = "gen_ai.usage.input_tokens"  //nolint:gosec // OTLP attribute key, not a credential
	attrOutputTokens  = "gen_ai.usage.output_tokens" //nolint:gosec // OTLP attribute key, not a credential
	attrStatusCode    = "http.response.status_code"

	// Cache-token + session + streaming attributes carried on the gen_ai
	// span — all GenAI semconv, all part of the gen_ai channel.
	attrCacheReadTokens     = "gen_ai.usage.cache_read.input_tokens"     //nolint:gosec // OTLP attribute key, not a credential
	attrCacheCreationTokens = "gen_ai.usage.cache_creation.input_tokens" //nolint:gosec // OTLP attribute key, not a credential
	attrConversationID      = "gen_ai.conversation.id"
	attrAgentID             = "gen_ai.agent.id"
	attrEnduserID           = "enduser.id"
	attrRequestStream       = "gen_ai.request.stream"
)

// EventFromSpan extracts the gen_ai half of a request event from a span and its
// resource attributes. Resource attributes are merged under the span's own
// (span wins). Returns ok=false when the span carries no correlation id — the
// key everything joins on. Only gen_ai semconv columns are populated; the
// gateway columns (configuration, protocol, method, detail, ...) are left zero
// for the Record feed to fill via UpsertGatewayRecord. contentMaxBytes bounds
// the captured gen_ai content (<= 0 keeps it whole).
func EventFromSpan(resourceAttrs []*commonpb.KeyValue, span *tracepb.Span, contentMaxBytes int) (store.RequestEvent, bool) {
	if span == nil {
		return store.RequestEvent{}, false
	}
	attrs := mergeAttrs(resourceAttrs, span.GetAttributes())

	corr := strAttr(attrs, attrCorrelationID)
	if corr == "" {
		return store.RequestEvent{}, false
	}

	return store.RequestEvent{
		CorrelationID:       corr,
		Provider:            strAttr(attrs, attrGenAIProvider),
		Model:               strAttr(attrs, attrModel),
		StatusCode:          int(intAttr(attrs, attrStatusCode)),
		LatencyMs:           spanLatencyMs(span),
		TokensIn:            intAttr(attrs, attrInputTokens),
		TokensOut:           intAttr(attrs, attrOutputTokens),
		TokensCached:        intAttr(attrs, attrCacheReadTokens),
		TokensCacheCreation: intAttr(attrs, attrCacheCreationTokens),
		SessionID:           strAttr(attrs, attrConversationID),
		AgentID:             strAttr(attrs, attrAgentID),
		UserID:              strAttr(attrs, attrEnduserID),
		Streaming:           boolAttr(attrs, attrRequestStream),
		GenAIContent:        captureContent(span, contentMaxBytes),
	}, true
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

func spanLatencyMs(span *tracepb.Span) int64 {
	start, end := span.GetStartTimeUnixNano(), span.GetEndTimeUnixNano()
	if end <= start {
		return 0
	}
	return int64((end - start) / 1_000_000) //nolint:gosec // a span duration in ms never overflows int64
}
