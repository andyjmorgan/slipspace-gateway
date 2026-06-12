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
		[]byte(`{"max_tokens":1,"messages":[{"content":"hi","role":"user"}],"model":"m","tools":[{"max_uses":3,"name":"web_search","type":"web_search_20250305"}]}`),
		[]byte(`{"context_management":{"edits":[{"keep":{"type":"tool_uses","value":3},"trigger":{"type":"input_tokens","value":30000},"type":"clear_tool_uses_20250919"},{"keep":"all","type":"clear_thinking_20251015"},{"instructions":"x","pause_after_compaction":true,"type":"compact_20260112"},{"type":"future_99990101","novel":1}]},"max_tokens":1,"messages":[{"content":"hi","role":"user"}],"model":"m"}`),
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

func FuzzContextManagementConfigUnmarshal(f *testing.F) {
	seeds := [][]byte{
		[]byte(`{"edits":[]}`),
		[]byte(`{"edits":[{"type":"clear_tool_uses_20250919","exclude_tools":["a"],"clear_tool_inputs":true}]}`),
		[]byte(`{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`),
		[]byte(`{"edits":[{"type":"clear_thinking_20251015","keep":{"type":"thinking_turns","value":2}}]}`),
		[]byte(`{"edits":[{"type":"compact_20260112","pause_after_compaction":false,"trigger":{"type":"input_tokens","value":150000}}]}`),
		[]byte(`{"edits":[{"type":"future_19990101","novel":[1,2]}],"future_top":1}`),
		[]byte(`{}`),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var cfg ContextManagementConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return
		}
		out, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("marshal: %v\ninput: %s", err, data)
		}
		var second ContextManagementConfig
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

func FuzzErrorResponseUnmarshal(f *testing.F) {
	seeds := [][]byte{
		[]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"bad"},"request_id":"req_1"}`),
		[]byte(`{"type":"error","error":{"type":"overloaded_error","message":"x","detail":"d"},"future":1}`),
		[]byte(`{"type":"error","error":{"type":"rate_limit_error","message":""}}`),
		[]byte(`{}`),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var er ErrorResponse
		if err := json.Unmarshal(data, &er); err != nil {
			return
		}
		out, err := json.Marshal(er)
		if err != nil {
			t.Fatalf("marshal: %v\ninput: %s", err, data)
		}
		var second ErrorResponse
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

func FuzzMessagesResponseUnmarshal(f *testing.F) {
	seeds := [][]byte{
		[]byte(`{"id":"x","model":"m","role":"assistant","type":"message","content":[],"usage":{"input_tokens":0,"output_tokens":0}}`),
		[]byte(`{"id":"x","model":"m","role":"assistant","type":"message","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":1,"output_tokens":1}}`),
		[]byte(`{"id":"x","model":"m","role":"assistant","type":"message","content":[{"type":"future","p":1}],"usage":{"input_tokens":1,"output_tokens":1}}`),
		[]byte(`{"id":"x","model":"m","role":"assistant","type":"message","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":1,"output_tokens":1,"cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":0},"output_tokens_details":{"thinking_tokens":0},"inference_geo":"not_available"}}`),
		[]byte(`{"id":"x","model":"m","role":"assistant","type":"message","content":[{"type":"tool_use","id":"t1","name":"f","input":{},"caller":{"type":"direct"}}],"stop_reason":"tool_use","stop_details":null,"context_management":{"applied_edits":[]},"usage":{"input_tokens":1,"output_tokens":1}}`),
		[]byte(`{"id":"x","model":"m","role":"assistant","type":"message","content":[{"type":"text","text":"hi","citations":[{"type":"web_search_result_location","cited_text":"c","url":"https://e.com","title":"T","encrypted_index":"z"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`),
		[]byte(`{"id":"x","model":"m","role":"assistant","type":"message","content":[{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"q"}},{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","caller":{"type":"direct"},"content":[{"type":"web_search_result","title":"t","url":"https://e.com","encrypted_content":"z"}]}],"usage":{"input_tokens":1,"output_tokens":1,"server_tool_use":{"web_search_requests":1,"web_fetch_requests":0}}}`),
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
