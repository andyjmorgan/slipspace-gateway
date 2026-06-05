package ingest

import (
	"context"
	"net/http"
	"strings"
	"testing"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func TestStrAttr_Kinds(t *testing.T) {
	attrs := map[string]*commonpb.AnyValue{
		"s":   {Value: &commonpb.AnyValue_StringValue{StringValue: "x"}},
		"i":   {Value: &commonpb.AnyValue_IntValue{IntValue: 7}},
		"b":   {Value: &commonpb.AnyValue_BoolValue{BoolValue: true}},
		"nil": nil,
	}
	if strAttr(attrs, "s") != "x" {
		t.Error("string")
	}
	if strAttr(attrs, "i") != "7" {
		t.Error("int->string")
	}
	if strAttr(attrs, "b") != "" {
		t.Error("bool should be empty via strAttr")
	}
	if strAttr(attrs, "nil") != "" || strAttr(attrs, "missing") != "" {
		t.Error("nil/missing")
	}
}

func TestIntAttr_Kinds(t *testing.T) {
	attrs := map[string]*commonpb.AnyValue{
		"i": {Value: &commonpb.AnyValue_IntValue{IntValue: 7}},
		"d": {Value: &commonpb.AnyValue_DoubleValue{DoubleValue: 3.9}},
		"s": {Value: &commonpb.AnyValue_StringValue{StringValue: "42"}},
		"x": {Value: &commonpb.AnyValue_BoolValue{BoolValue: true}},
	}
	if intAttr(attrs, "i") != 7 || intAttr(attrs, "d") != 3 || intAttr(attrs, "s") != 42 {
		t.Error("int/double/string parse")
	}
	if intAttr(attrs, "x") != 0 || intAttr(attrs, "missing") != 0 {
		t.Error("bool/missing -> 0")
	}
}

func TestBoolAttr_Kinds(t *testing.T) {
	attrs := map[string]*commonpb.AnyValue{
		"b": {Value: &commonpb.AnyValue_BoolValue{BoolValue: true}},
		"s": {Value: &commonpb.AnyValue_StringValue{StringValue: "true"}},
		"x": {Value: &commonpb.AnyValue_IntValue{IntValue: 1}},
	}
	if !boolAttr(attrs, "b") || !boolAttr(attrs, "s") {
		t.Error("bool/string-true")
	}
	if boolAttr(attrs, "x") || boolAttr(attrs, "missing") {
		t.Error("int/missing -> false")
	}
}

func TestAnyValueString_Kinds(t *testing.T) {
	cases := map[*commonpb.AnyValue]string{
		nil: "",
		{Value: &commonpb.AnyValue_StringValue{StringValue: "x"}}:       "x",
		{Value: &commonpb.AnyValue_IntValue{IntValue: 7}}:               "7",
		{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: 1.5}}:       "1.5",
		{Value: &commonpb.AnyValue_BoolValue{BoolValue: true}}:          "true",
		{Value: &commonpb.AnyValue_BytesValue{BytesValue: []byte("z")}}: "",
	}
	for v, want := range cases {
		if got := anyValueString(v); got != want {
			t.Errorf("anyValueString(%v) = %q, want %q", v, got, want)
		}
	}
}

func TestCaptureContent_EventsFallback(t *testing.T) {
	span := &tracepb.Span{Events: []*tracepb.Span_Event{
		{Name: "gen_ai.user.message", Attributes: []*commonpb.KeyValue{strKV("content", "hi")}},
		{Name: "other.event", Attributes: nil}, // ignored, not gen_ai.*
	}}
	b := captureContent(span, testContentCap)
	if b == nil || !strings.Contains(string(b), "gen_ai.user.message") {
		t.Fatalf("events fallback = %s", b)
	}
}

func TestMarshalBounded_Truncates(t *testing.T) {
	big := strings.Repeat("x", testContentCap+10)
	out := marshalBounded(map[string]string{"v": big}, testContentCap)
	if !strings.Contains(string(out), `"truncated":true`) {
		t.Fatalf("expected truncation marker, got %s", string(out)[:40])
	}
}

func TestMarshalBounded_ZeroDisablesCap(t *testing.T) {
	// 0 (and negative) means unlimited: the full payload survives even when it
	// would blow past the default cap. This is the "unset the boundary" path an
	// operator gets from content_max_bytes: 0.
	big := strings.Repeat("x", testContentCap+10)
	for _, cap := range []int{0, -1} {
		out := marshalBounded(map[string]string{"v": big}, cap)
		if strings.Contains(string(out), `"truncated":true`) {
			t.Fatalf("cap=%d truncated; want full payload", cap)
		}
		if !strings.Contains(string(out), big) {
			t.Fatalf("cap=%d dropped the payload", cap)
		}
	}
}

func TestNumberValue_Double(t *testing.T) {
	dp := &metricspb.NumberDataPoint{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 2.5}}
	if numberValue(dp) != 2.5 {
		t.Error("double value")
	}
	if numberValue(&metricspb.NumberDataPoint{}) != 0 {
		t.Error("empty -> 0")
	}
}

func TestPointsFromMetric_SumDouble(t *testing.T) {
	m := &metricspb.Metric{Name: "lat", Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{
		DataPoints: []*metricspb.NumberDataPoint{{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 1.25}}},
	}}}
	pts := PointsFromMetric(nil, m)
	if len(pts) != 1 || pts[0].Value != 1.25 {
		t.Fatalf("sum points = %+v", pts)
	}
}

func TestNewOTLPServer(t *testing.T) {
	srv := NewOTLPServer(NewTraceReceiver(&eventSink{}, discard(), testContentCap), NewMetricsReceiver(&metricSink{}, discard()))
	if srv == nil {
		t.Fatal("nil server")
	}
	if len(srv.GetServiceInfo()) == 0 {
		t.Error("expected registered services")
	}
	srv.Stop()
}

func TestTraceReceiver_EmptyBatch(t *testing.T) {
	r := NewTraceReceiver(&eventSink{}, discard(), testContentCap)
	if _, err := r.Export(context.Background(), &collectortrace.ExportTraceServiceRequest{}); err != nil {
		t.Fatalf("empty Export: %v", err)
	}
}

func TestRecord_TooLarge(t *testing.T) {
	st := &recordStore{}
	h := NewRecordHandler(testReg(), st, discard())
	body := strings.Repeat("a", maxRecordBytes+1)
	resp := post(t, h, "gw-a", sign("secret-a", []byte(body)), []byte(body))
	if resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.Code)
	}
	if st.events != 0 {
		t.Error("nothing should be stored")
	}
}
