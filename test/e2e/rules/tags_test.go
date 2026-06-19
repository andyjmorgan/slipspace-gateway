//go:build e2e

package rules_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
	"github.com/andyjmorgan/slipspace-gateway/test/e2e/types"
)

// TestTags_AddTagAction_AppearsOnRequestEvent fires a request that
// matches the dev policy's tag-openai-chat rule and asserts the
// published gateway.request event carries the tag, the live-feed
// entry surfaces it, and the side-channel counter reflects the
// application (the counter is exercised indirectly via the rule
// matching at all — the bus event is the ground-truth assertion
// because e2e can't reach the in-process Prometheus registry).
func TestTags_AddTagAction_AppearsOnRequestEvent(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"chatcmpl-tag","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
	})

	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	env := h.ExpectEvent("gateway.request", 5*time.Second)
	var ev types.RequestEvent
	if err := json.Unmarshal(env.InlinePayload, &ev); err != nil {
		t.Fatalf("decode payload: %v raw=%s", err, env.InlinePayload)
	}
	if !containsTag(ev.Tags, "surface:openai-chat") {
		t.Errorf("Tags=%v; want to contain surface:openai-chat", ev.Tags)
	}
}

// TestTags_HeaderTriggered exercises the tag-large-prompt rule —
// fires on the x-slipspace-tag-large: true header — so we cover the
// HeaderCondition→AddTagAction path in addition to the provider/
// endpoint one above.
func TestTags_HeaderTriggered(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
	})

	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "."}}},
		http.Header{"X-Slipspace-Tag-Large": []string{"true"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	env := h.ExpectEvent("gateway.request", 5*time.Second)
	var ev types.RequestEvent
	if err := json.Unmarshal(env.InlinePayload, &ev); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if !containsTag(ev.Tags, "audit:large-prompt") {
		t.Errorf("Tags=%v; want to contain audit:large-prompt", ev.Tags)
	}
	if !containsTag(ev.Tags, "surface:openai-chat") {
		t.Errorf("Tags=%v; want to also contain surface:openai-chat (both rules should fire)", ev.Tags)
	}
}

func containsTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}
