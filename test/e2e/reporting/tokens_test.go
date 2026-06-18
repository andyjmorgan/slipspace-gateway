//go:build e2e

package reporting_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
	"github.com/andyjmorgan/slipspace-gateway/test/e2e/types"
)

// TestTokens_OpenAIChat_NonStream proves the reporter parses upstream
// usage off a /v1/chat/completions response body and surfaces it on the
// published gateway.request event. The body shape mirrors what OpenAI
// emits in prod, captured during design.
func TestTokens_OpenAIChat_NonStream(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body: `{
			"id": "chatcmpl-tokens",
			"object": "chat.completion",
			"choices": [{"index": 0, "message": {"role": "assistant", "content": "pong"}, "finish_reason": "stop"}],
			"usage": {
				"prompt_tokens": 42,
				"completion_tokens": 7,
				"total_tokens": 49,
				"prompt_tokens_details": {"cached_tokens": 11}
			}
		}`,
	})

	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt-4o", "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	env := h.ExpectEvent("gateway.request", 5*time.Second)
	var ev types.RequestEvent
	if err := json.Unmarshal(env.InlinePayload, &ev); err != nil {
		t.Fatalf("decode payload: %v raw=%s", err, env.InlinePayload)
	}

	if ev.TokensIn != 42 {
		t.Errorf("TokensIn=%d, want 42", ev.TokensIn)
	}
	if ev.TokensOut != 7 {
		t.Errorf("TokensOut=%d, want 7", ev.TokensOut)
	}
	if ev.TokensCached != 11 {
		t.Errorf("TokensCached=%d, want 11", ev.TokensCached)
	}
	if ev.TokensCacheCreation != 0 {
		t.Errorf("TokensCacheCreation=%d, want 0 (OpenAI doesn't bill cache writes separately)", ev.TokensCacheCreation)
	}
}

// TestTokens_OpenAIChat_Stream proves the extractor handles a real SSE
// transcript: every chunk is null-usage up to the terminal chunk that
// carries the full block + empty choices array. Mirrors the live prod
// shape verified against api.openai.com during design.
func TestTokens_OpenAIChat_Stream(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method:    http.MethodPost,
		Path:      "/v1/chat/completions",
		Streaming: true,
		Headers:   map[string]string{"Content-Type": "text/event-stream"},
		StreamChunks: []harness.CannedStreamChunk{
			{Data: `{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"p"}}],"usage":null}`},
			{Data: `{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"ong"}}],"usage":null}`},
			{Data: `{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":null}`},
			{Data: `{"id":"c","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":13,"completion_tokens":3,"total_tokens":16,"prompt_tokens_details":{"cached_tokens":0}}}`},
			{Data: `[DONE]`},
		},
	})

	stream := h.PostStream("/v1/chat/completions",
		map[string]any{"model": "gpt-4o", "stream": true, "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)
	stream.Discard()

	env := h.ExpectEvent("gateway.request", 5*time.Second)
	var ev types.RequestEvent
	if err := json.Unmarshal(env.InlinePayload, &ev); err != nil {
		t.Fatalf("decode payload: %v raw=%s", err, env.InlinePayload)
	}
	if !ev.Streaming {
		t.Errorf("Streaming=false, want true")
	}
	if ev.TokensIn != 13 {
		t.Errorf("TokensIn=%d, want 13", ev.TokensIn)
	}
	if ev.TokensOut != 3 {
		t.Errorf("TokensOut=%d, want 3", ev.TokensOut)
	}
}

// TestTokens_Anthropic_StreamDeltaWithCache proves the Anthropic
// extractor folds cache_read_input_tokens + cache_creation_input_tokens
// into TokensIn and surfaces TokensCached + TokensCacheCreation
// separately on the event.
func TestTokens_Anthropic_StreamDeltaWithCache(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method:    http.MethodPost,
		Path:      "/v1/messages",
		Streaming: true,
		Headers:   map[string]string{"Content-Type": "text/event-stream"},
		StreamChunks: []harness.CannedStreamChunk{
			{Data: `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude-opus-4-7","content":[],"usage":{"input_tokens":80,"cache_creation_input_tokens":20,"cache_read_input_tokens":500,"output_tokens":1}}}`},
			{Data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"pong"}}`},
			{Data: `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":80,"cache_creation_input_tokens":20,"cache_read_input_tokens":500,"output_tokens":12}}`},
			{Data: `{"type":"message_stop"}`},
		},
	})

	stream := h.PostStream("/v1/messages",
		map[string]any{"model": "claude-haiku-4-5", "max_tokens": 100, "stream": true, "messages": []map[string]string{{"role": "user", "content": "."}}},
		http.Header{"x-api-key": []string{"placeholder"}, "anthropic-version": []string{"2023-06-01"}})
	stream.Discard()

	env := h.ExpectEvent("gateway.request", 5*time.Second)
	var ev types.RequestEvent
	if err := json.Unmarshal(env.InlinePayload, &ev); err != nil {
		t.Fatalf("decode payload: %v raw=%s", err, env.InlinePayload)
	}
	// Input = 80 + 500 + 20 = 600 (see anthropicUsageToObservation).
	if ev.TokensIn != 600 {
		t.Errorf("TokensIn=%d, want 600 (input_tokens 80 + cache_read 500 + cache_creation 20)", ev.TokensIn)
	}
	if ev.TokensOut != 12 {
		t.Errorf("TokensOut=%d, want 12 (message_delta supersedes message_start)", ev.TokensOut)
	}
	if ev.TokensCached != 500 {
		t.Errorf("TokensCached=%d, want 500", ev.TokensCached)
	}
	if ev.TokensCacheCreation != 20 {
		t.Errorf("TokensCacheCreation=%d, want 20", ev.TokensCacheCreation)
	}
}

