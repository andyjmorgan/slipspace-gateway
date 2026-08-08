package chat

import (
	"encoding/json"
	"errors"
	"testing"
)

// TestChat_ThinkOptionRoundTrips pins the polymorphic Ollama "think" field:
// each wire shape (bool, level string, null) must round-trip byte-equivalent.
func TestChat_ThinkOptionRoundTrips(t *testing.T) {
	cases := []struct {
		name    string
		json    string
		isBool  bool
		isLevel bool
	}{
		{name: "bool true", json: `true`, isBool: true},
		{name: "bool false", json: `false`, isBool: true},
		{name: "level high", json: `"high"`, isLevel: true},
		{name: "level medium", json: `"medium"`, isLevel: true},
		{name: "level low", json: `"low"`, isLevel: true},
		{name: "level max", json: `"max"`, isLevel: true},
		{name: "null", json: `null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var th ThinkOption
			if err := json.Unmarshal([]byte(tc.json), &th); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if th.IsBool() != tc.isBool {
				t.Fatalf("IsBool = %v, want %v", th.IsBool(), tc.isBool)
			}
			if th.IsLevel() != tc.isLevel {
				t.Fatalf("IsLevel = %v, want %v", th.IsLevel(), tc.isLevel)
			}
			out, err := json.Marshal(th)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(out) != tc.json {
				t.Fatalf("drift: in %s out %s", tc.json, out)
			}
		})
	}
}

func TestChat_ThinkOption_Projections(t *testing.T) {
	b := NewThinkBool(true)
	v, err := b.Bool()
	if err != nil || !v {
		t.Fatalf("Bool: %v %v", v, err)
	}
	if _, err := b.Level(); !errors.Is(err, ErrThinkNotLevel) {
		t.Fatalf("Level on bool: %v", err)
	}

	l := NewThinkLevel("high")
	s, err := l.Level()
	if err != nil || s != "high" {
		t.Fatalf("Level: %q %v", s, err)
	}
	if _, err := l.Bool(); !errors.Is(err, ErrThinkNotBool) {
		t.Fatalf("Bool on level: %v", err)
	}

	var empty ThinkOption
	if _, err := empty.Bool(); !errors.Is(err, ErrThinkNotBool) {
		t.Fatalf("Bool on empty: %v", err)
	}
	if _, err := empty.Level(); !errors.Is(err, ErrThinkNotLevel) {
		t.Fatalf("Level on empty: %v", err)
	}
	if got := l.Raw(); string(got) != `"high"` {
		t.Fatalf("Raw: %s", got)
	}
}

// TestChat_RequestOllamaFieldsRoundTrip pins the Ollama/vLLM OpenAI-compat
// request extensions think, reasoning, and chat_template_kwargs on
// ChatCompletionRequest, including unknown fields inside the nested
// reasoning object.
func TestChat_RequestOllamaFieldsRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{
			name: "think bool",
			json: `{"messages":[{"content":"hi","role":"user"}],"model":"gemma4","think":false}`,
		},
		{
			name: "think level",
			json: `{"messages":[{"content":"hi","role":"user"}],"model":"qwen3","think":"high"}`,
		},
		{
			name: "chat_template_kwargs",
			json: `{"chat_template_kwargs":{"enable_thinking":false},"messages":[{"content":"hi","role":"user"}],"model":"gemma4"}`,
		},
		{
			name: "nested reasoning effort",
			json: `{"messages":[{"content":"hi","role":"user"}],"model":"gemma4","reasoning":{"effort":"low"}}`,
		},
		{
			name: "reasoning with unknown sibling",
			json: `{"messages":[{"content":"hi","role":"user"}],"model":"gemma4","reasoning":{"effort":"high","summary":"auto"}}`,
		},
		{
			name: "all together next to flat reasoning_effort",
			json: `{"chat_template_kwargs":{"enable_thinking":true},"messages":[{"content":"hi","role":"user"}],"model":"qwen3","reasoning":{"effort":"medium"},"reasoning_effort":"medium","think":true}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req ChatCompletionRequest
			if err := json.Unmarshal([]byte(tc.json), &req); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			out, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !jsonValueEqual(t, []byte(tc.json), out) {
				t.Fatalf("drift\n in: %s\nout: %s", tc.json, out)
			}
		})
	}
}

func TestChat_RequestOllamaFields_Typed(t *testing.T) {
	in := []byte(`{"chat_template_kwargs":{"enable_thinking":false},"messages":[],"model":"m","reasoning":{"effort":"high"},"think":"low"}`)
	var req ChatCompletionRequest
	if err := json.Unmarshal(in, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Think == nil {
		t.Fatalf("think not bound")
	}
	lvl, err := req.Think.Level()
	if err != nil || lvl != "low" {
		t.Fatalf("think level: %q %v", lvl, err)
	}
	if req.Reasoning == nil || req.Reasoning.Effort != "high" {
		t.Fatalf("reasoning: %+v", req.Reasoning)
	}
	if string(req.ChatTemplateKwargs) != `{"enable_thinking":false}` {
		t.Fatalf("chat_template_kwargs raw: %s", req.ChatTemplateKwargs)
	}
	// The typed fields must not leak into the untyped Extra bag.
	for _, k := range []string{"think", "reasoning", "chat_template_kwargs"} {
		if _, ok := req.Extra[k]; ok {
			t.Fatalf("%s landed in Extra", k)
		}
	}
}

// TestChat_ReasoningOptions_UnknownFields verifies unknown keys inside the
// nested reasoning object land in its DynamicProperties.Extra and round-trip.
func TestChat_ReasoningOptions_UnknownFields(t *testing.T) {
	in := []byte(`{"effort":"high","summary":"auto"}`)
	var ro ReasoningOptions
	if err := json.Unmarshal(in, &ro); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ro.Effort != "high" {
		t.Fatalf("effort: %q", ro.Effort)
	}
	if string(ro.Extra["summary"]) != `"auto"` {
		t.Fatalf("extra: %v", ro.Extra)
	}
	out, err := json.Marshal(ro)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("drift\n in: %s\nout: %s", in, out)
	}
}
