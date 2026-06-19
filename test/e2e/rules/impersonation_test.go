//go:build e2e

package rules_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/contracts/events"
	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

// TestLlmImpersonation_E2E exercises the v1.0.1 stub: the rule
// short-circuits the pipeline and writes the rule's Message as a
// plain-text response. The upstream provider is never called.
//
// Full per-provider response-shape synthesisers (chat.completion /
// response / message / candidates × streaming) are deferred to
// v1.0.3+; this test confirms the stub is wired honestly — text
// body, X-Slipspace-Synthetic header, terminated=true on the rule
// event, gateway.request carrying the synthetic status.
func TestLlmImpersonation_E2E(t *testing.T) {
	t.Parallel()
	const message = "Blocked: this prompt looked like PII; please redact and retry."

	policy := matrixPolicy(`
  - name: pii-impersonator
    condition:
      type: provider
      operator: Equals
      expectedProvider: openai
    actions:
      - type: llmImpersonation
        message: `+"\""+message+"\""+`
`, "pii-impersonator")
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: policy})

	before := h.MockRequestCount()
	resp := h.PostJSON("/v1/chat/completions", chatBody(), nil)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/plain", got)
	}
	if got := resp.Header.Get("X-Slipspace-Synthetic"); got != "rule:pii-impersonator" {
		t.Errorf("X-Slipspace-Synthetic = %q", got)
	}
	if string(resp.Body) != message {
		t.Errorf("body = %q\nwant   %q", string(resp.Body), message)
	}

	// Upstream must not have been called — synthetic short-circuit.
	if got := h.MockRequestCount(); got != before {
		t.Errorf("MockRequestCount = %d, want %d (upstream should not be called)", got, before)
	}

	reqEnv := h.ExpectEvent("gateway.request", 5*time.Second)
	var reqEv events.Request
	if err := json.Unmarshal(reqEnv.InlinePayload, &reqEv); err != nil {
		t.Fatalf("decode request event: %v", err)
	}
	if reqEv.StatusCode != 200 {
		t.Errorf("gateway.request.status_code = %d, want 200 (synthetic)", reqEv.StatusCode)
	}

	ruleEnv := h.ExpectEvent("gateway.rule.matched", 5*time.Second)
	var ruleEv events.RuleMatched
	if err := json.Unmarshal(ruleEnv.InlinePayload, &ruleEv); err != nil {
		t.Fatalf("decode rule.matched: %v", err)
	}
	if ruleEv.RuleName != "pii-impersonator" || !ruleEv.Terminated {
		t.Errorf("rule.matched = %+v", ruleEv)
	}
	if len(ruleEv.ActionsApplied) != 1 || ruleEv.ActionsApplied[0] != "llmImpersonation" {
		t.Errorf("ActionsApplied = %v", ruleEv.ActionsApplied)
	}
}
