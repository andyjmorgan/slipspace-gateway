package responses

import (
	"encoding/json"
	"testing"
)

func FuzzUnmarshalStreamEvent(f *testing.F) {
	seeds := []string{
		`{"type":"response.created","sequence_number":0,"response":{}}`,
		`{"type":"response.output_text.delta","item_id":"m","output_index":0,"content_index":0,"delta":"hi","sequence_number":1}`,
		`{"type":"response.completed","sequence_number":9,"response":{"id":"r1"}}`,
		`{"type":"response.output_item.done","output_index":0,"sequence_number":4,"item":{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"gAAAAAB=="}}`,
		`{"type":"response.failed","error":{"message":"boom"}}`,
		`{"type":"response.unknown_future","extra":42}`,
		`{}`,
		`null`,
		`[]`,
		`"string"`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		e, err := UnmarshalStreamEvent([]byte(in))
		if err != nil {
			return
		}
		if _, err := json.Marshal(e); err != nil {
			t.Fatalf("marshal after parse: %v\nin: %s", err, in)
		}
	})
}

func FuzzUnmarshalTool(f *testing.F) {
	seeds := []string{
		`{"type":"function","name":"exec_command","strict":false,"parameters":{"type":"object"}}`,
		`{"type":"custom","name":"apply_patch","format":{"type":"grammar","syntax":"lark","definition":"start: x"}}`,
		`{"type":"tool_search","execution":"client","parameters":{"type":"object"}}`,
		`{"type":"web_search","external_web_access":false,"search_content_types":["text","image"]}`,
		`{"type":"image_generation","output_format":"png"}`,
		`{"type":"future_tool","knob":1}`,
		`{}`,
		`null`,
		`[]`,
		`"string"`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		v, err := UnmarshalTool([]byte(in))
		if err != nil {
			return
		}
		if _, err := json.Marshal(v); err != nil {
			t.Fatalf("marshal after parse: %v\nin: %s", err, in)
		}
	})
}

func FuzzResponsesRequest(f *testing.F) {
	seeds := []string{
		`{"model":"m","input":"hi"}`,
		`{"model":"m","input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`,
		`{"model":"m","input":"hi","reasoning":{"effort":"low"},"max_output_tokens":10}`,
		`{"model":"m","input":"hi","parallel_tool_calls":true,"top_logprobs":5,"max_tool_calls":3,"service_tier":"flex","prompt_cache_key":"k","prompt_cache_retention":"24h","safety_identifier":"s","include":["reasoning.encrypted_content"],"text":{"format":{"type":"text"}}}`,
		`{"model":"m","input":"hi","tools":[{"type":"function","name":"f"},{"type":"custom","name":"apply_patch","format":{"type":"grammar"}},{"type":"web_search"},{"type":"future_tool","k":1}]}`,
		`{}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		var req ResponsesRequest
		if err := json.Unmarshal([]byte(in), &req); err != nil {
			return
		}
		if _, err := json.Marshal(req); err != nil {
			t.Fatalf("marshal after parse: %v\nin: %s", err, in)
		}
	})
}

func FuzzResponsesResponse(f *testing.F) {
	seeds := []string{
		`{"id":"r","object":"response","created_at":1,"model":"m","status":"completed","output":[]}`,
		`{"id":"r","object":"response","created_at":1,"model":"m","status":"incomplete","incomplete_details":{"reason":"x"}}`,
		`{"id":"r","object":"response","created_at":1,"model":"m","status":"completed","billing":{"payer":"developer"},"frequency_penalty":0,"presence_penalty":0,"moderation":null,"reasoning":{"context":null,"effort":"low"}}`,
		`{"id":"r","object":"response","created_at":1,"model":"m","status":"completed","output":[{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":null},{"type":"message","id":"msg_1","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"hi"}]}],"metadata":{"k":"v"}}`,
		`{"id":"r","object":"response","created_at":1,"model":"m","status":"completed","output":[],"temperature":1,"top_p":1,"top_logprobs":0,"max_output_tokens":2048,"max_tool_calls":null,"store":true,"background":false,"completed_at":2,"truncation":"disabled","service_tier":"default","prompt_cache_retention":"in_memory","prompt_cache_key":null,"safety_identifier":null,"user":null,"instructions":null,"tools":[],"tool_choice":"auto","text":{"format":{"type":"text"}}}`,
		`{}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		var resp ResponsesResponse
		if err := json.Unmarshal([]byte(in), &resp); err != nil {
			return
		}
		if _, err := json.Marshal(resp); err != nil {
			t.Fatalf("marshal after parse: %v\nin: %s", err, in)
		}
	})
}
