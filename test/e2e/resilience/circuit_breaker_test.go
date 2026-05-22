//go:build e2e

package resilience_test

import (
	"net/http"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

// cbPolicyYAML wires a failover policy where the primary's per-target
// breaker trips after 2 failures. The backup is healthy. The breaker
// has a long cooldown so subsequent requests in the same test see
// the trip-and-skip behaviour without waiting for HalfOpen.
const cbPolicyYAML = `
configurations:
  dev:
    upstream_credentials:
      openai: sk-dev-mock
      anthropic: sk-ant-dev-mock
      gemini: dev-mock
    rule_names:
      - enable-cb

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true

rules:
  - name: enable-cb
    condition:
      type: provider
      operator: Equals
      expectedProvider: openai
    actions:
      - type: useResiliencePolicy
        policyName: cb-failover

resilience_policies:
  - name: cb-failover
    mode: failover
    failure_status_codes: [503]
    circuit_breaker:
      enabled: true
      failure_threshold: 2
      sampling_duration_seconds: 60
      cooldown_seconds: 3600
      half_open_success_threshold: 2
      minimum_throughput: 2
    targets:
      - name: openai-primary
        provider: openai
        order: 1
      - name: openai-backup
        provider: openai
        order: 2
`

// TestCircuitBreaker_TripsAndSkipsAfterFailures — fire 2 requests
// that both fail on the primary (failover absorbs to backup). After
// the breaker trips, a 3rd request must skip the primary entirely
// (one upstream attempt instead of two) but still succeed via the
// backup.
func TestCircuitBreaker_TripsAndSkipsAfterFailures(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: cbPolicyYAML})
	sess := h.NewSession(t)

	body := map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "."}}}

	// mockllm matches the FIRST entry in declaration order whose
	// predicates hold, so we interleave 503 + 200 to alternate
	// outcomes per call. Each request's primary attempt sees the
	// next 503 (consumed via MaxResponses); each backup attempt
	// sees the next 200.
	stage503 := func() {
		sess.Stage(harness.CannedResponse{
			Method:       http.MethodPost,
			Path:         "/v1/chat/completions",
			Status:       http.StatusServiceUnavailable,
			Body:         `{"error":"primary-down"}`,
			MaxResponses: 1,
		})
	}
	stage200 := func(maxR int) {
		sess.Stage(harness.CannedResponse{
			Method:       http.MethodPost,
			Path:         "/v1/chat/completions",
			Status:       http.StatusOK,
			Body:         `{"id":"backup-ok"}`,
			MaxResponses: maxR,
		})
	}

	// Request 1: 503 (primary, count=1) → 200 (backup).
	stage503()
	stage200(1)
	// Request 2: 503 (primary, count=2 → trip) → 200 (backup).
	stage503()
	stage200(1)
	// Request 3: backup-only (primary CB-blocked) → 200.
	stage200(1)

	// Request 1: primary 503 (count=1) → backup 200. 2 attempts.
	resp1 := sess.Post("/v1/chat/completions", body, nil)
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("request 1 status = %d; want 200", resp1.StatusCode)
	}

	// Request 2: primary 503 (count=2 → trip) → backup 200. 2 attempts.
	resp2 := sess.Post("/v1/chat/completions", body, nil)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("request 2 status = %d; want 200", resp2.StatusCode)
	}

	attemptsBefore := len(sess.Captured())
	if attemptsBefore != 4 {
		t.Errorf("attempts after 2 requests = %d; want 4 (2 primary fails + 2 backup successes)", attemptsBefore)
	}

	// Request 3: primary now CB-blocked → orchestrator skips
	// straight to backup. Only 1 upstream attempt this time.
	resp3 := sess.Post("/v1/chat/completions", body, nil)
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("request 3 status = %d; want 200 (backup serves)", resp3.StatusCode)
	}

	attemptsAfter := len(sess.Captured())
	delta := attemptsAfter - attemptsBefore
	if delta != 1 {
		t.Errorf("request 3 upstream attempts = %d; want 1 (primary CB-blocked)", delta)
	}
}
