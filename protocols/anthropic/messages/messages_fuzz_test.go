package messages

import (
	"encoding/json"
	"testing"
)

func FuzzMessagesRequestUnmarshal(f *testing.F) {
	seeds := [][]byte{
		[]byte(`{"max_tokens":1,"messages":[{"content":"hi","role":"user"}],"model":"m"}`),
		[]byte(`{"max_tokens":1,"messages":[{"content":[{"type":"text","text":"hi"}],"role":"user"}],"model":"m"}`),
		[]byte(`{"max_tokens":1,"messages":[{"content":[{"type":"future","x":1}],"role":"user"}],"model":"m","metadata":{"user_id":"u","extra":1}}`),
		[]byte(`{"future":"keep","max_tokens":1,"messages":[{"content":"hi","role":"user"}],"model":"m","system":[{"text":"s","type":"text"}]}`),
		[]byte(`{"max_tokens":1,"messages":[{"content":"hi","role":"user"}],"model":"m","output_config":{"effort":"xhigh","format":{"schema":{"type":"object"},"type":"json_schema"}}}`),
		[]byte(`{}`),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var req MessagesRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return
		}
		out, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("marshal after unmarshal: %v\ninput: %s", err, data)
		}
		var second MessagesRequest
		if err := json.Unmarshal(out, &second); err != nil {
			t.Fatalf("second unmarshal: %v\nremarshalled: %s\noriginal: %s", err, out, data)
		}
		again, err := json.Marshal(second)
		if err != nil {
			t.Fatalf("second marshal: %v", err)
		}
		if string(out) != string(again) {
			t.Fatalf("not idempotent\nfirst:  %s\nsecond: %s", out, again)
		}
	})
}

func FuzzMessagesResponseUnmarshal(f *testing.F) {
	seeds := [][]byte{
		[]byte(`{"id":"x","model":"m","role":"assistant","type":"message","content":[],"usage":{"input_tokens":0,"output_tokens":0}}`),
		[]byte(`{"id":"x","model":"m","role":"assistant","type":"message","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":1,"output_tokens":1}}`),
		[]byte(`{"id":"x","model":"m","role":"assistant","type":"message","content":[{"type":"future","p":1}],"usage":{"input_tokens":1,"output_tokens":1}}`),
		[]byte(`{"id":"x","model":"m","role":"assistant","type":"message","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":1,"output_tokens":1,"cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":0},"output_tokens_details":{"thinking_tokens":0},"inference_geo":"not_available"}}`),
		[]byte(`{"id":"x","model":"m","role":"assistant","type":"message","content":[{"type":"tool_use","id":"t1","name":"f","input":{},"caller":{"type":"direct"}}],"stop_reason":"tool_use","stop_details":null,"context_management":{"applied_edits":[]},"usage":{"input_tokens":1,"output_tokens":1}}`),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var resp MessagesResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			return
		}
		out, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal: %v\ninput: %s", err, data)
		}
		var second MessagesResponse
		if err := json.Unmarshal(out, &second); err != nil {
			t.Fatalf("second unmarshal: %v\nremarshalled: %s", err, out)
		}
		again, err := json.Marshal(second)
		if err != nil {
			t.Fatalf("second marshal: %v", err)
		}
		if string(out) != string(again) {
			t.Fatalf("not idempotent\nfirst:  %s\nsecond: %s", out, again)
		}
	})
}
