package chat

import (
	"bytes"
	"encoding/json"
	"testing"
)

func FuzzUnmarshalRequestMessage(f *testing.F) {
	seeds := []string{
		`{"role":"system","content":"hi"}`,
		`{"role":"developer","content":"hi"}`,
		`{"role":"user","content":"hi"}`,
		`{"role":"user","content":[{"type":"text","text":"hi"}]}`,
		`{"role":"assistant","content":"hi","tool_calls":[{"id":"x","type":"function","function":{"name":"f","arguments":"{}"}}]}`,
		`{"role":"tool","tool_call_id":"x","content":"42"}`,
		`{"role":"function","name":"do","content":"42"}`,
		`{"role":"future","data":"keep"}`,
		`{}`,
		`null`,
		`"string"`,
		`[]`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		m, err := UnmarshalRequestMessage([]byte(in))
		if err != nil {
			return
		}
		if _, err := json.Marshal(m); err != nil {
			t.Fatalf("marshal after parse: %v\nin: %s", err, in)
		}
	})
}

func FuzzUnmarshalContentPart(f *testing.F) {
	seeds := []string{
		`{"type":"text","text":"hi"}`,
		`{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}`,
		`{"type":"input_audio","input_audio":{"data":"AAA","format":"wav"}}`,
		`{"type":"audio","audio":{"id":"a_1","transcript":"hi"}}`,
		`{"type":"refusal","refusal":"no"}`,
		`{"type":"file","file":{"file_id":"f_1"}}`,
		`{"type":"future","extra":1}`,
		`{}`,
		`null`,
		`[]`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		p, err := UnmarshalContentPart([]byte(in))
		if err != nil {
			return
		}
		if _, err := json.Marshal(p); err != nil {
			t.Fatalf("marshal after parse: %v\nin: %s", err, in)
		}
	})
}

func FuzzChatCompletionRequest(f *testing.F) {
	seeds := []string{
		`{"model":"m","messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],"temperature":0.5,"max_tokens":10}`,
		`{"model":"m","messages":[{"role":"future","x":1}]}`,
		`{"model":"m","messages":[],"new_field":1}`,
		// Ollama OpenAI-compat: polymorphic think (bool | level string).
		`{"model":"gemma4","messages":[],"think":true}`,
		`{"model":"gemma4","messages":[],"think":false}`,
		`{"model":"qwen3","messages":[],"think":"high"}`,
		`{"model":"qwen3","messages":[],"think":"max"}`,
		`{"model":"qwen3","messages":[],"think":null}`,
		// vLLM/llama.cpp OpenAI-compat: open-ended chat_template_kwargs.
		`{"model":"gemma4","messages":[],"chat_template_kwargs":{"enable_thinking":false}}`,
		// Ollama OpenAI-compat: nested reasoning.effort next to the flat field.
		`{"model":"gemma4","messages":[],"reasoning":{"effort":"low"},"reasoning_effort":"low"}`,
		`{}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		var req ChatCompletionRequest
		if err := json.Unmarshal([]byte(in), &req); err != nil {
			return
		}
		if _, err := json.Marshal(req); err != nil {
			t.Fatalf("marshal after parse: %v\nin: %s", err, in)
		}
	})
}

func FuzzChatCompletionResponse(f *testing.F) {
	seeds := []string{
		`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[]}`,
		`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		// gpt-oss / Ollama qwen3 OpenAI-compat: choices[].message.reasoning.
		`{"id":"x","object":"chat.completion","created":1,"model":"gpt-oss","choices":[{"index":0,"message":{"role":"assistant","content":"4","reasoning":"2+2 is 4"},"finish_reason":"stop"}]}`,
		// older vLLM spelling "reasoning_content" rides along via DynamicProperties.
		`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi","reasoning_content":"legacy"},"finish_reason":"stop"}]}`,
		// web-search annotations: typed url_citation plus an unknown annotation type.
		`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"see source","annotations":[{"type":"url_citation","url_citation":{"start_index":0,"end_index":3,"title":"t","url":"https://x.test"}},{"type":"file_citation","file_citation":{"file_id":"f_1"}}]},"finish_reason":"stop"}]}`,
		`{}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		var resp ChatCompletionResponse
		if err := json.Unmarshal([]byte(in), &resp); err != nil {
			return
		}
		if _, err := json.Marshal(resp); err != nil {
			t.Fatalf("marshal after parse: %v\nin: %s", err, in)
		}
	})
}

// FuzzThinkOption drives the raw-preserving polymorphic think field: whatever
// parses must marshal back equivalent to the input (the whole point of the
// type), modulo the compaction + HTML escaping json.Marshal applies to
// Marshaler output.
func FuzzThinkOption(f *testing.F) {
	seeds := []string{
		`true`,
		`false`,
		`"high"`,
		`"medium"`,
		`"low"`,
		`"max"`,
		`"future-level"`,
		`null`,
		`0`,
		`{"unexpected":"object"}`,
		`[]`,
		`""`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		var th ThinkOption
		if err := json.Unmarshal([]byte(in), &th); err != nil {
			return
		}
		out, err := json.Marshal(th)
		if err != nil {
			t.Fatalf("marshal after parse: %v\nin: %s", err, in)
		}
		// json.Marshal compacts and HTML-escapes Marshaler output, so the
		// exact expectation is HTMLEscape(Compact(in)) — anything else is a
		// genuine round-trip drift.
		var compacted bytes.Buffer
		if err := json.Compact(&compacted, []byte(in)); err != nil {
			t.Fatalf("compact input: %v\nin: %s", err, in)
		}
		var want bytes.Buffer
		json.HTMLEscape(&want, compacted.Bytes())
		if !bytes.Equal(out, want.Bytes()) {
			t.Fatalf("raw drift: in %s want %s out %s", in, want.Bytes(), out)
		}
		// Projections must never panic; errors are fine.
		_, _ = th.Bool()
		_, _ = th.Level()
	})
}

// FuzzErrorResponse drives the OpenAI error envelope, including compat-server
// variants (numeric code, missing param) and unknown fields.
func FuzzErrorResponse(f *testing.F) {
	seeds := []string{
		`{"error":{"message":"m","type":"invalid_request_error","param":null,"code":null}}`,
		`{"error":{"message":"m","type":"invalid_request_error","param":"model","code":"model_not_found"}}`,
		`{"error":{"message":"m","type":"api_error","code":500}}`,
		`{"error":{"message":"m","type":"rate_limit_error","param":null,"code":null,"extra":1},"request_id":"req_1"}`,
		`{"error":{}}`,
		`{}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		var er ErrorResponse
		if err := json.Unmarshal([]byte(in), &er); err != nil {
			return
		}
		if _, err := json.Marshal(er); err != nil {
			t.Fatalf("marshal after parse: %v\nin: %s", err, in)
		}
	})
}
