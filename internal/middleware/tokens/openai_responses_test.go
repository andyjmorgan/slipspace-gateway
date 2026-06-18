package tokens_test

import (
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/tokens"
)

// TestExtract_OpenAIResponses_NonStream covers the static-body shape of
// /v1/responses. Field names differ from chat completions:
// input_tokens / output_tokens / input_tokens_details.cached_tokens.
func TestExtract_OpenAIResponses_NonStream(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"id": "resp_1",
		"object": "response",
		"output": [{"type": "message", "content": [{"type": "output_text", "text": "pong"}]}],
		"usage": {
			"input_tokens": 50,
			"output_tokens": 4,
			"total_tokens": 54,
			"input_tokens_details": {"cached_tokens": 20},
			"output_tokens_details": {"reasoning_tokens": 2}
		}
	}`)

	got := tokens.Extract("openai", "responses", body)
	want := tokens.Snapshot{Input: 50, Output: 4, Cached: 20, CacheCreation: 0, Recognised: true}
	if got != want {
		t.Errorf("snapshot = %+v, want %+v", got, want)
	}
}

// TestExtract_OpenAIResponses_Stream models the /v1/responses SSE event
// surface: usage arrives nested under `response` on the terminal
// `response.completed` event.
func TestExtract_OpenAIResponses_Stream(t *testing.T) {
	t.Parallel()
	raw := []byte(`event: response.in_progress
data: {"type":"response.in_progress","response":{"id":"r1","status":"in_progress"}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"po"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"ng"}

event: response.completed
data: {"type":"response.completed","response":{"id":"r1","status":"completed","usage":{"input_tokens":33,"output_tokens":2,"total_tokens":35,"input_tokens_details":{"cached_tokens":10}}}}

`)
	got := tokens.Extract("openai", "responses", raw)
	want := tokens.Snapshot{Input: 33, Output: 2, Cached: 10, CacheCreation: 0, Recognised: true}
	if got != want {
		t.Errorf("snapshot = %+v, want %+v", got, want)
	}
}

// TestExtract_OpenAIResponses_NoUsage covers the case where the
// response is missing the usage block entirely — Recognised must
// stay false.
func TestExtract_OpenAIResponses_NoUsage(t *testing.T) {
	t.Parallel()
	body := []byte(`{"id":"r1","object":"response","status":"in_progress"}`)
	got := tokens.Extract("openai", "responses", body)
	if got.Recognised {
		t.Errorf("no usage: Recognised=true, want false; snap=%+v", got)
	}
}
