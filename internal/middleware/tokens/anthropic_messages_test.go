package tokens_test

import (
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/middleware/sseframe"
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

// serverToolUse collates raw and runs the server-tool counter extraction —
// the test-side mirror of how the reporter pairs ServerToolUseFrames with
// ExtractFrames on the same collated frames.
func serverToolUse(endpoint string, raw []byte) map[string]int {
	return tokens.ServerToolUseFrames("anthropic", endpoint, sseframe.Collate(raw))
}

// TestServerToolUse_Stream models the captured prod shape: the final
// message_delta carries usage.server_tool_use with one counter per server
// tool family, zero-filled for the unused ones. The map must come back
// generically — every wire key, no hardcoded names.
func TestServerToolUse_Stream(t *testing.T) {
	t.Parallel()
	raw := []byte(`event: message_start
data: {"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","model":"claude-opus-4-8","content":[],"usage":{"input_tokens":2918,"output_tokens":48}}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":10668,"output_tokens":968,"server_tool_use":{"web_search_requests":1,"web_fetch_requests":0}}}

`)
	got := serverToolUse("messages", raw)
	if len(got) != 2 || got["web_search_requests"] != 1 || got["web_fetch_requests"] != 0 {
		t.Errorf("server_tool_use = %v, want map[web_fetch_requests:0 web_search_requests:1]", got)
	}
	// The token snapshot itself is untouched by the counters.
	want := tokens.Snapshot{Input: 10668, Output: 968, Recognised: true}
	if snap := tokens.Extract("anthropic", "messages", raw); snap != want {
		t.Errorf("snapshot = %+v, want %+v", snap, want)
	}
}

// TestServerToolUse_NonStream covers the whole-body shape with multiple
// counters, including a future key the gateway has never heard of.
func TestServerToolUse_NonStream(t *testing.T) {
	t.Parallel()
	body := []byte(`{"id":"m2","type":"message","usage":{"input_tokens":5,"output_tokens":7,"server_tool_use":{"web_search_requests":2,"code_execution_requests":3,"future_tool_requests":1}}}`)
	got := serverToolUse("messages", body)
	if len(got) != 3 || got["web_search_requests"] != 2 || got["code_execution_requests"] != 3 || got["future_tool_requests"] != 1 {
		t.Errorf("server_tool_use = %v", got)
	}
}

// TestServerToolUse_Absent: no server_tool_use block means nil — the
// reporter projects no gen_ai.usage.server_tool_use.* attributes.
func TestServerToolUse_Absent(t *testing.T) {
	t.Parallel()
	body := []byte(`{"id":"m3","type":"message","usage":{"input_tokens":5,"output_tokens":7}}`)
	if got := serverToolUse("messages", body); got != nil {
		t.Errorf("server_tool_use = %v, want nil when absent", got)
	}
	// Non-Anthropic endpoints have no analogous counters (OpenAI Responses
	// usage carries none — invocations are billed per *_call output item).
	openai := []byte(`{"id":"r1","usage":{"input_tokens":5,"output_tokens":7,"total_tokens":12}}`)
	if got := serverToolUse("responses", openai); got != nil {
		t.Errorf("responses server_tool_use = %v, want nil", got)
	}
}

// TestServerToolUse_NonIntegerMemberSkipped: a future non-integer member of
// the block must neither appear in the map nor break the integer counters
// beside it (the raw-value decode guards the whole usage unmarshal too).
func TestServerToolUse_NonIntegerMemberSkipped(t *testing.T) {
	t.Parallel()
	body := []byte(`{"type":"message","usage":{"input_tokens":5,"output_tokens":7,"server_tool_use":{"web_search_requests":4,"detail":{"nested":true}}}}`)
	got := serverToolUse("messages", body)
	if len(got) != 1 || got["web_search_requests"] != 4 {
		t.Errorf("server_tool_use = %v, want only web_search_requests:4", got)
	}
	want := tokens.Snapshot{Input: 5, Output: 7, Recognised: true}
	if snap := tokens.Extract("anthropic", "messages", body); snap != want {
		t.Errorf("snapshot = %+v, want %+v (usage decode must survive)", snap, want)
	}
}
