// Package ingest maps the feeds gateways push — gen_ai + sluice OTLP spans and
// metrics, and HMAC-trusted large-payload webhooks — into the telemetry store.
// This file is the pure extraction layer: an OTLP span (plus its resource
// attributes) becomes a store.RequestEvent.
//
// Attribute conventions: OTel GenAI semconv (gen_ai.*) for model/provider/usage,
// plus sluice.* for the post-rule fleet labels (gateway, configuration, provider,
// protocol) and the correlation id that joins an event to its payloads.
package ingest

import (
	"encoding/json"
	"strconv"
	"strings"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

const (
	attrCorrelationID = "sluice.correlation_id"
	attrGatewayID     = "sluice.gateway_id"
	attrConfiguration = "sluice.configuration"
	attrProvider      = "sluice.provider"
	attrProtocol      = "sluice.protocol"
	attrEndpoint      = "sluice.endpoint"
	attrModel         = "gen_ai.request.model"
	attrGenAIProvider = "gen_ai.provider.name"
	attrInputTokens   = "gen_ai.usage.input_tokens"  //nolint:gosec // OTLP attribute key, not a credential
	attrOutputTokens  = "gen_ai.usage.output_tokens" //nolint:gosec // OTLP attribute key, not a credential
	attrStatusCode    = "http.response.status_code"

	// Cache-token + session + streaming attributes the gateway already emits
	// on the gen_ai.* span, captured so the event mirrors the connector
	// Record's token + session detail.
	attrCacheReadTokens     = "gen_ai.usage.cache_read.input_tokens"     //nolint:gosec // OTLP attribute key, not a credential
	attrCacheCreationTokens = "gen_ai.usage.cache_creation.input_tokens" //nolint:gosec // OTLP attribute key, not a credential
	attrConversationID      = "gen_ai.conversation.id"
	attrRequestStream       = "gen_ai.request.stream"

	// Additive sluice.* fleet dimensions emitted by the gateway's emitTrace —
	// the post-rule tags, fired-rule names, policy ref, api-key name, upstream
	// status, session-id source, and inbound method.
	attrTags            = "sluice.tags"
	attrRulesFired      = "sluice.rules_fired"
	attrPolicyRef       = "sluice.policy_ref"
	attrAPIKeyName      = "sluice.api_key_name" //nolint:gosec // OTLP attribute key naming the key, not a credential
	attrUpstreamStatus  = "sluice.upstream_status"
	attrSessionIDSource = "sluice.session_id_source"
	attrMethod          = "sluice.method"
)

// EventFromSpan extracts a request event from a span and its resource
// attributes. Resource attributes are merged under the span's own (span wins).
// Returns ok=false when the span is not a sluice request event — i.e. it
// carries no correlation id, the key everything joins on.
func EventFromSpan(resourceAttrs []*commonpb.KeyValue, span *tracepb.Span) (store.RequestEvent, bool) {
	if span == nil {
		return store.RequestEvent{}, false
	}
	attrs := mergeAttrs(resourceAttrs, span.GetAttributes())

	corr := strAttr(attrs, attrCorrelationID)
	if corr == "" {
		return store.RequestEvent{}, false
	}

	provider := strAttr(attrs, attrProvider)
	if provider == "" {
		provider = strAttr(attrs, attrGenAIProvider)
	}

	protocol := strAttr(attrs, attrProtocol)
	if protocol == "" {
		protocol = strAttr(attrs, attrEndpoint)
	}

	return store.RequestEvent{
		CorrelationID:       corr,
		GatewayID:           strAttr(attrs, attrGatewayID),
		Configuration:       strAttr(attrs, attrConfiguration),
		Provider:            provider,
		Model:               strAttr(attrs, attrModel),
		Protocol:            protocol,
		Method:              strAttr(attrs, attrMethod),
		StatusCode:          int(intAttr(attrs, attrStatusCode)),
		UpstreamStatus:      int(intAttr(attrs, attrUpstreamStatus)),
		LatencyMs:           spanLatencyMs(span),
		TokensIn:            intAttr(attrs, attrInputTokens),
		TokensOut:           intAttr(attrs, attrOutputTokens),
		TokensCached:        intAttr(attrs, attrCacheReadTokens),
		TokensCacheCreation: intAttr(attrs, attrCacheCreationTokens),
		SessionID:           strAttr(attrs, attrConversationID),
		SessionIDSource:     strAttr(attrs, attrSessionIDSource),
		APIKeyName:          strAttr(attrs, attrAPIKeyName),
		PolicyRef:           strAttr(attrs, attrPolicyRef),
		Streaming:           boolAttr(attrs, attrRequestStream),
		Detail:              eventDetail(attrs),
		GenAIContent:        captureContent(span),
	}, true
}

// eventDetail packs the post-rule tags and fired-rule names — comma-joined in
// the span attributes — into the event's JSONB detail. Returns nil when neither
// is present, leaving the column SQL NULL rather than an empty object.
func eventDetail(attrs map[string]*commonpb.AnyValue) []byte {
	tags := splitCSV(strAttr(attrs, attrTags))
	rules := splitCSV(strAttr(attrs, attrRulesFired))
	if len(tags) == 0 && len(rules) == 0 {
		return nil
	}
	b, err := json.Marshal(store.EventDetail{Tags: tags, RulesFired: rules})
	if err != nil {
		return nil
	}
	return b
}

// splitCSV splits a comma-joined attribute value, returning nil for an empty
// string so an absent attribute yields no slice rather than [""].
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
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
