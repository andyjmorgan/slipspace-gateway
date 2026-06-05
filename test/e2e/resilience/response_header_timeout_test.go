//go:build e2e

package resilience_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

// headerTimeoutFailoverPolicy is a 2-target failover policy with a 1s
// per-policy response-header timeout. The primary's headers are delayed
// past that budget; the orchestrator must abandon it and fail over to the
// backup rather than waiting the gateway-wide default (120s in the harness).
const headerTimeoutFailoverPolicy = `
configurations:
  dev:
    credentials:
      openai: sk-dev-mock
      anthropic: sk-ant-dev-mock
      gemini: dev-mock
    bindings:
      - { protocol: chat, models: ["gpt-*"], group: fast-failover }

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true

groups:
  fast-failover:
    mode: failover
    response_header_timeout_seconds: 1
    targets:
      - { provider: openai }
      - { provider: anthropic }
`

// TestFailover_ResponseHeaderTimeout_AbandonsSlowPrimary proves the per-policy
// response_header_timeout_seconds replaces the gateway-wide default: the
// primary delays its status line 3s (well past the 1s policy budget but far
// under the 120s harness default), so only a working override produces the
// fail-over-and-serve-backup outcome — and quickly.
func TestFailover_ResponseHeaderTimeout_AbandonsSlowPrimary(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: headerTimeoutFailoverPolicy})
	sess := h.NewSession(t)

	// Primary: a would-be 200, but its headers arrive 3s late. With the 1s
	// override the transport cancels at 1s (transport error -> retry); had
	// the override not fired, the 3s delay completes under the 120s default
	// and this body would commit to the client.
	sess.Stage(harness.CannedResponse{
		Method:       http.MethodPost,
		Path:         "/v1/chat/completions",
		Status:       http.StatusOK,
		Body:         `{"id":"primary-slow","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"slow"}}]}`,
		DelayMs:      3000,
		MaxResponses: 1,
	})
	sess.Stage(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Status: http.StatusOK,
		Body:   `{"id":"backup-fast","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"fast"}}]}`,
	})

	start := time.Now()
	resp := sess.Post("/v1/chat/completions",
		map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("client status = %d; want 200 (recovered via failover). body=%s", resp.StatusCode, string(resp.Body))
	}
	if !strings.Contains(string(resp.Body), "backup-fast") {
		t.Errorf("body = %s; want the backup response (primary should have timed out, not committed)", string(resp.Body))
	}
	if strings.Contains(string(resp.Body), "primary-slow") {
		t.Errorf("client saw the slow primary; the header-timeout override did not fire. body=%s", string(resp.Body))
	}

	captured := sess.Captured()
	if len(captured) != 2 {
		t.Errorf("upstream attempts = %d; want 2 (primary timed out, backup served)", len(captured))
	}

	// The primary timed out at ~1s; total wall-clock must be far under both
	// the 3s upstream delay and the 120s harness default. A generous ceiling
	// keeps the assertion meaningful without being flaky on a loaded runner.
	if elapsed > 30*time.Second {
		t.Errorf("request took %s; the 1s policy header timeout did not bound the slow primary", elapsed)
	}
}
