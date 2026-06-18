//go:build e2e

package providers_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

// These tests exercise the v1.0.2 OpenAI-compatible chat completions
// surface on anthropic + gemini. The destination builder's per-endpoint
// auth_header / auth_format override (PR #13) is what makes the
// upstream credential land in Authorization: Bearer rather than the
// provider-native x-api-key / x-goog-api-key.

func TestAnthropic_ChatCompletions_OpenAICompat_NonStreaming(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	canned := `{"id":"chatcmpl-anth-1","object":"chat.completion","model":"claude-3-haiku-20240307","choices":[{"index":0,"message":{"role":"assistant","content":"hi from anth"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":3,"total_tokens":4}}`
	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Status: http.StatusOK,
		Body:   canned,
	})

	body := map[string]any{
		"model":    "claude-3-haiku-20240307",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	resp := h.PostJSON("/v1/chat/completions", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	if !strings.Contains(string(resp.Body), `"chatcmpl-anth-1"`) {
		t.Errorf("body missing id: %s", resp.Body)
	}

	got := h.LastCapturedRequest()
	if got == nil {
		t.Fatal("no upstream capture")
	}
	if got.Path != "/v1/chat/completions" {
		t.Errorf("upstream path = %q, want /v1/chat/completions (prefix stripped)", got.Path)
	}
	if auth := got.Headers["Authorization"]; !strings.HasPrefix(auth, "Bearer ") {
		t.Errorf("upstream Authorization = %q, want Bearer <key>", auth)
	}
	if v := got.Headers["X-Api-Key"]; v != "" {
		t.Errorf("upstream X-Api-Key = %q, want empty (override is Authorization)", v)
	}
}

func TestAnthropic_ChatCompletions_OpenAICompat_Streaming(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method:    http.MethodPost,
		Path:      "/v1/chat/completions",
		Streaming: true,
		Headers:   map[string]string{"Content-Type": "text/event-stream"},
		StreamChunks: []harness.CannedStreamChunk{
			{Data: `{"id":"c-anth","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`},
			{Data: `{"id":"c-anth","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`},
			{Data: `{"id":"c-anth","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`},
			{Data: "[DONE]"},
		},
	})

	body := map[string]any{
		"model":    "claude-3-haiku-20240307",
		"stream":   true,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	stream := h.PostStream("/v1/chat/completions", body, nil)
	chunks := stream.CollectAll(5 * time.Second)
	if stream.Status() != http.StatusOK {
		t.Fatalf("status=%d", stream.Status())
	}
	if len(chunks) < 4 {
		t.Fatalf("chunks=%d want >=4", len(chunks))
	}
	if !chunks[len(chunks)-1].IsDone() {
		t.Errorf("last chunk not [DONE]: %+v", chunks[len(chunks)-1])
	}
}

func TestAnthropic_ChatCompletions_BarePathNotAccepted(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	// The anthropic provider has prefix_required: true, so the bare
	// /v1/chat/completions inbound path must route to openai
	// (default provider) rather than anthropic. Asserting via the
	// upstream credential — openai's stays Bearer sk-dev-mock; if
	// it ever resolved to anthropic, the upstream would see the
	// anthropic credential under Authorization: Bearer.
	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"openai-1","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`,
	})

	body := map[string]any{
		"model":    "gpt-4o-mini",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	resp := h.PostJSON("/v1/chat/completions", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}

	got := h.LastCapturedRequest()
	if got == nil {
		t.Fatal("no upstream capture")
	}
	if auth := got.Headers["Authorization"]; !strings.HasPrefix(auth, "Bearer sk-dev-mock") {
		t.Errorf("upstream Authorization = %q, want openai's Bearer sk-dev-mock (bare path must not route to anthropic)", auth)
	}
}

func TestGemini_ChatCompletions_OpenAICompat_NonStreaming(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	canned := `{"id":"chatcmpl-gem-1","object":"chat.completion","model":"gemini-2.0-flash-001","choices":[{"index":0,"message":{"role":"assistant","content":"hi from gem"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":3,"total_tokens":4}}`
	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1beta/openai/chat/completions",
		Status: http.StatusOK,
		Body:   canned,
	})

	body := map[string]any{
		"model":    "gemini-2.0-flash-001",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	resp := h.PostJSON("/v1/chat/completions", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	if !strings.Contains(string(resp.Body), `"chatcmpl-gem-1"`) {
		t.Errorf("body missing id: %s", resp.Body)
	}

	got := h.LastCapturedRequest()
	if got == nil {
		t.Fatal("no upstream capture")
	}
	if got.Path != "/v1beta/openai/chat/completions" {
		t.Errorf("upstream path = %q, want /v1beta/openai/chat/completions (prefix stripped)", got.Path)
	}
	if auth := got.Headers["Authorization"]; !strings.HasPrefix(auth, "Bearer ") {
		t.Errorf("upstream Authorization = %q, want Bearer <key>", auth)
	}
	if v := got.Headers["X-Goog-Api-Key"]; v != "" {
		t.Errorf("upstream X-Goog-Api-Key = %q, want empty (override is Authorization)", v)
	}
}

func TestGemini_ChatCompletions_OpenAICompat_Streaming(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method:    http.MethodPost,
		Path:      "/v1beta/openai/chat/completions",
		Streaming: true,
		Headers:   map[string]string{"Content-Type": "text/event-stream"},
		StreamChunks: []harness.CannedStreamChunk{
			{Data: `{"id":"c-gem","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`},
			{Data: `{"id":"c-gem","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`},
			{Data: `{"id":"c-gem","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`},
			{Data: "[DONE]"},
		},
	})

	body := map[string]any{
		"model":    "gemini-2.0-flash-001",
		"stream":   true,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	stream := h.PostStream("/v1/chat/completions", body, nil)
	chunks := stream.CollectAll(5 * time.Second)
	if stream.Status() != http.StatusOK {
		t.Fatalf("status=%d", stream.Status())
	}
	if len(chunks) < 4 {
		t.Fatalf("chunks=%d want >=4", len(chunks))
	}
}
