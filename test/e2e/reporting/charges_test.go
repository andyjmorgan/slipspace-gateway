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

// TestCharges_Anthropic_TTLSplitTierGeo proves the full charge surface of
// an Anthropic response lands on the captured Record: the per-TTL
// cache-write split, thinking tokens, the billed service tier, and the
// inference region — everything the pricing layer needs.
func TestCharges_Anthropic_TTLSplitTierGeo(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/messages",
		Body: `{
			"id": "msg_charges",
			"type": "message",
			"role": "assistant",
			"model": "claude-fable-5",
			"content": [{"type": "text", "text": "pong"}],
			"stop_reason": "end_turn",
			"usage": {
				"input_tokens": 40,
				"output_tokens": 900,
				"cache_read_input_tokens": 1000,
				"cache_creation_input_tokens": 300,
				"cache_creation": {
					"ephemeral_5m_input_tokens": 100,
					"ephemeral_1h_input_tokens": 200
				},
				"output_tokens_details": {"thinking_tokens": 640},
				"service_tier": "standard",
				"inference_geo": "us"
			}
		}`,
	})

	resp := h.PostJSON("/v1/messages",
		map[string]any{"model": "claude-haiku-4-5", "max_tokens": 1000, "messages": []map[string]string{{"role": "user", "content": "."}}},
		http.Header{"x-api-key": []string{"placeholder"}, "anthropic-version": []string{"2023-06-01"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	env := h.ExpectEvent("gateway.request", 5*time.Second)
	var ev types.RequestEvent
	if err := json.Unmarshal(env.InlinePayload, &ev); err != nil {
		t.Fatalf("decode payload: %v raw=%s", err, env.InlinePayload)
	}

	if ev.TokensCacheCreation != 300 {
		t.Errorf("TokensCacheCreation=%d, want 300", ev.TokensCacheCreation)
	}
	if ev.TokensCacheCreation5m != 100 {
		t.Errorf("TokensCacheCreation5m=%d, want 100", ev.TokensCacheCreation5m)
	}
	if ev.TokensCacheCreation1h != 200 {
		t.Errorf("TokensCacheCreation1h=%d, want 200", ev.TokensCacheCreation1h)
	}
	if ev.TokensReasoning != 640 {
		t.Errorf("TokensReasoning=%d, want 640", ev.TokensReasoning)
	}
	if ev.ServiceTier != "standard" {
		t.Errorf("ServiceTier=%q, want standard", ev.ServiceTier)
	}
	if ev.InferenceGeo != "us" {
		t.Errorf("InferenceGeo=%q, want us", ev.InferenceGeo)
	}
}

// TestCharges_OpenAIResponses_ToolCallsAndReasoning proves OpenAI's
// per-call built-in tool billing evidence (counted from *_call output
// items — the usage block never carries it) and the Responses-surface
// reasoning count both reach the Record.
func TestCharges_OpenAIResponses_ToolCallsAndReasoning(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/responses",
		Body: `{
			"id": "resp_charges",
			"object": "response",
			"model": "gpt-5.4",
			"output": [
				{"type": "web_search_call", "id": "ws_1", "status": "completed"},
				{"type": "web_search_call", "id": "ws_2", "status": "completed"},
				{"type": "message", "id": "msg_1", "content": [{"type": "output_text", "text": "found it"}]}
			],
			"usage": {
				"input_tokens": 25,
				"output_tokens": 200,
				"output_tokens_details": {"reasoning_tokens": 128}
			}
		}`,
	})

	resp := h.PostJSON("/v1/responses",
		map[string]any{"model": "gpt-5.4", "input": "."},
		nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	env := h.ExpectEvent("gateway.request", 5*time.Second)
	var ev types.RequestEvent
	if err := json.Unmarshal(env.InlinePayload, &ev); err != nil {
		t.Fatalf("decode payload: %v raw=%s", err, env.InlinePayload)
	}

	if got := ev.ServerToolUse["web_search_call"]; got != 2 {
		t.Errorf("ServerToolUse[web_search_call]=%d, want 2 (map=%v)", got, ev.ServerToolUse)
	}
	if ev.TokensReasoning != 128 {
		t.Errorf("TokensReasoning=%d, want 128", ev.TokensReasoning)
	}
}

// TestCharges_OpenAIChat_AudioTokens proves the audio-modality
// sub-buckets survive to the Record — audio bills at its own rate.
func TestCharges_OpenAIChat_AudioTokens(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body: `{
			"id": "chatcmpl-audio",
			"object": "chat.completion",
			"choices": [{"index": 0, "message": {"role": "assistant", "content": "pong"}, "finish_reason": "stop"}],
			"usage": {
				"prompt_tokens": 150,
				"completion_tokens": 60,
				"total_tokens": 210,
				"prompt_tokens_details": {"cached_tokens": 0, "audio_tokens": 120},
				"completion_tokens_details": {"audio_tokens": 45}
			}
		}`,
	})

	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt-realtime-2", "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	env := h.ExpectEvent("gateway.request", 5*time.Second)
	var ev types.RequestEvent
	if err := json.Unmarshal(env.InlinePayload, &ev); err != nil {
		t.Fatalf("decode payload: %v raw=%s", err, env.InlinePayload)
	}

	if ev.TokensInputAudio != 120 {
		t.Errorf("TokensInputAudio=%d, want 120", ev.TokensInputAudio)
	}
	if ev.TokensOutputAudio != 45 {
		t.Errorf("TokensOutputAudio=%d, want 45", ev.TokensOutputAudio)
	}
}

// TestCharges_Gemini_GroundingQueries proves Google Search grounding
// queries — billed per query, invisible to usageMetadata — are counted
// from groundingMetadata onto the Record.
func TestCharges_Gemini_GroundingQueries(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1beta/models/gemini-2.5-flash:generateContent",
		Body: `{
			"candidates": [{
				"content": {"role": "model", "parts": [{"text": "grounded answer"}]},
				"finishReason": "STOP",
				"groundingMetadata": {"webSearchQueries": ["a", "b"]}
			}],
			"usageMetadata": {
				"promptTokenCount": 8,
				"totalTokenCount": 40,
				"promptTokensDetails": [{"modality": "AUDIO", "tokenCount": 5}]
			}
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

	if got := ev.ServerToolUse["web_search_queries"]; got != 2 {
		t.Errorf("ServerToolUse[web_search_queries]=%d, want 2 (map=%v)", got, ev.ServerToolUse)
	}
	if ev.TokensInputAudio != 5 {
		t.Errorf("TokensInputAudio=%d, want 5", ev.TokensInputAudio)
	}
}
