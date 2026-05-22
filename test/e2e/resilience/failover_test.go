//go:build e2e

package resilience_test

import (
	"net/http"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

// failoverPolicy is the standard 2-target failover policy used by
// the E2E suite: openai-primary then openai-backup. Both targets
// route to the same mockllm; sequencing is driven by the per-
// session canned-response pop introduced in PR-0.
const failoverPolicy = `
configurations:
  dev:
    upstream_credentials:
      openai: sk-dev-mock
      anthropic: sk-ant-dev-mock
      gemini: dev-mock
    rule_names:
      - enable-failover

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true

rules:
  - name: enable-failover
    condition:
      type: provider
      operator: Equals
      expectedProvider: openai
    actions:
      - type: useResiliencePolicy
        policyName: cross-provider-failover

resilience_policies:
  - name: cross-provider-failover
    mode: failover
    failure_status_codes: [503]
    targets:
      - name: openai-primary
        provider: openai
        order: 1
      - name: openai-backup
        provider: openai
        order: 2
`

// TestFailover_PrimaryReturns503_BackupServes200 — the headline
// acceptance case: client sees 200 even though the first upstream
// attempt returned 503.
func TestFailover_PrimaryReturns503_BackupServes200(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: failoverPolicy})
	sess := h.NewSession(t)

	sess.Stage(harness.CannedResponse{
		Method:       http.MethodPost,
		Path:         "/v1/chat/completions",
		Status:       http.StatusServiceUnavailable,
		Body:         `{"error":"upstream-down"}`,
		MaxResponses: 1,
	})
	sess.Stage(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Status: http.StatusOK,
		Body:   `{"id":"recovered","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"hi"}}]}`,
	})

	resp := sess.Post("/v1/chat/completions",
		map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("client status = %d; want 200 (recovered via failover). body=%s", resp.StatusCode, string(resp.Body))
	}

	captured := sess.Captured()
	if len(captured) != 2 {
		t.Errorf("upstream attempts = %d; want 2 (primary 503, backup 200)", len(captured))
	}
}

// TestFailover_AllTargetsReturn503_ClientSeesLastStatus — exhausted
// failover. Every staged upstream returns 503; the client sees the
// last attempt's status.
func TestFailover_AllTargetsReturn503_ClientSeesLastStatus(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: failoverPolicy})
	sess := h.NewSession(t)

	sess.Stage(harness.CannedResponse{
		Method:       http.MethodPost,
		Path:         "/v1/chat/completions",
		Status:       http.StatusServiceUnavailable,
		Body:         `{"error":"down-1"}`,
		MaxResponses: 1,
	})
	sess.Stage(harness.CannedResponse{
		Method:       http.MethodPost,
		Path:         "/v1/chat/completions",
		Status:       http.StatusServiceUnavailable,
		Body:         `{"error":"down-2"}`,
		MaxResponses: 1,
	})

	resp := sess.Post("/v1/chat/completions",
		map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("client status = %d; want 503 (every attempt failed, last status surfaced)", resp.StatusCode)
	}
	captured := sess.Captured()
	if len(captured) != 2 {
		t.Errorf("upstream attempts = %d; want 2", len(captured))
	}
}

// TestFailover_4xxNotInRetrySet_NotRetried — a 4xx from the primary
// (not in failure_status_codes) commits immediately. The backup is
// never attempted.
func TestFailover_4xxNotInRetrySet_NotRetried(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: failoverPolicy})
	sess := h.NewSession(t)

	sess.Stage(harness.CannedResponse{
		Method:       http.MethodPost,
		Path:         "/v1/chat/completions",
		Status:       http.StatusForbidden,
		Body:         `{"error":"denied"}`,
		MaxResponses: 1,
	})

	resp := sess.Post("/v1/chat/completions",
		map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("client status = %d; want 403 (not retryable, committed immediately)", resp.StatusCode)
	}
	captured := sess.Captured()
	if len(captured) != 1 {
		t.Errorf("upstream attempts = %d; want 1 (4xx not retried)", len(captured))
	}
}

// TestFailover_TransportErrorOnPrimary_BackupSucceeds — the
// mockllm "close" behaviour drops the connection without writing a
// status, simulating a transport-level failure. The orchestrator
// falls over to the backup.
func TestFailover_TransportErrorOnPrimary_BackupSucceeds(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: failoverPolicy})
	sess := h.NewSession(t)

	sess.Stage(harness.CannedResponse{
		Method:       http.MethodPost,
		Path:         "/v1/chat/completions",
		Behavior:     harness.BehaviorClose,
		MaxResponses: 1,
	})
	sess.Stage(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Status: http.StatusOK,
		Body:   `{"id":"recovered"}`,
	})

	resp := sess.Post("/v1/chat/completions",
		map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("client status = %d; want 200 (recovered after transport error). body=%s", resp.StatusCode, string(resp.Body))
	}
	captured := sess.Captured()
	if len(captured) != 2 {
		t.Errorf("upstream attempts = %d; want 2", len(captured))
	}
}

// TestFailover_NoPolicyRef_SingleShotUnchanged — sanity check that
// a request which the rules engine does NOT bind to a policy still
// runs single-shot, regression-free with respect to v1.1 behaviour.
func TestFailover_NoPolicyRef_SingleShotUnchanged(t *testing.T) {
	t.Parallel()
	// Same policy file but the rule won't fire because we send a
	// request that doesn't match its provider==openai condition —
	// route it via /v1/messages (anthropic) where the rule
	// condition fails.
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: failoverPolicy})
	sess := h.NewSession(t)

	sess.Stage(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/messages",
		Status: http.StatusServiceUnavailable,
		Body:   `{"error":"down"}`,
	})

	resp := sess.Post("/v1/messages",
		map[string]any{"model": "claude-3-5-sonnet", "messages": []map[string]string{{"role": "user", "content": "."}}, "max_tokens": 16},
		nil)

	// 503 should reach the client verbatim — no orchestration.
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("client status = %d; want 503 (no policy bound, single-shot)", resp.StatusCode)
	}
	if got := len(sess.Captured()); got != 1 {
		t.Errorf("upstream attempts = %d; want 1", got)
	}
}
