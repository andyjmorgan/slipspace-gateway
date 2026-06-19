package ingest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/store"
)

// spanEventWith builds a request_events span_event blob carrying gen_ai_content
// verbatim — the shape the OTLP ingest stores and ToolCallsFromEvent reads back.
func spanEventWith(content string) []byte {
	b, _ := json.Marshal(map[string]json.RawMessage{"gen_ai_content": json.RawMessage(content)})
	return b
}

func TestToolCallsFromEvent_CallAndResult(t *testing.T) {
	// An assistant response proposing a tool call (output), and — modelling the
	// next request of the same session — the client's result (input). The two map
	// to a call half and a response half joined on the id.
	e := store.RequestEvent{
		CorrelationID: "corr-1", SessionID: "sess-1",
		Provider: "anthropic", Configuration: "dev", Protocol: "messages", Model: "claude-x",
		SpanEvent: spanEventWith(`{
			"output_messages":[{"role":"assistant","parts":[
				{"type":"tool_call","id":"toolu_1","name":"Skill","arguments":{"skill":"ship"},"executor":"client"}
			]}],
			"input_messages":[{"role":"user","parts":[
				{"type":"tool_call_response","id":"toolu_0","result":"prior result"}
			]}]
		}`),
	}
	obs := ToolCallsFromEvent(e)
	if len(obs) != 2 {
		t.Fatalf("observations = %d, want 2: %+v", len(obs), obs)
	}
	call := obs[0]
	if call.Kind != store.ToolCallHalf || call.ToolCallID != "toolu_1" || call.ToolName != "Skill" {
		t.Errorf("call = %+v", call)
	}
	if string(call.Arguments) != `{"skill":"ship"}` || call.ArgumentsChars != len(`{"skill":"ship"}`) {
		t.Errorf("call args = %s (%d chars)", call.Arguments, call.ArgumentsChars)
	}
	// Call half stamps the span's routing facts + identity.
	if call.Provider != "anthropic" || call.Configuration != "dev" || call.Protocol != "messages" ||
		call.Model != "claude-x" || call.SessionID != "sess-1" || call.CorrelationID != "corr-1" || call.Executor != "client" {
		t.Errorf("call facts = %+v", call)
	}
	res := obs[1]
	if res.Kind != store.ToolResultHalf || res.ToolCallID != "toolu_0" || res.Result != "prior result" {
		t.Errorf("result = %+v", res)
	}
	if res.CorrelationID != "corr-1" || res.SessionID != "sess-1" {
		t.Errorf("result span facts = %+v", res)
	}
}

func TestToolCallsFromEvent_ServerExecutedSameSpan(t *testing.T) {
	// A provider-executed tool: the call AND its result both ride the assistant
	// output of one span — both extracted, joined by id, executor=server.
	e := store.RequestEvent{
		CorrelationID: "corr-2",
		SpanEvent: spanEventWith(`{"output_messages":[{"role":"assistant","parts":[
			{"type":"tool_call","id":"srv_1","name":"web_search","arguments":{"q":"x"},"executor":"server"},
			{"type":"tool_call_response","id":"srv_1","result":"hits","executor":"server"}
		]}]}`),
	}
	obs := ToolCallsFromEvent(e)
	if len(obs) != 2 || obs[0].Kind != store.ToolCallHalf || obs[1].Kind != store.ToolResultHalf {
		t.Fatalf("observations = %+v", obs)
	}
	if obs[0].Executor != "server" || obs[1].Executor != "server" {
		t.Errorf("executor not propagated: %+v", obs)
	}
}

func TestToolCallsFromEvent_SkipsIDless(t *testing.T) {
	// Gemini function calls carry no id on the wire; an id-less part can't be
	// keyed or joined, so it yields no observation.
	e := store.RequestEvent{
		CorrelationID: "corr-3",
		SpanEvent: spanEventWith(`{"output_messages":[{"role":"model","parts":[
			{"type":"tool_call","name":"lookup","arguments":{"q":"y"}},
			{"type":"tool_call_response","result":"z"}
		]}]}`),
	}
	if obs := ToolCallsFromEvent(e); obs != nil {
		t.Errorf("observations = %+v, want nil (no ids)", obs)
	}
}

func TestToolCallsFromEvent_NullAndAbsentArgs(t *testing.T) {
	e := store.RequestEvent{
		CorrelationID: "corr-4",
		SpanEvent: spanEventWith(`{"output_messages":[{"role":"assistant","parts":[
			{"type":"tool_call","id":"a","name":"NoArgs"},
			{"type":"tool_call","id":"b","name":"NullArgs","arguments":null}
		]}]}`),
	}
	obs := ToolCallsFromEvent(e)
	if len(obs) != 2 {
		t.Fatalf("observations = %d, want 2", len(obs))
	}
	for _, o := range obs {
		if o.Arguments != nil {
			t.Errorf("%s args = %s, want nil", o.ToolName, o.Arguments)
		}
	}
}

