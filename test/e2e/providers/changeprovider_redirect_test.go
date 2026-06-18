//go:build e2e

package providers_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

// These cases exercise the v1.0.2 "model-keyed redirect" pattern:
// a customer sends to the OpenAI surface, a rule fires on the model
// name and rewrites state.Provider, and the destination builder
// re-resolves the new provider's chat_completions endpoint (URL +
// credential + auth header format) automatically.
//
// No changeUrl / setHeader / changeApiKey in the rule YAML — only
// the changeProvider action. The whole machinery is the per-endpoint
// auth_header override (PR #13) + provider switch in MutableState
// (v1.0.1) + destination builder credential lookup (PR #12).

func TestRule_ChangeProvider_ClaudeRedirectsToAnthropic(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	// Stage the canned response on the anthropic chat_completions
	// upstream path. The redirected request will land here.
	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Status: http.StatusOK,
		Body:   `{"id":"chatcmpl-anth-redir","object":"chat.completion","model":"claude-haiku-4-5","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
	})

	body := map[string]any{
		"model":    "claude-haiku-4-5",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	// Send to the OPENAI surface, not anthropic.
	resp := h.PostJSON("/v1/chat/completions", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	if !strings.Contains(string(resp.Body), `"chatcmpl-anth-redir"`) {
		t.Errorf("response did not match anthropic canned body: %s", resp.Body)
	}

	got := h.LastCapturedRequest()
	if got == nil {
		t.Fatal("no upstream capture")
	}
	// Path stays /v1/chat/completions because both providers' chat
	// endpoints share the upstream template — what changes is the
	// upstream BASE URL (same in tests because both point at mockllm)
	// and, critically, the auth header.
	if got.Path != "/v1/chat/completions" {
		t.Errorf("upstream path = %q, want /v1/chat/completions", got.Path)
	}
	// Anthropic credential under Authorization: Bearer thanks to the
	// endpoint's auth_header override.
	if auth := got.Headers["Authorization"]; !strings.HasPrefix(auth, "Bearer ") {
		t.Errorf("upstream Authorization = %q, want Bearer", auth)
	}
	// The default openai-credential header (also "Authorization: Bearer")
	// is the same name, so just assert the value matches anthropic's
	// configured credential (the in-config-dev fixture uses sk-ant-dev-mock).
	if got := got.Headers["Authorization"]; !strings.Contains(got, "sk-ant-dev-mock") {
		t.Errorf("upstream Authorization = %q, want it to carry anthropic's credential", got)
	}
	if v := got.Headers["X-Api-Key"]; v != "" {
		t.Errorf("upstream X-Api-Key = %q, want empty (override is Authorization)", v)
	}
}

func TestRule_ChangeProvider_GeminiRedirectsToGemini(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	// Stage on gemini's compat path.
	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1beta/openai/chat/completions",
		Status: http.StatusOK,
		Body:   `{"id":"chatcmpl-gem-redir","object":"chat.completion","model":"gemini-2.0-flash-001","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
	})

	body := map[string]any{
		"model":    "gemini-2.0-flash-001",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	resp := h.PostJSON("/v1/chat/completions", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	if !strings.Contains(string(resp.Body), `"chatcmpl-gem-redir"`) {
		t.Errorf("response did not match gemini canned body: %s", resp.Body)
	}

	got := h.LastCapturedRequest()
	if got == nil {
		t.Fatal("no upstream capture")
	}
	if got.Path != "/v1beta/openai/chat/completions" {
		t.Errorf("upstream path = %q, want /v1beta/openai/chat/completions (gemini compat)", got.Path)
	}
	if auth := got.Headers["Authorization"]; !strings.HasPrefix(auth, "Bearer ") || !strings.Contains(auth, "dev-mock") {
		t.Errorf("upstream Authorization = %q, want Bearer carrying gemini's dev-mock credential", auth)
	}
	if v := got.Headers["X-Goog-Api-Key"]; v != "" {
		t.Errorf("upstream X-Goog-Api-Key = %q, want empty (override is Authorization)", v)
	}
}

// TestRule_ChangeProvider_OpenAIPassthrough confirms the redirect rules
// are model-conditional — an openai model name still lands on openai.
func TestRule_ChangeProvider_OpenAIPassthrough(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Status: http.StatusOK,
		Body:   `{"id":"chatcmpl-openai-pt","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`,
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
	if auth := got.Headers["Authorization"]; !strings.Contains(auth, "sk-dev-mock") {
		t.Errorf("upstream Authorization = %q, want openai's sk-dev-mock", auth)
	}
}
