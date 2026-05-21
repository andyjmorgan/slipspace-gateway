package tokens_test

import (
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/middleware/tokens"
)

// TestExtract_AnthropicMessages_StreamFixture runs the extractor
// against a real captured prod streaming response. message_start and
// message_delta both carry the same totals here (small response, model
// emitted final-output count upfront); LastWins yields the
// message_delta values, which match.
func TestExtract_AnthropicMessages_StreamFixture(t *testing.T) {
	t.Parallel()
	raw := readFixture(t, "anthropic_messages_stream.sse")

	got := tokens.Extract("anthropic", "messages", raw)
	want := tokens.Snapshot{Input: 14, Output: 6, Cached: 0, CacheCreation: 0, Recognised: true}
	if got != want {
		t.Errorf("snapshot = %+v, want %+v", got, want)
	}
}

// TestExtract_AnthropicMessages_NonStream covers the static-body shape
// returned when stream=false.
func TestExtract_AnthropicMessages_NonStream(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"id": "msg_1",
		"type": "message",
		"role": "assistant",
		"content": [{"type": "text", "text": "pong"}],
		"model": "claude-haiku-4-5",
		"stop_reason": "end_turn",
		"usage": {
			"input_tokens": 30,
			"output_tokens": 2,
			"cache_read_input_tokens": 100,
			"cache_creation_input_tokens": 25
		}
	}`)

	got := tokens.Extract("anthropic", "messages", body)
	want := tokens.Snapshot{Input: 155, Output: 2, Cached: 100, CacheCreation: 25, Recognised: true}
	if got != want {
		t.Errorf("snapshot = %+v, want %+v\n(Input = 30 + 100 + 25 = 155 — see anthropicUsageToObservation comment)", got, want)
	}
}

// TestExtract_AnthropicMessages_StreamDeltaSupersedes models the
// server-tool-use case from Anthropic's docs: message_start emits
// initial input_tokens, message_delta later carries a larger cumulative
// total after the model invokes a server-side tool. LastWins must yield
// the message_delta values, not the message_start ones.
func TestExtract_AnthropicMessages_StreamDeltaSupersedes(t *testing.T) {
	t.Parallel()
	raw := []byte(`event: message_start
data: {"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","model":"claude-opus-4-7","content":[],"usage":{"input_tokens":2679,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":3}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":10682,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":510}}

event: message_stop
data: {"type":"message_stop"}

`)
	got := tokens.Extract("anthropic", "messages", raw)
	want := tokens.Snapshot{Input: 10682, Output: 510, Cached: 0, CacheCreation: 0, Recognised: true}
	if got != want {
		t.Errorf("snapshot = %+v, want %+v", got, want)
	}
}

// TestExtract_AnthropicMessages_StreamWithCaching exercises a stream
// where the request was served partially from a prompt cache and
// also wrote new content into the cache.
func TestExtract_AnthropicMessages_StreamWithCaching(t *testing.T) {
	t.Parallel()
	raw := []byte(`event: message_start
data: {"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","model":"claude-opus-4-7","content":[],"usage":{"input_tokens":50,"cache_creation_input_tokens":40,"cache_read_input_tokens":2000,"output_tokens":2}}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":50,"cache_creation_input_tokens":40,"cache_read_input_tokens":2000,"output_tokens":85}}

`)
	got := tokens.Extract("anthropic", "messages", raw)
	// Input = 50 (uncached) + 2000 (cache read) + 40 (cache write) = 2090
	want := tokens.Snapshot{Input: 2090, Output: 85, Cached: 2000, CacheCreation: 40, Recognised: true}
	if got != want {
		t.Errorf("snapshot = %+v, want %+v", got, want)
	}
}

// TestExtract_AnthropicMessages_OnlyMessageStart covers a truncated
// stream that cut off before message_delta arrived. The Input/Output
// from message_start are still authoritative observations and must
// be reported (better than dropping them — the operator at least sees
// the prompt size).
func TestExtract_AnthropicMessages_OnlyMessageStart(t *testing.T) {
	t.Parallel()
	raw := []byte(`event: message_start
data: {"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","model":"claude-opus-4-7","content":[],"usage":{"input_tokens":100,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":1}}}

`)
	got := tokens.Extract("anthropic", "messages", raw)
	want := tokens.Snapshot{Input: 100, Output: 1, Cached: 0, CacheCreation: 0, Recognised: true}
	if got != want {
		t.Errorf("snapshot = %+v, want %+v", got, want)
	}
}
