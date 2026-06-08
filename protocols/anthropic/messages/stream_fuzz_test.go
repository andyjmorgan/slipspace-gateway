package messages

import (
	"encoding/json"
	"testing"
)

func FuzzUnmarshalStreamEvent(f *testing.F) {
	seeds := [][]byte{
		[]byte(`{"type":"message_start","message":{"id":"m","model":"x","role":"assistant","type":"message","content":[],"usage":{"input_tokens":0,"output_tokens":0}}}`),
		[]byte(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
		[]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`),
		[]byte(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"k\":"}}`),
		[]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}`),
		[]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}`),
		[]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"citations_delta","citation":{"type":"web_search_result_location","url":"https://e.com","cited_text":"x"}}}`),
		[]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"future_delta","extra":1}}`),
		[]byte(`{"type":"content_block_stop","index":0}`),
		[]byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":1,"output_tokens":2}}`),
		[]byte(`{"type":"message_stop"}`),
		[]byte(`{"type":"ping"}`),
		[]byte(`{"type":"error","error":{"type":"overloaded_error","message":"x"}}`),
		[]byte(`{"type":"error","error":{"type":"api_error","message":"x"},"request_id":"req_011Cabc"}`),
		[]byte(`{"type":"future_event","payload":1}`),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		ev, err := UnmarshalStreamEvent(data)
		if err != nil {
			return
		}
		out, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal: %v\ninput: %s", err, data)
		}
		second, err := UnmarshalStreamEvent(out)
		if err != nil {
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

func FuzzUnmarshalContentBlockDelta(f *testing.F) {
	seeds := [][]byte{
		[]byte(`{"type":"text_delta","text":"hi"}`),
		[]byte(`{"type":"input_json_delta","partial_json":"{"}`),
		[]byte(`{"type":"thinking_delta","thinking":"x"}`),
		[]byte(`{"type":"signature_delta","signature":"s"}`),
		[]byte(`{"type":"citations_delta","citation":{"type":"web_search_result_location","url":"https://e.com"}}`),
		[]byte(`{"type":"future_delta","p":1}`),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := UnmarshalContentBlockDelta(data)
		if err != nil {
			return
		}
		out, err := json.Marshal(d)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		second, err := UnmarshalContentBlockDelta(out)
		if err != nil {
			t.Fatalf("second unmarshal: %v", err)
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
