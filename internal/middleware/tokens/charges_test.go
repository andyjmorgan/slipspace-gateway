package tokens_test

import (
	"maps"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/sseframe"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/tokens"
)

// TestExtract_AnthropicMessages_CacheTTLSplit covers the nested
// cache_creation per-TTL breakdown alongside the flat total. The tiers
// bill at different write premiums, so both must survive extraction.
func TestExtract_AnthropicMessages_CacheTTLSplit(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"id": "msg_1",
		"type": "message",
		"model": "claude-haiku-4-5",
		"usage": {
			"input_tokens": 10,
			"output_tokens": 5,
			"cache_read_input_tokens": 0,
			"cache_creation_input_tokens": 300,
			"cache_creation": {
				"ephemeral_5m_input_tokens": 100,
				"ephemeral_1h_input_tokens": 200
			}
		}
	}`)

	got := tokens.Extract("anthropic", "messages", body)
	want := tokens.Snapshot{
		Input: 310, Output: 5, CacheCreation: 300,
		CacheCreation5m: 100, CacheCreation1h: 200, Recognised: true,
	}
	if got != want {
		t.Errorf("snapshot = %+v, want %+v", got, want)
	}
}

// TestExtract_AnthropicMessages_CacheTTLSplit_Stream proves the split
// survives the streaming path: message_delta's cumulative usage carries
// the nested breakdown and LastWins keeps it.
func TestExtract_AnthropicMessages_CacheTTLSplit_Stream(t *testing.T) {
	t.Parallel()
	raw := []byte(`event: message_start
data: {"type":"message_start","message":{"id":"m1","usage":{"input_tokens":4,"output_tokens":1}}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":4,"output_tokens":9,"cache_creation_input_tokens":50,"cache_creation":{"ephemeral_5m_input_tokens":50,"ephemeral_1h_input_tokens":0}}}

event: message_stop
data: {"type":"message_stop"}

`)
	got := tokens.Extract("anthropic", "messages", raw)
	want := tokens.Snapshot{
		Input: 54, Output: 9, CacheCreation: 50,
		CacheCreation5m: 50, Recognised: true,
	}
	if got != want {
		t.Errorf("snapshot = %+v, want %+v", got, want)
	}
}

// TestExtract_OpenAIChat_AudioTokens covers the prompt- and
// completion-side audio_tokens sub-buckets, which bill at audio rates.
func TestExtract_OpenAIChat_AudioTokens(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"id": "chatcmpl-1",
		"object": "chat.completion",
		"usage": {
			"prompt_tokens": 120,
			"completion_tokens": 40,
			"total_tokens": 160,
			"prompt_tokens_details": {"cached_tokens": 16, "audio_tokens": 80},
			"completion_tokens_details": {"reasoning_tokens": 0, "audio_tokens": 30}
		}
	}`)

	got := tokens.Extract("openai", "chat", body)
	want := tokens.Snapshot{
		Input: 120, Output: 40, Cached: 16,
		InputAudio: 80, OutputAudio: 30, Recognised: true,
	}
	if got != want {
		t.Errorf("snapshot = %+v, want %+v", got, want)
	}
}

// TestExtract_GeminiContent_AudioModality covers the per-modality
// *TokensDetails breakdowns: the AUDIO members feed the audio buckets,
// other modalities stay folded in the gross counts.
func TestExtract_GeminiContent_AudioModality(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"candidates": [{"content": {"parts": [{"text": "ok"}]}}],
		"usageMetadata": {
			"promptTokenCount": 500,
			"totalTokenCount": 620,
			"promptTokensDetails": [
				{"modality": "TEXT", "tokenCount": 100},
				{"modality": "AUDIO", "tokenCount": 400}
			],
			"candidatesTokensDetails": [
				{"modality": "AUDIO", "tokenCount": 20},
				{"modality": "TEXT", "tokenCount": 100}
			]
		}
	}`)

	got := tokens.Extract("gemini", "generate_content", body)
	want := tokens.Snapshot{
		Input: 500, Output: 120,
		InputAudio: 400, OutputAudio: 20, Recognised: true,
	}
	if got != want {
		t.Errorf("snapshot = %+v, want %+v", got, want)
	}
}

// TestServerToolUse_Responses_NonStream counts server-tool *_call output
// items on a non-streaming Responses body. function_call is
// client-executed and must not count.
func TestServerToolUse_Responses_NonStream(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"id": "resp_1",
		"object": "response",
		"output": [
			{"type": "web_search_call", "id": "ws_1", "status": "completed"},
			{"type": "web_search_call", "id": "ws_2", "status": "completed"},
			{"type": "code_interpreter_call", "id": "ci_1", "status": "completed"},
			{"type": "function_call", "id": "fc_1", "name": "get_weather", "arguments": "{}"},
			{"type": "message", "id": "msg_1", "content": [{"type": "output_text", "text": "done"}]}
		],
		"usage": {"input_tokens": 10, "output_tokens": 5}
	}`)

	got := tokens.ServerToolUseFrames("openai", "responses", sseframe.Collate(body))
	want := map[string]int{"web_search_call": 2, "code_interpreter_call": 1}
	if !maps.Equal(got, want) {
		t.Errorf("counters = %v, want %v", got, want)
	}
}

