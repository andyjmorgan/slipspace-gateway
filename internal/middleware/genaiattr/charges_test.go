package genaiattr_test

import (
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/genaiattr"
)

// TestExtractResponse_Responses_ReasoningTokens covers the previously
// dropped Responses-surface reasoning count on a non-streaming body —
// the asymmetry with Chat Completions this closes.
func TestExtractResponse_Responses_ReasoningTokens(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"id": "resp_1",
		"object": "response",
		"model": "gpt-5.4",
		"output": [{"type": "message", "id": "msg_1", "content": [{"type": "output_text", "text": "hi"}]}],
		"usage": {
			"input_tokens": 10,
			"output_tokens": 90,
			"output_tokens_details": {"reasoning_tokens": 64}
		}
	}`)

	a := genaiattr.ExtractResponse("responses", body)
	if a.ReasoningTokens == nil || *a.ReasoningTokens != 64 {
		t.Errorf("ReasoningTokens = %v, want 64", a.ReasoningTokens)
	}
}

// TestExtractResponse_Responses_ReasoningTokens_Stream reads the count
// from the terminal response.completed event's nested response.usage.
func TestExtractResponse_Responses_ReasoningTokens_Stream(t *testing.T) {
	t.Parallel()
	raw := []byte(`event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_1","delta":"hi"}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.4","output":[],"usage":{"input_tokens":10,"output_tokens":90,"output_tokens_details":{"reasoning_tokens":48}}}}

`)
	a := genaiattr.ExtractResponse("responses", raw)
	if a.ReasoningTokens == nil || *a.ReasoningTokens != 48 {
		t.Errorf("ReasoningTokens = %v, want 48", a.ReasoningTokens)
	}
}

// TestExtractResponse_Anthropic_UsageDescriptors covers the Anthropic
// pricing descriptors on a non-streaming body: thinking tokens surface
// as ReasoningTokens, plus the billed tier and inference region.
func TestExtractResponse_Anthropic_UsageDescriptors(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"id": "msg_1",
		"type": "message",
		"model": "claude-fable-5",
		"content": [{"type": "text", "text": "pong"}],
		"stop_reason": "end_turn",
		"usage": {
			"input_tokens": 20,
			"output_tokens": 500,
			"output_tokens_details": {"thinking_tokens": 320},
			"service_tier": "standard",
			"inference_geo": "us"
		}
	}`)

	a := genaiattr.ExtractResponse("messages", body)
	if a.ReasoningTokens == nil || *a.ReasoningTokens != 320 {
		t.Errorf("ReasoningTokens = %v, want 320", a.ReasoningTokens)
	}
	if a.ServiceTier != "standard" {
		t.Errorf("ServiceTier = %q, want standard", a.ServiceTier)
	}
	if a.InferenceGeo != "us" {
		t.Errorf("InferenceGeo = %q, want us", a.InferenceGeo)
	}
}

// TestExtractResponse_Anthropic_UsageDescriptors_Stream reads the
// descriptors across the streaming split: tier/geo arrive on
// message_start's nested usage, the cumulative thinking count on
// message_delta supersedes the initial value.
func TestExtractResponse_Anthropic_UsageDescriptors_Stream(t *testing.T) {
	t.Parallel()
	raw := []byte(`event: message_start
data: {"type":"message_start","message":{"id":"m1","model":"claude-fable-5","usage":{"input_tokens":20,"output_tokens":1,"output_tokens_details":{"thinking_tokens":1},"service_tier":"priority","inference_geo":"us"}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"pong"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":20,"output_tokens":400,"output_tokens_details":{"thinking_tokens":256}}}

event: message_stop
data: {"type":"message_stop"}

`)
	a := genaiattr.ExtractResponse("messages", raw)
	if a.ReasoningTokens == nil || *a.ReasoningTokens != 256 {
		t.Errorf("ReasoningTokens = %v, want 256 (message_delta supersedes)", a.ReasoningTokens)
	}
	if a.ServiceTier != "priority" {
		t.Errorf("ServiceTier = %q, want priority", a.ServiceTier)
	}
	if a.InferenceGeo != "us" {
		t.Errorf("InferenceGeo = %q, want us", a.InferenceGeo)
	}
}
