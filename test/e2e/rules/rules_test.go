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

// TestRules_MatchPublishesEvent fires a request that the config-dev
// `redact-emails` rule matches (provider=openai). It asserts the
// gateway publishes a gateway.rule.matched envelope carrying the
// expected rule_name + configuration, proving the engine wired
// end-to-end through the binary: routing → bodycapture → rules
// evaluator → MatchBuffer → reporter.OnComplete → NATS.
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

	env := h.ExpectEvent("gateway.rule.matched", 5*time.Second)

	var match events.RuleMatched
	if err := json.Unmarshal(env.InlinePayload, &match); err != nil {
		t.Fatalf("decode rule.matched payload: %v", err)
	}
	if match.RuleName != "redact-emails" {
		t.Errorf("RuleName = %q, want redact-emails", match.RuleName)
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

// TestRules_NoMatchEmitsNoEvent fires a request that none of the
// config-dev library rules match: anthropic provider (so the openai-only
// `redact-emails` skips), and a model name that starts with neither
// `claude-` nor `gemini-` (so the v1.0.2 `route-*-models-to-*` rules
// skip). Verifies the engine is selective — no gateway.rule.matched
// envelope arrives within a short window.
func TestRules_NoMatchEmitsNoEvent(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/messages",
		Body:   `{"id":"msg_x","type":"message","role":"assistant","model":"anthropic-internal","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":0,"output_tokens":0}}`,
	})

	resp := h.PostJSON("/anthropic/v1/messages",
		map[string]any{
			"model":      "anthropic-internal",
			"max_tokens": 64,
			"messages":   []map[string]string{{"role": "user", "content": "hi"}},
		},
		nil,
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	h.ExpectNoEvent("gateway.rule.matched", 750*time.Millisecond)
}
