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

func FuzzResponsesRequest(f *testing.F) {
	seeds := []string{
		`{"model":"m","input":"hi"}`,
		`{"model":"m","input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`,
		`{"model":"m","input":"hi","reasoning":{"effort":"low"},"max_output_tokens":10}`,
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
