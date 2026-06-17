package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/andyjmorgan/sluice-gateway/internal/arbiter/store"
)

// --- attribute builders ---

func strKV(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}
func intKV(k string, v int64) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: v}}}
}
func boolKV(k string, v bool) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: v}}}
}
func strSliceKV(k string, vs ...string) *commonpb.KeyValue {
	vals := make([]*commonpb.AnyValue, len(vs))
	for i, v := range vs {
		vals[i] = &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}
	}
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{
		Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: vals}},
	}}
}

// --- stub stores ---

type eventSink struct {
	events []store.RequestEvent
	checks [][]store.CheckTask
	err    error
}

func (s *eventSink) UpsertRequestEvent(_ context.Context, e store.RequestEvent) error {
	if s.err != nil {
		return s.err
	}
	s.events = append(s.events, e)
	return nil
}

func (s *eventSink) UpsertRequestEventWithChecks(_ context.Context, e store.RequestEvent, checks []store.CheckTask) error {
	if s.err != nil {
		return s.err
	}
	s.events = append(s.events, e)
	s.checks = append(s.checks, checks)
	return nil
}

type metricSink struct {
	points []store.MetricPoint
	err    error
}

func (s *metricSink) InsertMetricPoints(_ context.Context, pts []store.MetricPoint) error {
	if s.err != nil {
		return s.err
	}
	s.points = append(s.points, pts...)
	return nil
}

// testContentCap is the byte cap the extraction tests pass to EventFromSpan /
// captureContent; matches the config default. Cap-specific behaviour (0 =
// unlimited, over-cap truncation) is exercised in helpers_test.go.
const testContentCap = 16 * 1024

// --- EventFromSpan ---

func TestEventFromSpan_GenAIAttributes(t *testing.T) {
	// Single-writer model: the span carries GenAI semconv AND the gateway facts
	// (sluice.*). EventFromSpan projects provider/model/configuration/protocol/
	// status/identity AND the input/output token counts (#318) to columns, and
	// stores the COMPLETE span (every attribute + content + derived latency/tags)
	// into SpanEvent. The remaining measurements (latency, streaming, method, the
	// cached / cache-creation token counts) are read back out of the blob via
	// SpanFields, not off dedicated columns.
	span := &tracepb.Span{
		StartTimeUnixNano: 1_000_000_000,
		EndTimeUnixNano:   1_500_000_000, // 500ms
		Attributes: []*commonpb.KeyValue{
			strKV(attrCorrelationID, "corr-1"),
			strKV(attrGenAIProvider, "anthropic"),
			strKV(attrModel, "claude-x"),
			strKV(attrSluiceConfiguration, "dev"),
			strKV(attrSluiceProtocol, "messages"),
			strKV("slipspace.method", "POST"),
			intKV(attrStatusCode, 200),
			intKV(attrInputTokens, 10),
			intKV(attrOutputTokens, 20),
			intKV(attrCacheReadTokens, 5),
			intKV(attrCacheCreationTokens, 2),
			strKV(attrConversationID, "sess-9"),
			strKV(attrAgentID, "agt-9"),
			strKV(attrEnduserID, "usr-9"),
			boolKV(attrRequestStream, true),
			strKV(attrResourceGatewayID, "gw-9"),
		},
	}
	e, ok := EventFromSpan(nil, span, testContentCap)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	// Projected columns.
	if e.CorrelationID != "corr-1" || e.Provider != "anthropic" || e.Model != "claude-x" {
		t.Errorf("gen_ai dims = %+v", e)
	}
	if e.Configuration != "dev" || e.Protocol != "messages" || e.StatusCode != 200 {
		t.Errorf("gateway columns = %+v", e)
	}
	if e.SessionID != "sess-9" || e.AgentID != "agt-9" || e.UserID != "usr-9" {
		t.Errorf("identity columns = %+v", e)
	}
	// Promoted token columns (#318) — projected off the span, not just in the blob.
	if e.TokensIn != 10 || e.TokensOut != 20 {
		t.Errorf("token columns = (in %d, out %d), want (10, 20)", e.TokensIn, e.TokensOut)
	}
	// observed_at is the SPAN START time, not ingest now().
	if e.ObservedAt.UnixNano() != 1_000_000_000 {
		t.Errorf("observed_at = %d, want span start 1e9", e.ObservedAt.UnixNano())
	}
	// Measurements + method + streaming + tags + gateway id ride the blob.
	f := e.DecodeSpanFields()
	if f.LatencyMs != 500 {
		t.Errorf("latency = %d, want 500", f.LatencyMs)
	}
	if f.TokensIn != 10 || f.TokensOut != 20 || f.TokensCached != 5 || f.TokensCacheCreation != 2 {
		t.Errorf("tokens = %+v", f)
	}
	if !f.Streaming || f.Method != "POST" || f.GatewayID != "gw-9" {
		t.Errorf("blob fields = %+v", f)
	}
}

