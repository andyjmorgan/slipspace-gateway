//go:build e2e

package rules_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/andyjmorgan/sluice-gateway/contracts/events"
	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

// returnStatusPolicy installs a single terminating rule on the dev
// configuration. The rule fires for every openai chat-completions
// request and short-circuits with a synthetic 429.
const returnStatusPolicy = `
configurations:
  dev:
    credentials:
      openai: sk-dev-mock
      anthropic: sk-ant-dev-mock
      gemini: dev-mock
    bindings:
      - { protocol: chat, models: ["gpt-*"], backend: openai }
    rule_names:
      - rate-limit-openai

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true

rules:
  - name: rate-limit-openai
    condition:
      type: provider
      operator: Equals
      expectedProvider: openai
    actions:
      - type: returnStatusCode
        statusCode: 429
        bodyType: json
        body: '{"error":"rate_limited","retry_after":60}'
    behavior: exit
`

// TestReturnStatusCode_E2E proves the full synthetic-response path
// end-to-end through the gateway binary: a terminating rule fires,
// the client receives the rule's body + Content-Type + status, the
// upstream mockllm is never called, and both gateway.request +
// gateway.rule.matched events arrive with the synthetic status and
// terminated=true.
func TestReturnStatusCode_E2E(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: returnStatusPolicy})

	before := h.MockRequestCount()

	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{
			"model":    "gpt-4o-mini",
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		},
		nil,
	)

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := resp.Header.Get("X-Sluice-Synthetic"); got != "rule:rate-limit-openai" {
		t.Errorf("X-Sluice-Synthetic = %q, want rule:rate-limit-openai", got)
	}
	if resp.Header.Get("X-Sluice-Correlation-Id") == "" {
		t.Error("X-Sluice-Correlation-Id should be set on synthetic response")
	}
	if string(resp.Body) != `{"error":"rate_limited","retry_after":60}` {
		t.Errorf("body = %q", resp.Body)
	}

	// Upstream must not have been called — synthetic short-circuit.
	if got := h.MockRequestCount(); got != before {
		t.Errorf("MockRequestCount = %d, want %d (upstream should not be called)", got, before)
	}

	reqEnv := h.ExpectEvent("gateway.request", 5*time.Second)
	var reqEv events.Request
	if err := json.Unmarshal(reqEnv.InlinePayload, &reqEv); err != nil {
		t.Fatalf("decode gateway.request: %v", err)
	}
	if reqEv.StatusCode != 429 {
		t.Errorf("gateway.request.status_code = %d, want 429 (synthetic)", reqEv.StatusCode)
	}
	if reqEv.Provider != "openai" || reqEv.Endpoint != "chat" {
		t.Errorf("gateway.request labels: provider=%q endpoint=%q", reqEv.Provider, reqEv.Endpoint)
	}
	if reqEv.Model != "gpt-4o-mini" {
		t.Errorf("gateway.request.model = %q", reqEv.Model)
	}

	ruleEnv := h.ExpectEvent("gateway.rule.matched", 5*time.Second)
	var ruleEv events.RuleMatched
	if err := json.Unmarshal(ruleEnv.InlinePayload, &ruleEv); err != nil {
		t.Fatalf("decode gateway.rule.matched: %v", err)
	}
	if ruleEv.RuleName != "rate-limit-openai" {
		t.Errorf("RuleName = %q", ruleEv.RuleName)
	}
	if !ruleEv.Terminated {
		t.Errorf("Terminated should be true for returnStatusCode")
	}
	if len(ruleEv.ActionsApplied) != 1 || ruleEv.ActionsApplied[0] != "returnStatusCode" {
		t.Errorf("ActionsApplied = %v, want [returnStatusCode]", ruleEv.ActionsApplied)
	}
}
