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
