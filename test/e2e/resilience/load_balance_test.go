//go:build e2e

package resilience_test

import (
	"net/http"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

// loadBalancePolicy is the standard 50/50 load-balance config used
// by the E2E suite. Both targets resolve to the same mockllm; the
// session-scoped pop semantics from PR-0 drive the sequenced
// behaviour for each test.
const loadBalanceYAML = `
configurations:
  dev:
    credentials:
      openai: sk-dev-mock
      anthropic: sk-ant-dev-mock
      gemini: dev-mock
    bindings:
      - { protocol: chat, models: ["gpt-*"], group: weighted-pool }

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true

groups:
  weighted-pool:
    mode: load_balance
    failure_status_codes: [503]
    targets:
      - { provider: openai, weight: 50 }
      - { provider: anthropic, weight: 50 }
`

// TestLoadBalance_FirstAttemptCommits — a healthy target gets
// selected, returns 200, client sees 200. No retry. We do not pin
// which target was picked (real RNG); we only assert end-to-end
// commit.
func TestLoadBalance_FirstAttemptCommits(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: loadBalanceYAML})
	sess := h.NewSession(t)

	sess.Stage(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Status: http.StatusOK,
		Body:   `{"id":"ok"}`,
	})

	resp := sess.Post("/v1/chat/completions",
		map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d; want 200", resp.StatusCode)
	}
	if got := len(sess.Captured()); got != 1 {
		t.Errorf("attempts = %d; want 1 (no retry on healthy first attempt)", got)
	}
}

// TestLoadBalance_LBWF_RetryAbsorbsFailure — first selection
// returns 503, orchestrator re-rolls from the remaining pool and
// the second attempt succeeds. Client sees 200 across either
// permutation of target ordering.
func TestLoadBalance_LBWF_RetryAbsorbsFailure(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: loadBalanceYAML})
	sess := h.NewSession(t)

	sess.Stage(harness.CannedResponse{
		Method:       http.MethodPost,
		Path:         "/v1/chat/completions",
		Status:       http.StatusServiceUnavailable,
		Body:         `{"error":"down"}`,
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
		t.Errorf("status = %d; want 200 (LBWF absorbed 503). body=%s", resp.StatusCode, string(resp.Body))
	}
	if got := len(sess.Captured()); got != 2 {
		t.Errorf("attempts = %d; want 2", got)
	}
}

// TestLoadBalance_StrictWeights_NoReRoll — strict_weights disables
// the re-roll: the first selected target's failure surfaces to the
// client.
func TestLoadBalance_StrictWeights_NoReRoll(t *testing.T) {
	t.Parallel()

	strictYAML := `
configurations:
  dev:
    credentials:
      openai: sk-dev-mock
      anthropic: sk-ant-dev-mock
      gemini: dev-mock
    bindings:
      - { protocol: chat, models: ["gpt-*"], group: strict-pool }

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true

groups:
  strict-pool:
    mode: load_balance
    strict_weights: true
    failure_status_codes: [503]
    targets:
      - { provider: openai, weight: 50 }
      - { provider: anthropic, weight: 50 }
`
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: strictYAML})
	sess := h.NewSession(t)

	sess.Stage(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Status: http.StatusServiceUnavailable,
		Body:   `{"error":"down"}`,
	})

	resp := sess.Post("/v1/chat/completions",
		map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503 (strict_weights blocks re-roll)", resp.StatusCode)
	}
	if got := len(sess.Captured()); got != 1 {
		t.Errorf("attempts = %d; want 1 (strict_weights: no re-roll)", got)
	}
}

// TestLoadBalance_AllFail_SurfacesLastStatus — every selection
// returns 503. The orchestrator exhausts the pool and writes the
// last status to the client.
func TestLoadBalance_AllFail_SurfacesLastStatus(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: loadBalanceYAML})
	sess := h.NewSession(t)

	sess.Stage(harness.CannedResponse{
		Method:       http.MethodPost,
		Path:         "/v1/chat/completions",
		Status:       http.StatusServiceUnavailable,
		Body:         `{"error":"down"}`,
		MaxResponses: 1,
	})
	sess.Stage(harness.CannedResponse{
		Method:       http.MethodPost,
		Path:         "/v1/chat/completions",
		Status:       http.StatusServiceUnavailable,
		Body:         `{"error":"down"}`,
		MaxResponses: 1,
	})

	resp := sess.Post("/v1/chat/completions",
		map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503 (all-failed surfaces last status)", resp.StatusCode)
	}
	if got := len(sess.Captured()); got != 2 {
		t.Errorf("attempts = %d; want 2 (pool exhausted)", got)
	}
}
