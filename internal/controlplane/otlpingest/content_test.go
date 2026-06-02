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
