package otlpingest

import (
	"encoding/json"
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

func kvStr(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}

func kvInt(k string, v int64) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: v}}}
}

func kvBool(k string, v bool) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: v}}}
}

func kvDouble(k string, v float64) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: v}}}
}

func TestEventFromSpan_AttributeValueKinds(t *testing.T) {
	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{
		kvStr(attrCorrelationID, "c"),
		kvInt(attrGatewayID, 123),        // strAttr over an int value -> "123"
		kvBool(attrModel, true),          // strAttr over an unsupported kind -> ""
		kvDouble(attrOutputTokens, 33.7), // intAttr over a double -> 33
		kvBool(attrInputTokens, true),    // intAttr over an unsupported kind -> 0
	}}

	e, ok := EventFromSpan(nil, span)
	if !ok {
		t.Fatal("want ok=true")
	}
	if e.GatewayID != "123" {
		t.Errorf("int-valued string attr = %q, want \"123\"", e.GatewayID)
	}
	if e.Model != "" {
		t.Errorf("bool-valued string attr = %q, want \"\"", e.Model)
	}
	if e.TokensOut != 33 {
		t.Errorf("double-valued int attr = %d, want 33", e.TokensOut)
	}
	if e.TokensIn != 0 {
		t.Errorf("bool-valued int attr = %d, want 0", e.TokensIn)
	}
}

func TestEventFromSpan_FullGenAISpan(t *testing.T) {
	resource := []*commonpb.KeyValue{
		kvStr(attrGatewayID, "beta-sluice"),
		kvStr(attrConfiguration, "production"),
		kvStr(attrModel, "resource-loses"), // span should win over this
	}
	span := &tracepb.Span{
		StartTimeUnixNano: 1_000_000_000,
		EndTimeUnixNano:   1_250_000_000, // +250ms
		Attributes: []*commonpb.KeyValue{
			kvStr(attrCorrelationID, "corr-1"),
			kvStr(attrModel, "gpt-4o"),
			kvStr(attrBackend, "openai"),
			kvStr(attrProtocol, "chat"),
			kvInt(attrInputTokens, 11),
			kvInt(attrOutputTokens, 22),
			kvInt(attrStatusCode, 200),
		},
	}

	e, ok := EventFromSpan(resource, span)
	if !ok {
		t.Fatal("want ok=true for a gen_ai span with a correlation id")
	}
	if e.CorrelationID != "corr-1" || e.GatewayID != "beta-sluice" || e.Configuration != "production" {
		t.Errorf("labels = %+v", e)
	}
	if e.Model != "gpt-4o" || e.Backend != "openai" || e.Protocol != "chat" {
		t.Errorf("span attrs not extracted / span did not win: %+v", e)
	}
	if e.TokensIn != 11 || e.TokensOut != 22 || e.StatusCode != 200 {
		t.Errorf("metrics = %+v", e)
	}
	if e.LatencyMs != 250 {
		t.Errorf("latency = %d ms, want 250", e.LatencyMs)
	}
}

func TestEventFromSpan_SkipsWithoutCorrelationID(t *testing.T) {
	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{kvStr(attrModel, "gpt-4o")}}
	if _, ok := EventFromSpan(nil, span); ok {
		t.Error("want ok=false when no correlation id")
	}
	if _, ok := EventFromSpan(nil, nil); ok {
		t.Error("want ok=false for a nil span")
	}
}

func TestEventFromSpan_BackendFallsBackToProvider(t *testing.T) {
	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{
		kvStr(attrCorrelationID, "c"),
		kvStr(attrProvider, "anthropic"), // no sluice.backend
	}}
	e, ok := EventFromSpan(nil, span)
	if !ok || e.Backend != "anthropic" {
		t.Errorf("backend fallback to provider failed: ok=%v backend=%q", ok, e.Backend)
	}
}

func TestEventFromSpan_ProtocolFallsBackToEndpoint(t *testing.T) {
	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{
		kvStr(attrCorrelationID, "c"),
		kvStr(attrEndpoint, "chat_completions"), // no sluice.protocol
	}}
	e, ok := EventFromSpan(nil, span)
	if !ok || e.Protocol != "chat_completions" {
		t.Errorf("protocol fallback to endpoint failed: ok=%v protocol=%q", ok, e.Protocol)
	}
}

func TestEventFromSpan_ProtocolWinsOverEndpoint(t *testing.T) {
	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{
		kvStr(attrCorrelationID, "c"),
		kvStr(attrProtocol, "chat"),
		kvStr(attrEndpoint, "chat_completions"),
	}}
	e, ok := EventFromSpan(nil, span)
	if !ok || e.Protocol != "chat" {
		t.Errorf("sluice.protocol should win over sluice.endpoint: ok=%v protocol=%q", ok, e.Protocol)
	}
}

