package chat

import (
	"encoding/json"
	"testing"
)

func TestChat_ResponseFormatRoundTrips(t *testing.T) {
	cases := []string{
		`{"type":"text"}`,
		`{"type":"json_object"}`,
		`{"json_schema":{"name":"out","schema":{"type":"object"},"strict":true},"type":"json_schema"}`,
	}
	for _, in := range cases {
		var rf ResponseFormat
		if err := json.Unmarshal([]byte(in), &rf); err != nil {
			t.Fatalf("unmarshal %s: %v", in, err)
		}
		out, err := json.Marshal(rf)
		if err != nil {
			t.Fatalf("marshal %s: %v", in, err)
		}
		if !jsonValueEqual(t, []byte(in), out) {
			t.Fatalf("drift\n in: %s\nout: %s", in, out)
		}
	}
}

func TestChat_StreamOptionsRoundTrip(t *testing.T) {
	in := []byte(`{"future":"keep","include_usage":true}`)
	var so StreamOptions
	if err := json.Unmarshal(in, &so); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !so.IncludeUsage {
		t.Fatalf("include_usage = false")
	}
	out, err := json.Marshal(so)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("drift")
	}
}

func TestChat_AudioOptionsAndMessageRoundTrip(t *testing.T) {
	opts := []byte(`{"format":"mp3","voice":"alloy"}`)
	var ao AudioOptions
	if err := json.Unmarshal(opts, &ao); err != nil {
		t.Fatalf("opts unmarshal: %v", err)
	}
	out, err := json.Marshal(ao)
	if err != nil || !jsonValueEqual(t, opts, out) {
		t.Fatalf("opts drift: %v %s", err, out)
	}

	expires := int64(1700000000)
	am := AudioMessage{ID: "a1", Transcript: "hi", ExpiresAt: &expires}
	raw, err := json.Marshal(am)
	if err != nil {
		t.Fatalf("am marshal: %v", err)
	}
	var back AudioMessage
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("am unmarshal: %v", err)
	}
	if back.ID != "a1" || back.Transcript != "hi" || back.ExpiresAt == nil || *back.ExpiresAt != expires {
		t.Fatalf("audio message round-trip: %+v", back)
	}
}

func TestChat_UsageDetailsRoundTrip(t *testing.T) {
	in := []byte(`{` +
		`"completion_tokens":4,` +
		`"completion_tokens_details":{"accepted_prediction_tokens":1,"reasoning_tokens":2},` +
		`"prompt_tokens":7,` +
		`"prompt_tokens_details":{"cached_tokens":3},` +
		`"total_tokens":11` +
		`}`)
	var u Usage
	if err := json.Unmarshal(in, &u); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if u.PromptTokensDetails == nil || u.PromptTokensDetails.CachedTokens == nil || *u.PromptTokensDetails.CachedTokens != 3 {
		t.Fatalf("prompt details: %+v", u.PromptTokensDetails)
	}
	if u.CompletionTokensDetails == nil || u.CompletionTokensDetails.ReasoningTokens == nil || *u.CompletionTokensDetails.ReasoningTokens != 2 {
		t.Fatalf("completion details: %+v", u.CompletionTokensDetails)
	}
	out, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("drift\n in: %s\nout: %s", in, out)
	}
}

// TestChat_UsageZeroSubCounterRoundTrips guards the rollup-fidelity fix: a
// provider-reported reasoning_tokens / cached_tokens of 0 must survive a
// decode→encode cycle (the streaming accumulator re-marshals Usage). Pre-fix
// the plain-int + omitempty fields collapsed a real 0 to absent.
func TestChat_UsageZeroSubCounterRoundTrips(t *testing.T) {
	in := []byte(`{` +
		`"completion_tokens":4,` +
		`"completion_tokens_details":{"reasoning_tokens":0},` +
		`"prompt_tokens":7,` +
		`"prompt_tokens_details":{"cached_tokens":0},` +
		`"total_tokens":11` +
		`}`)
	var u Usage
	if err := json.Unmarshal(in, &u); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if u.PromptTokensDetails == nil || u.PromptTokensDetails.CachedTokens == nil || *u.PromptTokensDetails.CachedTokens != 0 {
		t.Fatalf("cached_tokens 0 not preserved: %+v", u.PromptTokensDetails)
	}
	if u.CompletionTokensDetails == nil || u.CompletionTokensDetails.ReasoningTokens == nil || *u.CompletionTokensDetails.ReasoningTokens != 0 {
		t.Fatalf("reasoning_tokens 0 not preserved: %+v", u.CompletionTokensDetails)
	}
	out, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("zero sub-counter dropped on re-marshal: got %s", out)
	}
}