// TestServerToolUse_Responses_StreamDedup proves the same item observed
// on output_item.added, output_item.done, and the response.completed
// output array counts exactly once (dedup by item id).
func TestServerToolUse_Responses_StreamDedup(t *testing.T) {
	t.Parallel()
	raw := []byte(`event: response.output_item.added
data: {"type":"response.output_item.added","item":{"type":"web_search_call","id":"ws_1","status":"in_progress"}}

event: response.output_item.done
data: {"type":"response.output_item.done","item":{"type":"web_search_call","id":"ws_1","status":"completed"}}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_1","output":[{"type":"web_search_call","id":"ws_1","status":"completed"},{"type":"message","id":"msg_1"}],"usage":{"input_tokens":10,"output_tokens":5}}}

`)
	got := tokens.ServerToolUseFrames("openai", "responses", sseframe.Collate(raw))
	want := map[string]int{"web_search_call": 1}
	if !maps.Equal(got, want) {
		t.Errorf("counters = %v, want %v", got, want)
	}
}

// TestServerToolUse_Responses_NoServerTools yields nil when the response
// contains only client tool calls and messages.
func TestServerToolUse_Responses_NoServerTools(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"id": "resp_1",
		"output": [{"type": "function_call", "id": "fc_1", "name": "f", "arguments": "{}"}],
		"usage": {"input_tokens": 1, "output_tokens": 1}
	}`)
	if got := tokens.ServerToolUseFrames("openai", "responses", sseframe.Collate(body)); got != nil {
		t.Errorf("counters = %v, want nil", got)
	}
}

// TestServerToolUse_Gemini_Grounding counts webSearchQueries from the
// grounding metadata block — Gemini's per-query billing unit.
func TestServerToolUse_Gemini_Grounding(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"candidates": [{
			"content": {"parts": [{"text": "grounded answer"}]},
			"groundingMetadata": {
				"webSearchQueries": ["query one", "query two", "query three"]
			}
		}],
		"usageMetadata": {"promptTokenCount": 5, "totalTokenCount": 30}
	}`)

	got := tokens.ServerToolUseFrames("gemini", "generate_content", sseframe.Collate(body))
	want := map[string]int{"web_search_queries": 3}
	if !maps.Equal(got, want) {
		t.Errorf("counters = %v, want %v", got, want)
	}
}

// TestServerToolUse_Gemini_NotGrounded yields nil for an ordinary
// ungrounded response.
func TestServerToolUse_Gemini_NotGrounded(t *testing.T) {
	t.Parallel()
	body := []byte(`{"candidates":[{"content":{"parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":1,"totalTokenCount":2}}`)
	if got := tokens.ServerToolUseFrames("gemini", "generate_content", sseframe.Collate(body)); got != nil {
		t.Errorf("counters = %v, want nil", got)
	}
}

// TestServerToolUse_ChatCompletions_Nil documents the deliberate gap:
// the Chat Completions response carries no per-call evidence, so
// nothing is counted rather than something guessed.
func TestServerToolUse_ChatCompletions_Nil(t *testing.T) {
	t.Parallel()
	body := []byte(`{"id":"c1","usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	if got := tokens.ServerToolUseFrames("openai", "chat", sseframe.Collate(body)); got != nil {
		t.Errorf("counters = %v, want nil", got)
	}
}