// TestTokens_Gemini_NonStreamMaxTokens covers the prod-captured case
// where Gemini's response carries promptTokenCount + thoughtsTokenCount
// + totalTokenCount but no candidatesTokenCount (finishReason=MAX_TOKENS,
// model burned its budget on thoughts). Output is derived as total -
// prompt to fold thoughts into the chargeable output count.
func TestTokens_Gemini_NonStreamMaxTokens(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1beta/models/gemini-2.5-flash:generateContent",
		Body: `{
			"candidates": [{"content": {"role": "model"}, "finishReason": "MAX_TOKENS", "index": 0}],
			"usageMetadata": {
				"promptTokenCount": 6,
				"totalTokenCount": 23,
				"thoughtsTokenCount": 17,
				"serviceTier": "standard"
			},
			"modelVersion": "gemini-2.5-flash"
		}`,
	})

	resp := h.PostJSON("/v1beta/models/gemini-2.5-flash:generateContent",
		map[string]any{"contents": []map[string]any{{"parts": []map[string]string{{"text": "."}}}}},
		http.Header{"x-goog-api-key": []string{"placeholder"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	env := h.ExpectEvent("gateway.request", 5*time.Second)
	var ev types.RequestEvent
	if err := json.Unmarshal(env.InlinePayload, &ev); err != nil {
		t.Fatalf("decode payload: %v raw=%s", err, env.InlinePayload)
	}
	if ev.TokensIn != 6 {
		t.Errorf("TokensIn=%d, want 6", ev.TokensIn)
	}
	if ev.TokensOut != 17 {
		t.Errorf("TokensOut=%d, want 17 (total 23 - prompt 6, folds thoughts in)", ev.TokensOut)
	}
}

// TestTokens_StreamingWithoutUsage_NoFields proves the reporter leaves
// the token fields zero when the upstream omits a usage block (e.g. a
// streaming request without stream_options.include_usage=true, or a
// client-cancelled stream that didn't reach the terminal chunk). Better
// than inventing a zero-ish total that would be indistinguishable from
// a real zero.
func TestTokens_StreamingWithoutUsage_NoFields(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method:    http.MethodPost,
		Path:      "/v1/chat/completions",
		Streaming: true,
		Headers:   map[string]string{"Content-Type": "text/event-stream"},
		StreamChunks: []harness.CannedStreamChunk{
			{Data: `{"id":"c","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}`},
			{Data: `{"id":"c","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`},
			{Data: `[DONE]`},
		},
	})

	stream := h.PostStream("/v1/chat/completions",
		map[string]any{"model": "gpt-4o", "stream": true, "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)
	stream.Discard()

	env := h.ExpectEvent("gateway.request", 5*time.Second)
	var ev types.RequestEvent
	if err := json.Unmarshal(env.InlinePayload, &ev); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if ev.TokensIn != 0 || ev.TokensOut != 0 || ev.TokensCached != 0 || ev.TokensCacheCreation != 0 {
		t.Errorf("token fields populated on no-usage stream: %+v", ev)
	}
}
