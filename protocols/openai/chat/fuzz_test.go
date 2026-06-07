package chat

import (
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
