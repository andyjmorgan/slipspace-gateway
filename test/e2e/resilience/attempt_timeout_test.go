//go:build e2e

package resilience_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

// attemptTimeoutFailoverPolicy is a 2-target failover group whose primary
// carries a 1s per-target timeout_seconds while the group sets none. The
// gateway-wide response-header timeout stays at the 120s harness default, so
// only the per-target whole-attempt deadline can abandon the slow primary.
const attemptTimeoutFailoverPolicy = `
configurations:
  dev:
    credentials:
      openai: sk-dev-mock
      anthropic: sk-ant-dev-mock
      gemini: dev-mock
    bindings:
      - { protocol: chat, models: ["gpt-*"], group: bounded-failover }

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true

groups:
  bounded-failover:
    mode: failover
    targets:
      - { provider: openai, timeout_seconds: 1 }
      - { provider: anthropic }
`

// TestFailover_PerTargetTimeout_AbandonsSlowPrimary proves a group target's
// timeout_seconds is enforced through the real binary as a whole-attempt
// bound: the primary delays its status line 3s (past the 1s target timeout,
// far under the 120s header-timeout default), the orchestrator abandons it
// as a transport error and the backup serves the client.
func TestFailover_PerTargetTimeout_AbandonsSlowPrimary(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: attemptTimeoutFailoverPolicy})
	sess := h.NewSession(t)

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
		t.Errorf("body = %s; want the backup response (primary should have timed out)", string(resp.Body))
	}
	if strings.Contains(string(resp.Body), "primary-slow") {
		t.Errorf("client saw the slow primary; the per-target timeout_seconds did not fire. body=%s", string(resp.Body))
	}
	if captured := sess.Captured(); len(captured) != 2 {
		t.Errorf("upstream attempts = %d; want 2 (primary timed out, backup served)", len(captured))
	}
	if elapsed > 30*time.Second {
		t.Errorf("request took %s; the 1s target timeout did not bound the slow primary", elapsed)
	}
}

// groupTimeoutFailoverPolicy sets the bound at group level instead, with the
// backup overriding it upward so the backup's own (fast) response is not
// affected by the tight group value.
const groupTimeoutFailoverPolicy = `
configurations:
  dev:
    credentials:
      openai: sk-dev-mock
      anthropic: sk-ant-dev-mock
      gemini: dev-mock
    bindings:
      - { protocol: chat, models: ["gpt-*"], group: group-bounded }

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true

groups:
  group-bounded:
    mode: failover
    timeout_seconds: 1
    targets:
      - { provider: openai }
      - { provider: anthropic, timeout_seconds: 60 }
`

// TestFailover_GroupTimeout_AppliesToEveryTarget proves the group-level
// timeout_seconds bounds attempts whose target sets none, through the binary.
func TestFailover_GroupTimeout_AppliesToEveryTarget(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: groupTimeoutFailoverPolicy})
	sess := h.NewSession(t)

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

	resp := sess.Post("/v1/chat/completions",
		map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)

	if resp.StatusCode != http.StatusOK || !strings.Contains(string(resp.Body), "backup-fast") {
		t.Fatalf("status/body = %d %s; want the backup's 200 after the group timeout abandoned the primary", resp.StatusCode, string(resp.Body))
	}
	if captured := sess.Captured(); len(captured) != 2 {
		t.Errorf("upstream attempts = %d; want 2", len(captured))
	}
}

// retryBackoffFailoverPolicy paces the failover walk: a constant 1500ms delay
// before the second attempt. The primary fails fast with a retryable 503, so
// the only thing standing between the two upstream calls is the backoff.
const retryBackoffFailoverPolicy = `
configurations:
  dev:
    credentials:
      openai: sk-dev-mock
      anthropic: sk-ant-dev-mock
      gemini: dev-mock
    bindings:
      - { protocol: chat, models: ["gpt-*"], group: paced-failover }

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true

groups:
  paced-failover:
    mode: failover
    retry:
      enabled: true
      max_attempts: 3
      backoff_type: constant
      delay_ms: 1500
    targets:
      - { provider: openai }
      - { provider: anthropic }
`

// TestFailover_RetryBackoff_DelaysSecondAttempt proves the group's retry
// block is authorable and honoured through the binary: the two upstream
// calls are separated by at least the configured delay.
func TestFailover_RetryBackoff_DelaysSecondAttempt(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: retryBackoffFailoverPolicy})
	sess := h.NewSession(t)

	sess.Stage(harness.CannedResponse{
		Method:       http.MethodPost,
		Path:         "/v1/chat/completions",
		Status:       http.StatusServiceUnavailable,
		Body:         `{"error":"primary down"}`,
		MaxResponses: 1,
	})
	sess.Stage(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Status: http.StatusOK,
		Body:   `{"id":"backup-paced","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"}}]}`,
	})

	start := time.Now()
	resp := sess.Post("/v1/chat/completions",
		map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusOK || !strings.Contains(string(resp.Body), "backup-paced") {
		t.Fatalf("status/body = %d %s; want the backup's 200", resp.StatusCode, string(resp.Body))
	}
	if captured := sess.Captured(); len(captured) != 2 {
		t.Errorf("upstream attempts = %d; want 2", len(captured))
	}
	if elapsed < 1500*time.Millisecond {
		t.Errorf("request took %s; the 1500ms retry backoff before the second attempt did not apply", elapsed)
	}
}

// maxAttemptsFailoverPolicy caps the walk at one attempt although two targets
// exist: the client must see the primary's failure, not the backup.
const maxAttemptsFailoverPolicy = `
configurations:
  dev:
    credentials:
      openai: sk-dev-mock
      anthropic: sk-ant-dev-mock
      gemini: dev-mock
    bindings:
      - { protocol: chat, models: ["gpt-*"], group: capped-failover }

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true

groups:
  capped-failover:
    mode: failover
    retry:
      enabled: true
      max_attempts: 1
      backoff_type: constant
      delay_ms: 10
    targets:
      - { provider: openai }
      - { provider: anthropic }
`

// TestFailover_RetryMaxAttempts_StopsWalk proves retry.max_attempts caps the
// attempt budget through the binary: with max_attempts 1 the backup is never
// called and the client sees the primary's 503.
func TestFailover_RetryMaxAttempts_StopsWalk(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: maxAttemptsFailoverPolicy})
	sess := h.NewSession(t)

	sess.Stage(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Status: http.StatusServiceUnavailable,
		Body:   `{"error":"primary down"}`,
	})

	resp := sess.Post("/v1/chat/completions",
		map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("client status = %d; want 503 (budget of one attempt, no failover). body=%s", resp.StatusCode, string(resp.Body))
	}
	if captured := sess.Captured(); len(captured) != 1 {
		t.Errorf("upstream attempts = %d; want exactly 1", len(captured))
	}
}