func TestEventFromSpan_TokenAttrAsString(t *testing.T) {
	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{
		kvStr(attrCorrelationID, "c"),
		kvStr(attrInputTokens, "42"), // some SDKs emit usage as strings
	}}
	e, _ := EventFromSpan(nil, span)
	if e.TokensIn != 42 {
		t.Errorf("string token attr not parsed: %d", e.TokensIn)
	}
}

func TestEventFromSpan_ZeroLatencyWhenEndBeforeStart(t *testing.T) {
	span := &tracepb.Span{
		StartTimeUnixNano: 2_000,
		EndTimeUnixNano:   1_000,
		Attributes:        []*commonpb.KeyValue{kvStr(attrCorrelationID, "c")},
	}
	if e, _ := EventFromSpan(nil, span); e.LatencyMs != 0 {
		t.Errorf("latency = %d, want 0 when end <= start", e.LatencyMs)
	}
}

// TestEventFromSpan_EnrichmentAttrs covers the fleet-enrichment fields: the
// gen_ai.* cache/session/streaming attrs the gateway already emits plus the
// additive sluice.* dimensions, including the tags + rules_fired detail JSONB.
func TestEventFromSpan_EnrichmentAttrs(t *testing.T) {
	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{
		kvStr(attrCorrelationID, "corr-1"),
		// gen_ai.* the gateway already emits.
		kvInt(attrCacheReadTokens, 64),
		kvInt(attrCacheCreationTokens, 8),
		kvStr(attrConversationID, "bundle-9"),
		kvBool(attrRequestStream, true),
		// additive sluice.* fleet dimensions.
		kvStr(attrMethod, "POST"),
		kvInt(attrUpstreamStatus, 502),
		kvStr(attrSessionIDSource, "X-Agentling-Task-Id"),
		kvStr(attrAPIKeyName, "internal-svc"),
		kvStr(attrPolicyRef, "failover-pool"),
		kvStr(attrTags, "team:platform,env:prod"),
		kvStr(attrRulesFired, "redirect-claude,add-team-tag"),
	}}

	e, ok := EventFromSpan(nil, span)
	if !ok {
		t.Fatal("want ok=true")
	}
	if e.TokensCached != 64 || e.TokensCacheCreation != 8 {
		t.Errorf("cache tokens = (%d,%d), want (64,8)", e.TokensCached, e.TokensCacheCreation)
	}
	if e.SessionID != "bundle-9" || e.SessionIDSource != "X-Agentling-Task-Id" {
		t.Errorf("session = (%q,%q)", e.SessionID, e.SessionIDSource)
	}
	if !e.Streaming {
		t.Error("streaming = false, want true")
	}
	if e.Method != "POST" || e.UpstreamStatus != 502 {
		t.Errorf("method/upstream = (%q,%d)", e.Method, e.UpstreamStatus)
	}
	if e.APIKeyName != "internal-svc" || e.PolicyRef != "failover-pool" {
		t.Errorf("apikey/policy = (%q,%q)", e.APIKeyName, e.PolicyRef)
	}
	var d configdb.EventDetail
	if err := json.Unmarshal(e.Detail, &d); err != nil {
		t.Fatalf("detail not valid JSON: %v (raw %s)", err, e.Detail)
	}
	if got := d.Tags; len(got) != 2 || got[0] != "team:platform" || got[1] != "env:prod" {
		t.Errorf("detail.tags = %v", got)
	}
	if got := d.RulesFired; len(got) != 2 || got[0] != "redirect-claude" || got[1] != "add-team-tag" {
		t.Errorf("detail.rules_fired = %v", got)
	}
}

// TestEventFromSpan_NoDetailWhenNoTagsOrRules leaves detail nil so the JSONB
// column stays SQL NULL on a lean single-shot request.
func TestEventFromSpan_NoDetailWhenNoTagsOrRules(t *testing.T) {
	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{kvStr(attrCorrelationID, "c")}}
	e, _ := EventFromSpan(nil, span)
	if e.Detail != nil {
		t.Errorf("detail = %s, want nil when no tags/rules", e.Detail)
	}
	if e.Streaming || e.UpstreamStatus != 0 || e.TokensCached != 0 {
		t.Errorf("lean event picked up phantom values: %+v", e)
	}
}

// TestEventFromSpan_RequestStreamAsString covers an SDK that serialises the
// boolean stream flag as the string "true".
func TestEventFromSpan_RequestStreamAsString(t *testing.T) {
	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{
		kvStr(attrCorrelationID, "c"),
		kvStr(attrRequestStream, "true"),
	}}
	if e, _ := EventFromSpan(nil, span); !e.Streaming {
		t.Error("string-valued stream attr not parsed as true")
	}
	// A non-bool, non-"true" string is false.
	span2 := &tracepb.Span{Attributes: []*commonpb.KeyValue{
		kvStr(attrCorrelationID, "c"),
		kvInt(attrRequestStream, 1),
	}}
	if e, _ := EventFromSpan(nil, span2); e.Streaming {
		t.Error("int-valued stream attr should not parse as true")
	}
}
