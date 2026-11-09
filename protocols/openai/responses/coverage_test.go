package responses

import (
	"encoding/json"
	"testing"
)

func TestResponses_UsageDetailsRoundTrip(t *testing.T) {
	in := []byte(`{` +
		`"input_tokens":10,` +
		`"input_tokens_details":{"cached_tokens":3},` +
		`"output_tokens":5,` +
		`"output_tokens_details":{"reasoning_tokens":2},` +
		`"total_tokens":15` +
		`}`)
	var u Usage
	if err := json.Unmarshal(in, &u); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if u.InputTokensDetails == nil || u.InputTokensDetails.CachedTokens == nil || *u.InputTokensDetails.CachedTokens != 3 {
		t.Fatalf("input details: %+v", u.InputTokensDetails)
	}
	if u.OutputTokensDetails == nil || u.OutputTokensDetails.ReasoningTokens == nil || *u.OutputTokensDetails.ReasoningTokens != 2 {
		t.Fatalf("output details: %+v", u.OutputTokensDetails)
	}
	out, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("drift")
	}
}

// TestResponses_UsageZeroSubCounterRoundTrips guards the rollup-fidelity fix:
// a provider-reported reasoning_tokens / cached_tokens of 0 must survive a
// decode→encode cycle (the streaming accumulator re-marshals Usage). With the
// pre-fix plain-int + omitempty fields a real 0 collapsed to absent.
func TestResponses_UsageZeroSubCounterRoundTrips(t *testing.T) {
	in := []byte(`{` +
		`"input_tokens":10,` +
		`"input_tokens_details":{"cached_tokens":0},` +
		`"output_tokens":5,` +
		`"output_tokens_details":{"reasoning_tokens":0},` +
		`"total_tokens":15` +
		`}`)
	var u Usage
	if err := json.Unmarshal(in, &u); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if u.InputTokensDetails == nil || u.InputTokensDetails.CachedTokens == nil || *u.InputTokensDetails.CachedTokens != 0 {
		t.Fatalf("cached_tokens 0 not preserved: %+v", u.InputTokensDetails)
	}
	if u.OutputTokensDetails == nil || u.OutputTokensDetails.ReasoningTokens == nil || *u.OutputTokensDetails.ReasoningTokens != 0 {
		t.Fatalf("reasoning_tokens 0 not preserved: %+v", u.OutputTokensDetails)
	}
	out, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("zero sub-counter dropped on re-marshal: got %s", out)
	}
}

// TestResponses_InputTokensDetails_CacheWriteTokensRoundTrips pins
// usage.input_tokens_details.cache_write_tokens (GitHub #511), the surface
// OpenAI documents the counter on (ResponseUsage requires it alongside
// cached_tokens). The typed pointer must carry both a non-zero value and an
// explicit 0, and the not-yet-typed per-lifetime breakdown
// (cache_write_tokens_details) must keep riding along in Extra.
func TestResponses_InputTokensDetails_CacheWriteTokensRoundTrips(t *testing.T) {
	cases := []struct {
		name string
		json string
		want int
	}{
		{
			name: "prompt-caching guide example with lifetime breakdown",
			json: `{"input_tokens":12000,"input_tokens_details":{"cache_write_tokens":7000,"cache_write_tokens_details":{"ttl_12h_tokens":4000,"ttl_30m_tokens":3000},"cached_tokens":4000},"output_tokens":5,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":12005}`,
			want: 7000,
		},
		{
			name: "explicit zero survives",
			json: `{"input_tokens":37,"input_tokens_details":{"cache_write_tokens":0,"cached_tokens":0},"output_tokens":11,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":48}`,
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var u Usage
			if err := json.Unmarshal([]byte(tc.json), &u); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			d := u.InputTokensDetails
			if d == nil || d.CacheWriteTokens == nil || *d.CacheWriteTokens != tc.want {
				t.Fatalf("cache_write_tokens not typed: %+v", d)
			}
			if _, inExtra := d.Extra["cache_write_tokens"]; inExtra {
				t.Fatalf("cache_write_tokens landed in Extra instead of the typed field")
			}
			out, err := json.Marshal(u)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !jsonValueEqual(t, []byte(tc.json), out) {
				t.Fatalf("drift\n in: %s\nout: %s", tc.json, out)
			}
		})
	}

	t.Run("lifetime breakdown rides Extra", func(t *testing.T) {
		var u Usage
		if err := json.Unmarshal([]byte(cases[0].json), &u); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if _, ok := u.InputTokensDetails.Extra["cache_write_tokens_details"]; !ok {
			t.Fatalf("cache_write_tokens_details dropped: %v", u.InputTokensDetails.Extra)
		}
	})
}

func TestResponses_OutputItemFunctionCall(t *testing.T) {
	in := []byte(`{` +
		`"arguments":"{\"q\":\"x\"}",` +
		`"call_id":"call_1",` +
		`"id":"fc_1",` +
		`"name":"search",` +
		`"status":"completed",` +
		`"type":"function_call"` +
		`}`)
	var o OutputItem
	if err := json.Unmarshal(in, &o); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if o.Type != "function_call" || o.Name != "search" || o.CallID != "call_1" {
		t.Fatalf("output item: %+v", o)
	}
	out, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("drift")
	}
}

