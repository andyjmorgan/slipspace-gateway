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

// TestRules_NoMatchEmitsNoEvent fires a request that none of the
// config-dev library rules match: gpt-oss provider (no provider/
// endpoint tag rule covers it under the dev configuration), model
// name that starts with neither `claude-` nor `gemini-` (so the
// route-* rules skip), and no x-sluice-tag-large header. Verifies
// the engine is selective — no gateway.rule.matched envelope arrives
// within a short window.
func TestRules_NoMatchEmitsNoEvent(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"chatcmpl-x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`,
	})

	resp := h.PostJSON("/gpt-oss/v1/chat/completions",
		map[string]any{
			"model":    "gpt-oss-internal",
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		},
		nil,
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	h.ExpectNoEvent("gateway.rule.matched", 750*time.Millisecond)
}