func TestEventFromSpan_TagsAndRulesToBlob(t *testing.T) {
	// sluice.tags / sluice.rules_fired arrays normalise into JSON arrays in the
	// blob so the GIN @> filter + the facet unnest see real arrays.
	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{
		strKV(attrCorrelationID, "corr-tags"),
		strSliceKV(attrSluiceTags, "eu", "pii"),
		strSliceKV(attrSluiceRulesFired, "redirect"),
	}}
	e, ok := EventFromSpan(nil, span, testContentCap)
	if !ok {
		t.Fatal("ok = false")
	}
	f := e.DecodeSpanFields()
	if len(f.Tags) != 2 || f.Tags[0] != "eu" || f.Tags[1] != "pii" {
		t.Errorf("tags = %+v", f.Tags)
	}
	if len(f.RulesFired) != 1 || f.RulesFired[0] != "redirect" {
		t.Errorf("rules_fired = %+v", f.RulesFired)
	}
	// The blob carries each array exactly once, under the canonical bare key.
	// The verbatim sluice.-prefixed raw attrs must NOT be duplicated in —
	// nothing reads them, and every reader consumes tags / rules_fired.
	var blob map[string]json.RawMessage
	if err := json.Unmarshal(e.SpanEvent, &blob); err != nil {
		t.Fatalf("unmarshal span_event: %v", err)
	}
	if _, ok := blob["tags"]; !ok {
		t.Error("span_event missing canonical key tags")
	}
	if _, ok := blob["rules_fired"]; !ok {
		t.Error("span_event missing canonical key rules_fired")
	}
	if _, ok := blob[attrSluiceTags]; ok {
		t.Errorf("span_event still carries redundant raw key %q", attrSluiceTags)
	}
	if _, ok := blob[attrSluiceRulesFired]; ok {
		t.Errorf("span_event still carries redundant raw key %q", attrSluiceRulesFired)
	}
}

func TestAnyValueNative_Kinds(t *testing.T) {
	cases := []struct {
		v    *commonpb.AnyValue
		want any
	}{
		{nil, nil},
		{&commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "x"}}, "x"},
		{&commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 7}}, int64(7)},
		{&commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: 1.5}}, 1.5},
		{&commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: true}}, true},
		{&commonpb.AnyValue{Value: &commonpb.AnyValue_BytesValue{BytesValue: []byte("z")}}, nil},
	}
	for _, c := range cases {
		if got := anyValueNative(c.v); got != c.want {
			t.Errorf("anyValueNative(%v) = %v, want %v", c.v, got, c.want)
		}
	}
	// Array of strings.
	arr := &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{
		Values: []*commonpb.AnyValue{{Value: &commonpb.AnyValue_StringValue{StringValue: "a"}}},
	}}}
	if got, ok := anyValueNative(arr).([]any); !ok || len(got) != 1 || got[0] != "a" {
		t.Errorf("array native = %v", anyValueNative(arr))
	}
	// Kvlist (nested object).
	kv := &commonpb.AnyValue{Value: &commonpb.AnyValue_KvlistValue{KvlistValue: &commonpb.KeyValueList{
		Values: []*commonpb.KeyValue{strKV("k", "v")},
	}}}
	if got, ok := anyValueNative(kv).(map[string]any); !ok || got["k"] != "v" {
		t.Errorf("kvlist native = %v", anyValueNative(kv))
	}
}

func TestStrSliceAttr(t *testing.T) {
	attrs := map[string]*commonpb.AnyValue{
		"arr":   strSliceKV("x", "a", "b").Value,
		"empty": strSliceKV("x").Value,
		"one":   {Value: &commonpb.AnyValue_StringValue{StringValue: "solo"}},
		"blank": {Value: &commonpb.AnyValue_StringValue{StringValue: ""}},
		"int":   {Value: &commonpb.AnyValue_IntValue{IntValue: 1}},
	}
	if got := strSliceAttr(attrs, "arr"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("array = %v", got)
	}
	if strSliceAttr(attrs, "empty") != nil {
		t.Error("empty array -> nil")
	}
	if got := strSliceAttr(attrs, "one"); len(got) != 1 || got[0] != "solo" {
		t.Errorf("single string = %v", got)
	}
	if strSliceAttr(attrs, "blank") != nil || strSliceAttr(attrs, "int") != nil || strSliceAttr(attrs, "missing") != nil {
		t.Error("blank/int/missing -> nil")
	}
}

