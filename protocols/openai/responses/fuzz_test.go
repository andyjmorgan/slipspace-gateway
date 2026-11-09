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
		`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":1,"delta":"{\"ci","sequence_number":5}`,
		`{"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":1,"arguments":"{\"city\":\"SF\"}","sequence_number":6}`,
		`{"type":"response.custom_tool_call_input.delta","item_id":"ctc_1","output_index":1,"delta":"const cmds = [\n","sequence_number":7}`,
		`{"type":"response.custom_tool_call_input.done","item_id":"ctc_1","output_index":1,"input":"const cmds = [];","sequence_number":8}`,
		`{"type":"response.incomplete","sequence_number":9,"response":{"id":"r1","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`,
		`{"type":"error","code":"ERR_SOMETHING","message":"Something went wrong","param":null,"sequence_number":1}`,
		`{"type":"error","code":null,"message":"m","param":"input"}`,
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
		// #511: input_tokens_details.cache_write_tokens (+ the untyped per-lifetime breakdown) and the explicit-zero form.
		`{"id":"r","object":"response","created_at":1,"model":"m","status":"completed","output":[],"usage":{"input_tokens":12000,"output_tokens":5,"total_tokens":12005,"input_tokens_details":{"cached_tokens":4000,"cache_write_tokens":7000,"cache_write_tokens_details":{"ttl_30m_tokens":3000,"ttl_12h_tokens":4000}},"output_tokens_details":{"reasoning_tokens":0}}}`,
		`{"id":"r","object":"response","created_at":1,"model":"m","status":"completed","output":[],"usage":{"input_tokens":37,"output_tokens":11,"total_tokens":48,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}`,
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
