package responses

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestResponsesRequest_StringInput(t *testing.T) {
	in := []byte(`{` +
		`"input":"Say hi.",` +
		`"max_output_tokens":256,` +
		`"model":"gpt-4o-mini",` +
		`"temperature":0.4,` +
		`"truncation":"auto"` +
		`}`)
	var req ResponsesRequest
	if err := json.Unmarshal(in, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Model != "gpt-4o-mini" {
		t.Fatalf("model = %q", req.Model)
	}
	if string(req.Input) != `"Say hi."` {
		t.Fatalf("input = %s", req.Input)
	}
	roundTripJSON(t, in, &req)
}

func TestResponsesRequest_ArrayInputWithReasoning(t *testing.T) {
	in := []byte(`{` +
		`"input":[{"content":[{"text":"hello","type":"input_text"}],"role":"user","type":"message"}],` +
		`"model":"o4-mini",` +
		`"reasoning":{"effort":"low"},` +
		`"tools":[{"name":"search","parameters":{"type":"object"},"type":"function"}]` +
		`}`)
	var req ResponsesRequest
	if err := json.Unmarshal(in, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Reasoning == nil || req.Reasoning.Effort != "low" {
		t.Fatalf("reasoning = %+v", req.Reasoning)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "search" {
		t.Fatalf("tools = %+v", req.Tools)
	}
	roundTripJSON(t, in, &req)
}

func TestResponsesRequest_UnknownTopLevelField(t *testing.T) {
	in := []byte(`{"future_field":"keep","input":"hi","model":"m"}`)
	var req ResponsesRequest
	if err := json.Unmarshal(in, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(req.Extra["future_field"]) != `"keep"` {
		t.Fatalf("extras: %v", req.Extra)
	}
	roundTripJSON(t, in, &req)
}

func TestResponsesResponse_NonStreaming(t *testing.T) {
	in := []byte(`{` +
		`"created_at":1700000000,` +
		`"id":"resp_abc",` +
		`"model":"gpt-4o-mini",` +
		`"object":"response",` +
		`"output":[{"content":[{"text":"Hello.","type":"output_text"}],"id":"msg_1","role":"assistant","status":"completed","type":"message"}],` +
		`"output_text":"Hello.",` +
		`"status":"completed",` +
		`"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}` +
		`}`)
	var resp ResponsesResponse
	if err := json.Unmarshal(in, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "completed" {
		t.Fatalf("status = %q", resp.Status)
	}
	if resp.Usage.InputTokens != 5 || resp.Usage.OutputTokens != 3 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	if len(resp.Output) != 1 || resp.Output[0].Type != "message" {
		t.Fatalf("output = %+v", resp.Output)
	}
	roundTripJSON(t, in, &resp)
}

func TestResponsesResponse_IncompleteWithReasoning(t *testing.T) {
	in := []byte(`{` +
		`"created_at":1700000000,` +
		`"id":"resp_inc",` +
		`"incomplete_details":{"reason":"max_output_tokens"},` +
		`"model":"o4-mini",` +
		`"object":"response",` +
		`"reasoning":{"effort":"high"},` +
		`"status":"incomplete"` +
		`}`)
	var resp ResponsesResponse
	if err := json.Unmarshal(in, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.IncompleteDetails.Reason != "max_output_tokens" {
		t.Fatalf("incomplete reason = %q", resp.IncompleteDetails.Reason)
	}
	if resp.Reasoning.Effort != "high" {
		t.Fatalf("reasoning effort = %q", resp.Reasoning.Effort)
	}
	roundTripJSON(t, in, &resp)
}

func TestStreamEvent_AllKnownTypes(t *testing.T) {
	cases := []struct {
		name string

		json string

		typ string

		want any
	}{
		{name: "created", json: `{"response":{"id":"resp_1"},"sequence_number":0,"type":"response.created"}`, typ: "response.created", want: &ResponseCreatedEvent{}},
		{name: "in_progress", json: `{"response":{"id":"resp_1"},"sequence_number":1,"type":"response.in_progress"}`, typ: "response.in_progress", want: &ResponseInProgressEvent{}},
		{name: "item_added", json: `{"item":{"id":"msg_1","type":"message"},"output_index":0,"sequence_number":2,"type":"response.output_item.added"}`, typ: "response.output_item.added", want: &OutputItemAddedEvent{}},
		{name: "part_added", json: `{"content_index":0,"item_id":"msg_1","output_index":0,"part":{"text":"","type":"output_text"},"sequence_number":3,"type":"response.content_part.added"}`, typ: "response.content_part.added", want: &ContentPartAddedEvent{}},
		{name: "text_delta", json: `{"content_index":0,"delta":"Hello","item_id":"msg_1","output_index":0,"sequence_number":4,"type":"response.output_text.delta"}`, typ: "response.output_text.delta", want: &OutputTextDeltaEvent{}},
		{name: "text_done", json: `{"content_index":0,"item_id":"msg_1","output_index":0,"sequence_number":5,"text":"Hello.","type":"response.output_text.done"}`, typ: "response.output_text.done", want: &OutputTextDoneEvent{}},
		{name: "part_done", json: `{"content_index":0,"item_id":"msg_1","output_index":0,"part":{"text":"Hello.","type":"output_text"},"sequence_number":6,"type":"response.content_part.done"}`, typ: "response.content_part.done", want: &ContentPartDoneEvent{}},
		{name: "item_done", json: `{"item":{"id":"msg_1","type":"message"},"output_index":0,"sequence_number":7,"type":"response.output_item.done"}`, typ: "response.output_item.done", want: &OutputItemDoneEvent{}},
		{name: "completed", json: `{"response":{"id":"resp_1","status":"completed"},"sequence_number":8,"type":"response.completed"}`, typ: "response.completed", want: &ResponseCompletedEvent{}},
		{name: "failed", json: `{"error":{"message":"boom"},"sequence_number":9,"type":"response.failed"}`, typ: "response.failed", want: &ResponseFailedEvent{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, err := UnmarshalStreamEvent([]byte(tc.json))
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if reflect.TypeOf(e) != reflect.TypeOf(tc.want) {
				t.Fatalf("got %T want %T", e, tc.want)
			}
			if e.EventType() != tc.typ {
				t.Fatalf("event type = %q want %q", e.EventType(), tc.typ)
			}
			out, err := json.Marshal(e)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !jsonValueEqual(t, []byte(tc.json), out) {
				t.Fatalf("drift\n in: %s\nout: %s", tc.json, out)
			}
		})
	}
}

func TestStreamEvent_UnknownDiscriminatorRoundTrips(t *testing.T) {
	in := []byte(`{"future_field":42,"sequence_number":7,"type":"response.tool_call.delta"}`)
	e, err := UnmarshalStreamEvent(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	u, ok := e.(*UnknownEvent)
	if !ok {
		t.Fatalf("got %T", e)
	}
	if u.EventType() != "response.tool_call.delta" {
		t.Fatalf("event type = %q", u.EventType())
	}
	if string(u.Extra["future_field"]) != `42` {
		t.Fatalf("extras: %v", u.Extra)
	}
	out, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("drift\n in: %s\nout: %s", in, out)
	}
}

func TestStreamEvent_DeltaWithUnknownField(t *testing.T) {
	in := []byte(`{"content_index":0,"delta":"Hi","future_field":1,"item_id":"m","output_index":0,"sequence_number":3,"type":"response.output_text.delta"}`)
	e, err := UnmarshalStreamEvent(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	d, ok := e.(*OutputTextDeltaEvent)
	if !ok {
		t.Fatalf("got %T", e)
	}
	if string(d.Extra["future_field"]) != "1" {
		t.Fatalf("extras: %v", d.Extra)
	}
	out, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("drift\n in: %s\nout: %s", in, out)
	}
}

func TestStreamEvents_MixedSlice(t *testing.T) {
	in := []byte(`[` +
		`{"response":{"id":"r1"},"sequence_number":0,"type":"response.created"},` +
		`{"content_index":0,"delta":"H","item_id":"m","output_index":0,"sequence_number":1,"type":"response.output_text.delta"},` +
		`{"content_index":0,"delta":"i","item_id":"m","output_index":0,"sequence_number":2,"type":"response.output_text.delta"},` +
		`{"sequence_number":3,"type":"response.completed","response":{"id":"r1","status":"completed"}},` +
		`{"sequence_number":4,"type":"response.future"}` +
		`]`)
	events, err := UnmarshalStreamEvents(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("len = %d", len(events))
	}
	if _, ok := events[4].(*UnknownEvent); !ok {
		t.Fatalf("events[4] = %T", events[4])
	}
}

func TestStreamEvent_MissingType(t *testing.T) {
	_, err := UnmarshalStreamEvent([]byte(`{"delta":"x"}`))
	if err == nil {
		t.Fatalf("expected error for missing type")
	}
}

func TestStreamEvents_NonArray(t *testing.T) {
	_, err := UnmarshalStreamEvents([]byte(`{"not":"array"}`))
	if err == nil {
		t.Fatalf("expected error on non-array input")
	}
}

func TestResponses_AllExportedFieldsHaveJSONTag(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(ResponsesRequest{}),
		reflect.TypeOf(ResponsesResponse{}),
		reflect.TypeOf(Tool{}),
		reflect.TypeOf(ReasoningOptions{}),
		reflect.TypeOf(ReasoningOutput{}),
		reflect.TypeOf(IncompleteDetails{}),
		reflect.TypeOf(Usage{}),
		reflect.TypeOf(InputTokensDetails{}),
		reflect.TypeOf(OutputTokensDetails{}),
		reflect.TypeOf(OutputItem{}),
		reflect.TypeOf(ResponseCreatedEvent{}),
		reflect.TypeOf(ResponseInProgressEvent{}),
		reflect.TypeOf(OutputItemAddedEvent{}),
		reflect.TypeOf(OutputItemDoneEvent{}),
		reflect.TypeOf(ContentPartAddedEvent{}),
		reflect.TypeOf(ContentPartDoneEvent{}),
		reflect.TypeOf(OutputTextDeltaEvent{}),
		reflect.TypeOf(OutputTextDoneEvent{}),
		reflect.TypeOf(ResponseCompletedEvent{}),
		reflect.TypeOf(ResponseFailedEvent{}),
		reflect.TypeOf(UnknownEvent{}),
	}
	for _, rt := range types {
		t.Run(rt.Name(), func(t *testing.T) {
			for i := 0; i < rt.NumField(); i++ {
				sf := rt.Field(i)
				if sf.Anonymous || !sf.IsExported() {
					continue
				}
				if _, ok := sf.Tag.Lookup("json"); !ok {
					t.Errorf("%s.%s missing json tag", rt.Name(), sf.Name)
				}
			}
		})
	}
}

func TestResponses_RegistryHasFallback(t *testing.T) {
	if streamEventRegistry.Fallback == nil {
		t.Fatalf("streamEventRegistry must have a fallback")
	}
	want := map[string]bool{
		"response.created":            false,
		"response.in_progress":        false,
		"response.output_item.added":  false,
		"response.output_item.done":   false,
		"response.content_part.added": false,
		"response.content_part.done":  false,
		"response.output_text.delta":  false,
		"response.output_text.done":   false,
		"response.completed":          false,
		"response.failed":             false,
	}
	for k := range streamEventRegistry.Factories {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected event %q", k)
		}
		want[k] = true
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("missing event %q", k)
		}
	}
}

func roundTripJSON(t *testing.T, in []byte, v any) {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}

func jsonValueEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("a: %v", err)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("b: %v", err)
	}
	return reflect.DeepEqual(av, bv)
}