func TestBuildSpanEvent_ContentAndDerived(t *testing.T) {
	// A span with content attributes + no end time: the blob carries the content
	// under gen_ai_content and a zero derived latency.
	span := &tracepb.Span{
		StartTimeUnixNano: 1_000_000_000,
		Attributes: []*commonpb.KeyValue{
			strKV(attrCorrelationID, "c"),
			strKV(attrInputMessages, `[{"role":"user"}]`),
		},
	}
	e, ok := EventFromSpan(nil, span, testContentCap)
	if !ok {
		t.Fatal("ok = false")
	}
	var blob map[string]json.RawMessage
	if err := json.Unmarshal(e.SpanEvent, &blob); err != nil {
		t.Fatalf("blob decode: %v", err)
	}
	if _, ok := blob["gen_ai_content"]; !ok {
		t.Errorf("content not folded into blob: %s", e.SpanEvent)
	}
	if _, ok := blob["slipspace.latency_ms"]; !ok {
		t.Errorf("derived latency missing: %s", e.SpanEvent)
	}
}

func TestEventFromSpan_NoCorrelationID(t *testing.T) {
	if _, ok := EventFromSpan(nil, &tracepb.Span{Attributes: []*commonpb.KeyValue{strKV(attrModel, "m")}}, testContentCap); ok {
		t.Fatal("ok = true for span without correlation id")
	}
}

func TestEventFromSpan_NilSpan(t *testing.T) {
	if _, ok := EventFromSpan(nil, nil, testContentCap); ok {
		t.Fatal("ok = true for nil span")
	}
}

func TestEventFromSpan_ResourceMerge(t *testing.T) {
	// Resource attributes are merged under span attributes; gen_ai.provider.name
	// on the resource is picked up when the span carries only the correlation id.
	resource := []*commonpb.KeyValue{strKV(attrGenAIProvider, "openai")}
	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{strKV(attrCorrelationID, "c")}}
	e, ok := EventFromSpan(resource, span, testContentCap)
	if !ok {
		t.Fatal("ok = false")
	}
	if e.Provider != "openai" {
		t.Errorf("provider = %q, want openai (resource merge)", e.Provider)
	}
}

// --- captureContent ---

func TestCaptureContent_Attrs(t *testing.T) {
	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{
		strKV(attrInputMessages, `[{"role":"user"}]`),
		strKV(attrOutputMessages, `[{"role":"assistant"}]`),
		strKV(attrToolDefinitions, "not json"), // dropped
	}}
	b := captureContent(span, testContentCap)
	if b == nil {
		t.Fatal("content = nil")
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("content: %v", err)
	}
	if _, ok := got["input_messages"]; !ok {
		t.Error("missing input_messages")
	}
	if _, ok := got["tool_definitions"]; ok {
		t.Error("invalid tool_definitions should be omitted")
	}
}

func TestCaptureContent_None(t *testing.T) {
	if b := captureContent(&tracepb.Span{}, testContentCap); b != nil {
		t.Errorf("content = %s, want nil", b)
	}
}

// --- TraceReceiver ---

func traceReq(spans ...*tracepb.Span) *collectortrace.ExportTraceServiceRequest {
	return &collectortrace.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			ScopeSpans: []*tracepb.ScopeSpans{{Spans: spans}},
		}},
	}
}

func TestTraceReceiver_Export(t *testing.T) {
	sink := &eventSink{}
	r := NewTraceReceiver(sink, discard(), testContentCap, nil)
	good := &tracepb.Span{Attributes: []*commonpb.KeyValue{strKV(attrCorrelationID, "c1")}}
	skip := &tracepb.Span{Attributes: []*commonpb.KeyValue{strKV(attrModel, "m")}} // no corr id
	if _, err := r.Export(context.Background(), traceReq(good, skip)); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(sink.events) != 1 {
		t.Fatalf("stored %d events, want 1", len(sink.events))
	}
}