func TestResponses_OutputItemWithUnknownField(t *testing.T) {
	in := []byte(`{"future_field":"keep","id":"m_1","type":"reasoning"}`)
	var o OutputItem
	if err := json.Unmarshal(in, &o); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(o.Extra["future_field"]) != `"keep"` {
		t.Fatalf("extras: %v", o.Extra)
	}
	out, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("drift")
	}
}

func TestResponses_ReasoningOutputRoundTrip(t *testing.T) {
	in := []byte(`{"effort":"high","summary":[{"text":"considered","type":"summary_text"}]}`)
	var r ReasoningOutput
	if err := json.Unmarshal(in, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("drift")
	}
}

func TestResponses_SealedInterfaceMarkers(t *testing.T) {
	ResponseCreatedEvent{}.isResponsesStreamEvent()
	ResponseInProgressEvent{}.isResponsesStreamEvent()
	OutputItemAddedEvent{}.isResponsesStreamEvent()
	OutputItemDoneEvent{}.isResponsesStreamEvent()
	ContentPartAddedEvent{}.isResponsesStreamEvent()
	ContentPartDoneEvent{}.isResponsesStreamEvent()
	OutputTextDeltaEvent{}.isResponsesStreamEvent()
	OutputTextDoneEvent{}.isResponsesStreamEvent()
	ResponseCompletedEvent{}.isResponsesStreamEvent()
	ResponseFailedEvent{}.isResponsesStreamEvent()
	UnknownEvent{}.isResponsesStreamEvent()
}

// Server-side compaction request shape per
// https://developers.openai.com/api/docs/guides/compaction and the create
// reference (context_management: array of {type, compact_threshold}).
func TestResponsesRequest_ContextManagement(t *testing.T) {
	cases := []struct {
		name          string
		in            string
		wantThreshold *int
		wantExtraKey  string
	}{
		{
			name:          "compaction with threshold",
			in:            `{"context_management":[{"compact_threshold":200000,"type":"compaction"}],"input":"hi","model":"gpt-5.3-codex","store":false}`,
			wantThreshold: intPtr(200000),
		},
		{
			name: "threshold absent",
			in:   `{"context_management":[{"type":"compaction"}],"input":"hi","model":"m"}`,
		},
		{
			name:          "unknown item field rides Extra",
			in:            `{"context_management":[{"compact_threshold":1000,"future":"keep","type":"compaction"}],"input":"hi","model":"m"}`,
			wantThreshold: intPtr(1000),
			wantExtraKey:  "future",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r ResponsesRequest
			if err := json.Unmarshal([]byte(tc.in), &r); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(r.ContextManagement) != 1 || r.ContextManagement[0].Type != "compaction" {
				t.Fatalf("context_management = %+v", r.ContextManagement)
			}
			item := r.ContextManagement[0]
			switch {
			case tc.wantThreshold == nil && item.CompactThreshold != nil:
				t.Fatalf("compact_threshold = %d, want nil", *item.CompactThreshold)
			case tc.wantThreshold != nil && (item.CompactThreshold == nil || *item.CompactThreshold != *tc.wantThreshold):
				t.Fatalf("compact_threshold = %v, want %d", item.CompactThreshold, *tc.wantThreshold)
			}
			if tc.wantExtraKey != "" && string(item.Extra[tc.wantExtraKey]) != `"keep"` {
				t.Fatalf("item extras: %v", item.Extra)
			}
			if _, leaked := r.Extra["context_management"]; leaked {
				t.Fatalf("context_management leaked into request Extra")
			}
			out, err := json.Marshal(r)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !jsonValueEqual(t, []byte(tc.in), out) {
				t.Fatalf("drift\n in: %s\nout: %s", tc.in, out)
			}
		})
	}
}

// created_by ("The identifier of the actor that created the item") per
// https://developers.openai.com/api/reference/resources/responses/subresources/input_items/methods/list.md
func TestResponses_OutputItemCreatedBy(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{
			name: "function_call",
			in:   `{"arguments":"{}","call_id":"call_1","created_by":"assistant","id":"fc_1","name":"search","status":"completed","type":"function_call"}`,
		},
		{
			name: "compaction item",
			in:   `{"created_by":"system","encrypted_content":"gAAAAABpM0Yj-...=","id":"cmp_001","type":"compaction"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var o OutputItem
			if err := json.Unmarshal([]byte(tc.in), &o); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if o.CreatedBy == "" {
				t.Fatalf("created_by not typed: %+v", o)
			}
			if _, leaked := o.Extra["created_by"]; leaked {
				t.Fatalf("created_by leaked into Extra")
			}
			out, err := json.Marshal(o)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !jsonValueEqual(t, []byte(tc.in), out) {
				t.Fatalf("drift\n in: %s\nout: %s", tc.in, out)
			}
		})
	}
}

func intPtr(i int) *int { return &i }