func TestChat_ToolFunctionRoundTrip(t *testing.T) {
	in := []byte(`{"description":"adds","name":"add","parameters":{"type":"object"},"strict":true}`)
	var tf ToolFunction
	if err := json.Unmarshal(in, &tf); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if tf.Name != "add" || tf.Strict == nil || !*tf.Strict {
		t.Fatalf("tool function: %+v", tf)
	}
	out, err := json.Marshal(tf)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("drift")
	}
}

func TestChat_NewStringContent_RoundTrip(t *testing.T) {
	c := NewStringContent("hi")
	out, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != `"hi"` {
		t.Fatalf("got %s", out)
	}
}

func TestChat_NewPartsContent_Error(t *testing.T) {
	parts := []ContentPart{&UnknownContentPart{Type: "x"}}
	c, err := NewPartsContent(parts)
	if err != nil {
		t.Fatalf("new parts: %v", err)
	}
	if !c.IsArray() {
		t.Fatalf("expected array")
	}
}

func TestChat_AsString_DecodeError(t *testing.T) {
	c := MessageContent{raw: []byte(`"unterminated`)}
	if _, err := c.AsString(); err == nil {
		t.Fatalf("expected decode error")
	}
}

func TestChat_IsNull_NonNullArray(t *testing.T) {
	c := NewStringContent("x")
	if c.IsNull() {
		t.Fatalf("expected non-null")
	}
}

func TestChat_SealedInterfaceMarkers(t *testing.T) {
	SystemMessage{}.isRequestMessage()
	DeveloperMessage{}.isRequestMessage()
	UserMessage{}.isRequestMessage()
	AssistantMessage{}.isRequestMessage()
	ToolMessage{}.isRequestMessage()
	FunctionMessage{}.isRequestMessage()
	UnknownRequestMessage{}.isRequestMessage()

	TextContentPart{}.isContentPart()
	ImageURLContentPart{}.isContentPart()
	InputAudioContentPart{}.isContentPart()
	OutputAudioContentPart{}.isContentPart()
	RefusalContentPart{}.isContentPart()
	FileContentPart{}.isContentPart()
	UnknownContentPart{}.isContentPart()

	UrlCitationAnnotation{}.isAnnotation()
	UnknownAnnotation{}.isAnnotation()
}

func TestChat_RequestMessages_UnmarshalJSONError(t *testing.T) {
	var rm RequestMessages
	if err := rm.UnmarshalJSON([]byte(`{"not":"array"}`)); err == nil {
		t.Fatalf("expected error")
	}
}

// TestChat_ResponseMessageReasoningRoundTrip pins the OpenAI-compat
// choices[].message.reasoning field (gpt-oss / Ollama qwen3 / vLLM). The typed
// "reasoning" key surfaces on the struct; the older vLLM "reasoning_content"
// spelling rides along untyped via DynamicProperties. Both must round-trip
// byte-equivalent (modulo key order).
func TestChat_ResponseMessageReasoningRoundTrip(t *testing.T) {
	in := []byte(`{"content":"4","reasoning":"2+2 is 4","role":"assistant"}`)
	var m ResponseMessage
	if err := json.Unmarshal(in, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Reasoning == nil || *m.Reasoning != "2+2 is 4" {
		t.Fatalf("reasoning not typed: %+v", m.Reasoning)
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("drift\n in: %s\nout: %s", in, out)
	}

	// Legacy "reasoning_content" spelling is preserved via DynamicProperties
	// without being claimed by the typed field.
	legacy := []byte(`{"content":"hi","reasoning_content":"legacy trace","role":"assistant"}`)
	var lm ResponseMessage
	if err := json.Unmarshal(legacy, &lm); err != nil {
		t.Fatalf("legacy unmarshal: %v", err)
	}
	if lm.Reasoning != nil {
		t.Fatalf("legacy spelling must not bind the typed field: %+v", lm.Reasoning)
	}
	lout, err := json.Marshal(lm)
	if err != nil {
		t.Fatalf("legacy marshal: %v", err)
	}
	if !jsonValueEqual(t, legacy, lout) {
		t.Fatalf("legacy drift\n in: %s\nout: %s", legacy, lout)
	}
}
