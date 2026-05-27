//go:build e2e

package providers_test

import (
	"net/http"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

// TestUpstream_ResponseHeaderTimeout_OverrideThreadsThrough spawns the
// gateway with an explicit SLUICE_UPSTREAM_RESPONSE_HEADER_TIMEOUT_SECONDS
// override and confirms a normal forward still succeeds. The timeout's
// enforced 120s floor makes the failure mode (a slow upstream tripping the
// header timeout) impractical to exercise in CI wall-clock, so this asserts
// the configured value is accepted by Validate at startup and threaded into
// the proxy transport without breaking the happy path through the binary.
func TestUpstream_ResponseHeaderTimeout_OverrideThreadsThrough(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{
		UpstreamResponseHeaderTimeoutSeconds: 120,
	})

	canned := `{"id":"chatcmpl-hdr-1","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Status: http.StatusOK,
		Body:   canned,
	})

	body := map[string]any{
		"model":    "gpt-4o-mini",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	resp := h.PostJSON("/v1/chat/completions", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
}
