//go:build e2e

package reporting_test

import (
	"encoding/json"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
	"github.com/andyjmorgan/slipspace-gateway/test/e2e/types"
)

func approxUSD(t *testing.T, got, want float64, what string) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// TestCost_Anthropic_FullSurface prices an Anthropic response carrying
// every charge category through the real binary: uncached input, cache
// read, the 5m/1h write split, output, the "us" geo multiplier, and a
// server-tool call — asserting the Record's cost block against the
// embedded 2026-07 card (claude-haiku-4*: in 1, out 5, read 0.1,
// w5m 1.25, w1h 2; web_search_requests $10/1k; geo us 1.1×).
func TestCost_Anthropic_FullSurface(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/messages",
		Body: `{
			"id": "msg_cost",
			"type": "message",
			"role": "assistant",
			"model": "claude-haiku-4-5",
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
				"server_tool_use": {"web_search_requests": 3},
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
	if ev.Cost == nil {
		t.Fatalf("Cost=nil, want a cost block (record=%s)", env.InlinePayload)
	}

	const mtok = 1_000_000.0
	geo := 1.1
	wantInput := 40 * 1.0 / mtok * geo
	wantRead := 1000 * 0.1 / mtok * geo
	wantWrite := (100*1.25 + 200*2.0) / mtok * geo
	wantOutput := 900 * 5.0 / mtok * geo
	wantTools := 3 * 10.0 / 1000.0

	approxUSD(t, ev.Cost.ByCategory["input"], wantInput, "input")
	approxUSD(t, ev.Cost.ByCategory["cache_read"], wantRead, "cache_read")
	approxUSD(t, ev.Cost.ByCategory["cache_write"], wantWrite, "cache_write")
	approxUSD(t, ev.Cost.ByCategory["output"], wantOutput, "output")
	approxUSD(t, ev.Cost.ByCategory["tool_calls"], wantTools, "tool_calls")
	approxUSD(t, ev.Cost.TotalUSD, wantInput+wantRead+wantWrite+wantOutput+wantTools, "total")
	if ev.Cost.TableVersion == "" {
		t.Errorf("TableVersion empty, want the embedded card version")
	}
}

// TestCost_UnmatchedModel_NoCostBlock proves an unmatched model yields
// no cost block on the Record — unpriced, never a guessed $0. The model
// is synthetic (never hardcode a real model as a no-match probe).
func TestCost_UnmatchedModel_NoCostBlock(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body: `{
			"id": "chatcmpl-nocost",
			"object": "chat.completion",
			"choices": [{"index": 0, "message": {"role": "assistant", "content": "pong"}, "finish_reason": "stop"}],
			"usage": {"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12}
		}`,
	})

	// "gpt-unpriced-internal" routes (the dev binding matches gpt-*) but
	// is synthetic so a growing defaults card can never start matching it.
	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt-unpriced-internal", "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	env := h.ExpectEvent("gateway.request", 5*time.Second)
	var ev types.RequestEvent
	if err := json.Unmarshal(env.InlinePayload, &ev); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if ev.TokensIn != 10 {
		t.Fatalf("TokensIn=%d, want 10 (usage must still extract)", ev.TokensIn)
	}
	if ev.Cost != nil {
		t.Errorf("Cost=%+v, want nil for an unmatched model", ev.Cost)
	}
}

// TestCost_Gemini_GroundingPriced covers a second vendor arm end to end:
// gemini-2.5-flash token rates (in 0.30 / out 2.50, batch-only tiers) —
// grounding queries are counted but carry no rate on the 2.5 family
// (the vendor bills 2.5 grounding per prompt, a different unit), so
// tool_calls must be absent while token categories are priced.
func TestCost_Gemini_GroundingPriced(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1beta/models/gemini-2.5-flash:generateContent",
		Body: `{
			"candidates": [{
				"content": {"role": "model", "parts": [{"text": "grounded"}]},
				"finishReason": "STOP",
				"groundingMetadata": {"webSearchQueries": ["a", "b"]}
			}],
			"usageMetadata": {"promptTokenCount": 100000, "totalTokenCount": 140000}
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
		t.Fatalf("decode payload: %v", err)
	}
	if ev.Cost == nil {
		t.Fatalf("Cost=nil, want a cost block")
	}
	approxUSD(t, ev.Cost.ByCategory["input"], 100000*0.30/1_000_000, "input")
	approxUSD(t, ev.Cost.ByCategory["output"], 40000*2.5/1_000_000, "output")
	if _, has := ev.Cost.ByCategory["tool_calls"]; has {
		t.Errorf("tool_calls priced on gemini-2.5 (%v) — the 2.5 grounding unit is per prompt, deliberately unrated", ev.Cost.ByCategory)
	}
}