func TestToolCallsFromEvent_ResultCapped(t *testing.T) {
	big := strings.Repeat("x", maxToolResultBytes+500)
	content, _ := json.Marshal(map[string]any{
		"input_messages": []any{map[string]any{"role": "user", "parts": []any{
			map[string]any{"type": "tool_call_response", "id": "toolu_big", "result": big},
		}}},
	})
	e := store.RequestEvent{CorrelationID: "corr-5", SpanEvent: spanEventWith(string(content))}
	obs := ToolCallsFromEvent(e)
	if len(obs) != 1 {
		t.Fatalf("observations = %d, want 1", len(obs))
	}
	if len(obs[0].Result) > maxToolResultBytes {
		t.Errorf("result not capped: %d bytes", len(obs[0].Result))
	}
	if obs[0].ResultChars != len(big) {
		t.Errorf("result_chars = %d, want true size %d", obs[0].ResultChars, len(big))
	}
}

func TestToolCallsFromEvent_NoContent(t *testing.T) {
	cases := map[string][]byte{
		"nil blob":      nil,
		"no content":    []byte(`{"gen_ai.request.model":"m"}`),
		"empty content": []byte(`{"gen_ai_content":null}`),
		"bad blob":      []byte(`not json`),
		"bad content":   spanEventWith(`"not an object"`),
		"no tool parts": spanEventWith(`{"input_messages":[{"role":"user","parts":[{"type":"text","content":"hi"}]}]}`),
	}
	for name, blob := range cases {
		t.Run(name, func(t *testing.T) {
			if obs := ToolCallsFromEvent(store.RequestEvent{CorrelationID: "c", SpanEvent: blob}); obs != nil {
				t.Errorf("observations = %+v, want nil", obs)
			}
		})
	}
}

func TestCapRunes(t *testing.T) {
	if capRunes("abc", 0) != "abc" {
		t.Error("cap 0 disables")
	}
	if capRunes("abc", 10) != "abc" {
		t.Error("under cap unchanged")
	}
	// Multibyte: cutting mid-rune backs up to a rune boundary, so the result is
	// always valid UTF-8 and never longer than the cap.
	got := capRunes("héllo", 2) // 'h'=1 byte, 'é'=2 bytes
	if got != "h" {
		t.Errorf("cap mid-rune = %q, want %q", got, "h")
	}
}

// TestTraceReceiver_IndexesToolCalls covers the end of the ingest path: a span
// with tool content is upserted AND its tool calls indexed; a tool-call write
// failure is swallowed (best-effort) and never fails the span ingest.
func TestTraceReceiver_IndexesToolCalls(t *testing.T) {
	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{
		strKV(attrCorrelationID, "c1"),
		strKV(attrOutputMessages, `[{"role":"assistant","parts":[{"type":"tool_call","id":"toolu_1","name":"Skill","arguments":{"a":1}}]}]`),
	}}
	sink := &eventSink{}
	r := NewTraceReceiver(sink, discard(), 0, nil) // cap 0 keeps content whole
	if _, err := r.Export(context.Background(), traceReq(span)); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(sink.events) != 1 {
		t.Fatalf("events = %d, want 1", len(sink.events))
	}
	if len(sink.toolCalls) != 1 || len(sink.toolCalls[0]) != 1 || sink.toolCalls[0][0].ToolName != "Skill" {
		t.Fatalf("tool calls = %+v", sink.toolCalls)
	}

	// A tool-call write error is logged and swallowed: the span ingest still
	// succeeds (Export never errors).
	failSink := &eventSink{toolErr: context.DeadlineExceeded}
	r2 := NewTraceReceiver(failSink, discard(), 0, nil)
	if _, err := r2.Export(context.Background(), traceReq(span)); err != nil {
		t.Fatalf("Export must swallow tool-call write error: %v", err)
	}
	if len(failSink.events) != 1 {
		t.Errorf("span still written: events = %d, want 1", len(failSink.events))
	}
}

// stubExploder returns a fixed check set, modelling the scanner being on.
type stubExploder struct{ checks []store.CheckTask }

func (s stubExploder) Explode(store.RequestEvent) []store.CheckTask { return s.checks }

// TestTraceReceiver_WriteEventExploderPaths covers writeEvent's scanner-on
// branches: a span that explodes to nothing takes the plain upsert; one that
// explodes to checks takes the atomic span+checks write.
func TestTraceReceiver_WriteEventExploderPaths(t *testing.T) {
	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{strKV(attrCorrelationID, "c1")}}

	// Explodes to nothing -> plain upsert (no checks recorded).
	empty := &eventSink{}
	if _, err := NewTraceReceiver(empty, discard(), 0, stubExploder{}).Export(context.Background(), traceReq(span)); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(empty.checks) != 0 || len(empty.events) != 1 {
		t.Errorf("explode-empty took the checks path: %+v", empty)
	}

	// Explodes to a check -> atomic span+checks write.
	withChecks := &eventSink{}
	exp := stubExploder{checks: []store.CheckTask{{CorrelationID: "c1", UnitID: "u", CheckType: "pii"}}}
	if _, err := NewTraceReceiver(withChecks, discard(), 0, exp).Export(context.Background(), traceReq(span)); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(withChecks.checks) != 1 || len(withChecks.checks[0]) != 1 {
		t.Errorf("explode-nonempty did not take the checks path: %+v", withChecks.checks)
	}
}
