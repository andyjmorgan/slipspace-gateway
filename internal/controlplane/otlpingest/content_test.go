package otlpingest

import (
	"encoding/json"
	"strings"
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func spanEvent(name string, attrs ...*commonpb.KeyValue) *tracepb.Span_Event {
	return &tracepb.Span_Event{Name: name, Attributes: attrs}
}

func TestCaptureContent(t *testing.T) {
	span := &tracepb.Span{Events: []*tracepb.Span_Event{
		spanEvent("gen_ai.user.message", kvStr("content", "hello"), kvInt("index", 0)),
		spanEvent("gen_ai.choice", kvStr("content", "hi there")),
		spanEvent("exception", kvStr("type", "boom")), // non-gen_ai, skipped
	}}

	b := captureContent(span)
	if b == nil {
		t.Fatal("want captured content")
	}
	var events []map[string]any
	if err := json.Unmarshal(b, &events); err != nil {
		t.Fatalf("content not JSON: %v\n%s", err, b)
	}
	if len(events) != 2 {
		t.Fatalf("captured %d events, want 2 (non-gen_ai dropped)", len(events))
	}
	if events[0]["name"] != "gen_ai.user.message" {
		t.Errorf("event = %+v", events[0])
	}
	attrs := events[0]["attributes"].(map[string]any)
	if attrs["content"] != "hello" || attrs["index"] != "0" {
		t.Errorf("attributes = %+v (int stringified)", attrs)
	}
}

func TestCaptureContent_FromAttributes(t *testing.T) {
	inputMsgs := `[{"role":"user","parts":[{"type":"text","content":"what is the weather?"}]}]`
	outputMsgs := `[{"role":"assistant","parts":[{"type":"tool_call","id":"call_1","name":"get_weather","arguments":{"city":"Dublin"}}]}]`
	toolDefs := `[{"type":"function","name":"get_weather","description":"look up the weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}]`
	sysInstr := `[{"type":"text","content":"you are a helpful assistant"}]`

	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{
		kvStr(attrInputMessages, inputMsgs),
		kvStr(attrOutputMessages, outputMsgs),
		kvStr(attrToolDefinitions, toolDefs),
		kvStr(attrSystemInstructions, sysInstr),
		kvStr("gen_ai.request.model", "gpt-4o"), // non-content attr, ignored
	}}

	b := captureContent(span)
	if b == nil {
		t.Fatal("want captured content")
	}
	var got struct {
		InputMessages      json.RawMessage `json:"input_messages"`
		OutputMessages     json.RawMessage `json:"output_messages"`
		ToolDefinitions    json.RawMessage `json:"tool_definitions"`
		SystemInstructions json.RawMessage `json:"system_instructions"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("content not JSON: %v\n%s", err, b)
	}

	// Each field must re-embed as a nested JSON value, not a doubly-escaped
	// string — assert structurally rather than by string compare so key order
	// is irrelevant.
	var in []map[string]any
	if err := json.Unmarshal(got.InputMessages, &in); err != nil {
		t.Fatalf("input_messages not nested JSON: %v\n%s", err, got.InputMessages)
	}
	if in[0]["role"] != "user" {
		t.Errorf("input role = %v", in[0]["role"])
	}

	var out []map[string]any
	if err := json.Unmarshal(got.OutputMessages, &out); err != nil {
		t.Fatalf("output_messages not nested JSON: %v\n%s", err, got.OutputMessages)
	}
	parts := out[0]["parts"].([]any)
	call := parts[0].(map[string]any)
	if call["type"] != "tool_call" || call["name"] != "get_weather" {
		t.Errorf("tool call not preserved: %+v", call)
	}
	if args := call["arguments"].(map[string]any); args["city"] != "Dublin" {
		t.Errorf("tool call arguments lost: %+v", args)
	}

	var defs []map[string]any
	if err := json.Unmarshal(got.ToolDefinitions, &defs); err != nil {
		t.Fatalf("tool_definitions not nested JSON: %v\n%s", err, got.ToolDefinitions)
	}
	if defs[0]["name"] != "get_weather" {
		t.Errorf("tool def name lost: %+v", defs[0])
	}
	if len(got.SystemInstructions) == 0 {
		t.Error("system_instructions missing")
	}
}

func TestCaptureContent_AttrsPreferredOverEvents(t *testing.T) {
	// A span carrying both content attributes and gen_ai.* events must take the
	// attribute path (structured object), not the legacy event array.
	span := &tracepb.Span{
		Attributes: []*commonpb.KeyValue{
			kvStr(attrInputMessages, `[{"role":"user","parts":[{"type":"text","content":"hi"}]}]`),
		},
		Events: []*tracepb.Span_Event{
			spanEvent("gen_ai.user.message", kvStr("content", "stale")),
		},
	}
	b := captureContent(span)
	var got map[string]json.RawMessage
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("want structured object, got %s (err %v)", b, err)
	}
	if _, ok := got["input_messages"]; !ok {
		t.Errorf("attribute path not taken: %s", b)
	}
}

func TestCaptureContent_SkipsInvalidJSONAttr(t *testing.T) {
	// A content attribute whose value is not valid JSON is dropped; with no
	// valid content attribute and no events, capture yields nil.
	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{
		kvStr(attrInputMessages, "not json"),
	}}
	if b := captureContent(span); b != nil {
		t.Errorf("want nil for invalid-JSON-only span, got %s", b)
	}
}

func TestCaptureContent_AttrsTruncateOversize(t *testing.T) {
	big := `["` + strings.Repeat("x", maxContentBytes+100) + `"]`
	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{
		kvStr(attrInputMessages, big),
	}}
	b := captureContent(span)
	if len(b) >= maxContentBytes {
		t.Fatalf("content not truncated: %d bytes", len(b))
	}
	var marker map[string]any
	if err := json.Unmarshal(b, &marker); err != nil || marker["truncated"] != true {
		t.Errorf("want a truncation marker, got %s (err %v)", b, err)
	}
}

func TestCaptureContent_NoneWhenNoGenAIEvents(t *testing.T) {
	span := &tracepb.Span{Events: []*tracepb.Span_Event{spanEvent("exception", kvStr("type", "x"))}}
	if b := captureContent(span); b != nil {
		t.Errorf("want nil, got %s", b)
	}
	if b := captureContent(&tracepb.Span{}); b != nil {
		t.Errorf("want nil for no events, got %s", b)
	}
}

func TestCaptureContent_TruncatesOversize(t *testing.T) {
	big := strings.Repeat("x", maxContentBytes+100)
	span := &tracepb.Span{Events: []*tracepb.Span_Event{
		spanEvent("gen_ai.user.message", kvStr("content", big)),
	}}
	b := captureContent(span)
	if len(b) >= maxContentBytes {
		t.Fatalf("content not truncated: %d bytes", len(b))
	}
	var marker map[string]any
	if err := json.Unmarshal(b, &marker); err != nil || marker["truncated"] != true {
		t.Errorf("want a truncation marker, got %s (err %v)", b, err)
	}
}

func TestAnyValueString_Kinds(t *testing.T) {
	cases := map[*commonpb.AnyValue]string{
		{Value: &commonpb.AnyValue_StringValue{StringValue: "s"}}: "s",
		{Value: &commonpb.AnyValue_IntValue{IntValue: 5}}:         "5",
		{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: 2.5}}: "2.5",
		{Value: &commonpb.AnyValue_BoolValue{BoolValue: true}}:    "true",
		{}: "", // unset
	}
	for v, want := range cases {
		if got := anyValueString(v); got != want {
			t.Errorf("anyValueString = %q, want %q", got, want)
		}
	}
	if got := anyValueString(nil); got != "" {
		t.Errorf("nil = %q, want empty", got)
	}
}