func TestTraceReceiver_StoreErrorIsSwallowed(t *testing.T) {
	r := NewTraceReceiver(&eventSink{err: errors.New("db down")}, discard(), testContentCap, nil)
	good := &tracepb.Span{Attributes: []*commonpb.KeyValue{strKV(attrCorrelationID, "c1")}}
	if _, err := r.Export(context.Background(), traceReq(good)); err != nil {
		t.Fatalf("Export must not propagate store errors: %v", err)
	}
}

// --- MetricsReceiver / PointsFromMetric ---

func gaugeMetric(name string, v int64) *metricspb.Metric {
	return &metricspb.Metric{
		Name: name,
		Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{
			DataPoints: []*metricspb.NumberDataPoint{{
				TimeUnixNano: 2_000_000_000,
				Value:        &metricspb.NumberDataPoint_AsInt{AsInt: v},
				Attributes:   []*commonpb.KeyValue{strKV("provider", "anthropic")},
			}},
		}},
	}
}

func TestPointsFromMetric_GaugeAndSkip(t *testing.T) {
	pts := PointsFromMetric(nil, gaugeMetric("sluice.requests", 7))
	if len(pts) != 1 {
		t.Fatalf("points = %d, want 1", len(pts))
	}
	if pts[0].Name != "sluice.requests" || pts[0].Value != 7 {
		t.Errorf("point = %+v", pts[0])
	}
	// Histogram is skipped; nil metric is skipped.
	if p := PointsFromMetric(nil, &metricspb.Metric{Name: "h", Data: &metricspb.Metric_Histogram{}}); p != nil {
		t.Errorf("histogram should yield nil, got %v", p)
	}
	if p := PointsFromMetric(nil, nil); p != nil {
		t.Errorf("nil metric should yield nil, got %v", p)
	}
}

func TestMetricsReceiver_Export(t *testing.T) {
	sink := &metricSink{}
	r := NewMetricsReceiver(sink, discard())
	req := &collectormetrics.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{gaugeMetric("m", 3)}}},
		}},
	}
	if _, err := r.Export(context.Background(), req); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(sink.points) != 1 {
		t.Fatalf("points = %d, want 1", len(sink.points))
	}
}

// TestEventFromSpan_ConversationParent covers the unified subagent projection:
// gen_ai.conversation.id is the thread, sluice.session_id the bundle root, and
// sluice.parent_conversation_id the hierarchy edge.
func TestEventFromSpan_ConversationParent(t *testing.T) {
	span := &tracepb.Span{
		StartTimeUnixNano: 1_000_000_000,
		EndTimeUnixNano:   1_200_000_000,
		Attributes: []*commonpb.KeyValue{
			strKV(attrCorrelationID, "corr-sub"),
			strKV(attrConversationID, "thread-2"),
			strKV(attrSluiceSessionID, "sess-1"),
			strKV(attrSluiceParentConversationID, "sess-1"),
			intKV(attrStatusCode, 200),
		},
	}
	e, ok := EventFromSpan(nil, span, testContentCap)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if e.SessionID != "sess-1" {
		t.Errorf("session_id = %q, want sess-1 (the bundle root)", e.SessionID)
	}
	if e.ConversationID != "thread-2" {
		t.Errorf("conversation_id = %q, want thread-2 (the thread)", e.ConversationID)
	}
	if e.ParentConversationID != "sess-1" {
		t.Errorf("parent_conversation_id = %q, want sess-1", e.ParentConversationID)
	}
}

// TestEventFromSpan_SessionFallsBackToConversation covers a span predating the
// sluice.session_id attribute: the bundle root falls back to the conversation.
func TestEventFromSpan_SessionFallsBackToConversation(t *testing.T) {
	span := &tracepb.Span{
		StartTimeUnixNano: 1_000_000_000,
		EndTimeUnixNano:   1_100_000_000,
		Attributes: []*commonpb.KeyValue{
			strKV(attrCorrelationID, "corr-old"),
			strKV(attrConversationID, "sess-only"),
			intKV(attrStatusCode, 200),
		},
	}
	e, ok := EventFromSpan(nil, span, testContentCap)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if e.SessionID != "sess-only" || e.ConversationID != "sess-only" {
		t.Errorf("session/conversation = (%q, %q), want both sess-only", e.SessionID, e.ConversationID)
	}
	if e.ParentConversationID != "" {
		t.Errorf("parent = %q, want empty", e.ParentConversationID)
	}
}
