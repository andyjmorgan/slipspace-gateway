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

// TestRules_MatchPublishesEvent fires a request that the config-dev
// `redact-emails` rule matches (provider=openai). It asserts the
// gateway emits a gateway.rule.matched envelope carrying the expected
// rule_name + configuration, proving the engine wired end-to-end
// through the binary: routing → bodycapture → rules evaluator →
// MatchBuffer → reporter.OnComplete → connector spool → harness
// envelope translation.
func TestRules_MatchPublishesEvent(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"chatcmpl-rule-test","object":"chat.completion"}`,
	})

	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{
			"model":    "gpt-4o-mini",
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		},
		nil,
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	// openai.chat_completions fires two rules in dev policy:
	// tag-openai-chat (addTag) and redact-emails (setHeader). Both
	// produce gateway.rule.matched envelopes; the harness fans them
	// out from a single Record so adjacent envelopes can arrive in
	// any order relative to wall-clock receive. Drain both and search
	// by rule_name rather than relying on receive order.
	var match events.RuleMatched
	found := false
	for i := 0; i < 2; i++ {
		env := h.ExpectEvent("gateway.rule.matched", 5*time.Second)
		var m events.RuleMatched
		if err := json.Unmarshal(env.InlinePayload, &m); err != nil {
			t.Fatalf("decode rule.matched payload: %v", err)
		}
		if m.RuleName == "redact-emails" {
			match = m
			found = true
		}
	}
	if !found {
		t.Fatalf("redact-emails rule.matched envelope not seen")
	}
	if match.Configuration != "dev" {
		t.Errorf("Configuration = %q, want dev", match.Configuration)
	}
	if len(match.ActionsApplied) != 1 || match.ActionsApplied[0] != "setHeader" {
		t.Errorf("ActionsApplied = %v, want [setHeader]", match.ActionsApplied)
	}
	if match.Terminated {
		t.Errorf("Terminated should be false for setHeader")
	}
	if match.CorrelationID == "" {
		t.Error("CorrelationID should be stamped by the reporter")
	}
}

// noRulesPolicy routes gpt-* chat to openai but attaches zero rules, so a
// request flows through the engine and matches nothing.
const noRulesPolicy = `
configurations:
  dev:
    credentials:
      openai: sk-dev-mock
    bindings:
      - { protocol: chat, models: ["gpt-*"], provider: openai }

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true
`

// TestRules_NoMatchEmitsNoEvent fires a request through a configuration with
// no rules attached. The request routes (gpt-* → openai) and returns 200, but
// the engine matches nothing — no gateway.rule.matched envelope arrives.
func TestRules_NoMatchEmitsNoEvent(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: noRulesPolicy})

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"chatcmpl-x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`,
	})

	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{
			"model":    "gpt-4o-mini",
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		},
		nil,
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	h.ExpectNoEvent("gateway.rule.matched", 750*time.Millisecond)
}
