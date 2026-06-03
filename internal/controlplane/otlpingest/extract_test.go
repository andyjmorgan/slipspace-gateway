package otlpingest

import (
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
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
