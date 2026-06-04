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

	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
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

// --- stub stores ---

type eventSink struct {
	events []store.RequestEvent
	err    error
}

func (s *eventSink) UpsertRequestEvent(_ context.Context, e store.RequestEvent) error {
	if s.err != nil {
		return s.err
	}
	s.events = append(s.events, e)
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

// --- EventFromSpan ---

func TestEventFromSpan_FullAttributes(t *testing.T) {
	span := &tracepb.Span{
		StartTimeUnixNano: 1_000_000_000,
		EndTimeUnixNano:   1_500_000_000, // 500ms
		Attributes: []*commonpb.KeyValue{
			strKV(attrCorrelationID, "corr-1"),
			strKV(attrGatewayID, "gw-a"),
			strKV(attrConfiguration, "default"),
			strKV(attrBackend, "anthropic"),
			strKV(attrModel, "claude-x"),
			strKV(attrProtocol, "messages"),
			strKV(attrMethod, "POST"),
			intKV(attrStatusCode, 200),
			intKV(attrUpstreamStatus, 200),
			intKV(attrInputTokens, 10),
			intKV(attrOutputTokens, 20),
			intKV(attrCacheReadTokens, 5),
			intKV(attrCacheCreationTokens, 2),
			strKV(attrConversationID, "sess-9"),
			strKV(attrSessionIDSource, "x-session"),
			strKV(attrAPIKeyName, "key-1"),
			strKV(attrPolicyRef, "pol-1"),
			boolKV(attrRequestStream, true),
			strKV(attrTags, "a,b"),
			strKV(attrRulesFired, "r1,r2"),
		},
	}
	e, ok := EventFromSpan(nil, span)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if e.CorrelationID != "corr-1" || e.GatewayID != "gw-a" || e.Backend != "anthropic" {
		t.Errorf("labels = %+v", e)
	}
	if e.LatencyMs != 500 {
		t.Errorf("latency = %d, want 500", e.LatencyMs)
	}
	if e.TokensIn != 10 || e.TokensOut != 20 || e.TokensCached != 5 || e.TokensCacheCreation != 2 {
		t.Errorf("tokens = %+v", e)
	}
	if !e.Streaming {
		t.Error("streaming = false, want true")
	}
	var d store.EventDetail
	if err := json.Unmarshal(e.Detail, &d); err != nil {
		t.Fatalf("detail: %v", err)
	}
	if len(d.Tags) != 2 || len(d.RulesFired) != 2 {
		t.Errorf("detail = %+v", d)
	}
}

func TestEventFromSpan_NoCorrelationID(t *testing.T) {
	if _, ok := EventFromSpan(nil, &tracepb.Span{Attributes: []*commonpb.KeyValue{strKV(attrModel, "m")}}); ok {
		t.Fatal("ok = true for span without correlation id")
	}
}

func TestEventFromSpan_NilSpan(t *testing.T) {
	if _, ok := EventFromSpan(nil, nil); ok {
		t.Fatal("ok = true for nil span")
	}
}

func TestEventFromSpan_ResourceFallbacks(t *testing.T) {
	// backend falls back to provider; protocol falls back to endpoint; resource
	// attrs are merged under span attrs.
	resource := []*commonpb.KeyValue{strKV(attrProvider, "openai"), strKV(attrEndpoint, "chat_completions")}
	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{strKV(attrCorrelationID, "c")}}
	e, ok := EventFromSpan(resource, span)
	if !ok {
		t.Fatal("ok = false")
	}
	if e.Backend != "openai" {
		t.Errorf("backend = %q, want openai (provider fallback)", e.Backend)
	}
	if e.Protocol != "chat_completions" {
		t.Errorf("protocol = %q, want chat_completions (endpoint fallback)", e.Protocol)
	}
}

// --- captureContent ---

func TestCaptureContent_Attrs(t *testing.T) {
	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{
		strKV(attrInputMessages, `[{"role":"user"}]`),
		strKV(attrOutputMessages, `[{"role":"assistant"}]`),
		strKV(attrToolDefinitions, "not json"), // dropped
	}}
	b := captureContent(span)
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
	if b := captureContent(&tracepb.Span{}); b != nil {
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
	r := NewTraceReceiver(sink, discard())
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
	r := NewTraceReceiver(&eventSink{err: errors.New("db down")}, discard())
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
