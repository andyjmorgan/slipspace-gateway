//go:build e2e

package rules_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
	"github.com/andyjmorgan/slipspace-gateway/test/e2e/types"
)

// These cases reproduce GitHub issue #294 through the binary: a model bound
// to a two-provider resilience group, and a rule that rewrites the model AND
// switches the provider to a third one outside the group. Before the fix the
// orchestrator re-applied the group target's own provider switch every
// attempt, so the body model changed but the request still landed on a group
// member (which 404'd, then failed over). An explicit rule changeProvider now
// beats the binding-derived group: exactly one upstream attempt, on the rule's
// provider, with that provider's credential and path.
//
// Provider identity at the mock LLM: gemini's chat protocol path is
// /v1beta/openai/chat/completions (distinct from the openai/anthropic
// /v1/chat/completions the group members share) and its credential here is
// unique to this policy, so the captured request names the provider
// unambiguously.

const (
	precedenceGroup      = "qwen-load-balance"
	precedenceModel      = "qwen-coder-e2e"
	precedenceRewrite    = "oss-e2e-model"
	precedenceThirdCred  = "gem-third-provider-mock"
	precedenceGeminiPath = "/v1beta/openai/chat/completions"
	precedenceGroupPath  = "/v1/chat/completions"
)

// precedencePolicy binds qwen-coder-* to a load_balance group over openai +
// anthropic (the issue's shape, 404 in the failure set) and inlines the rule
// library under test. rulesYAML is the `rules:` list body.
func precedencePolicy(rulesYAML string, ruleNames ...string) string {
	names := ""
	for _, n := range ruleNames {
		names += "      - " + n + "\n"
	}
	return `
configurations:
  dev:
    credentials:
      openai: sk-dev-mock
      anthropic: sk-ant-dev-mock
      gemini: ` + precedenceThirdCred + `
    bindings:
      - { protocol: chat, models: ["qwen-coder-*"], group: ` + precedenceGroup + ` }
      - { protocol: chat, models: ["gpt-*"], provider: openai }
    rule_names:
` + names + `
api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true

groups:
  ` + precedenceGroup + `:
    mode: load_balance
    failure_status_codes: [502, 503, 504, 404]
    targets:
      - { provider: openai }
      - { provider: anthropic }

rules:
` + rulesYAML + `
`
}

// rewriteToThirdProviderRule is the issue's rule verbatim in shape:
// model Equals <bound model> → changeModelName + changeProvider to a provider
// that is NOT a member of the group.
const rewriteToThirdProviderRule = `
  - name: re-write-qwen-coder-to-oss
    condition:
      type: modelName
      operator: Equals
      expectedModelName: ` + precedenceModel + `
    actions:
      - type: changeModelName
        newModelName: ` + precedenceRewrite + `
      - type: changeProvider
        newProvider: gemini
`

func precedenceBody() map[string]any {
	return map[string]any{
		"model":    precedenceModel,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
}

func decodeRequestEvent(t *testing.T, h *harness.Harness) types.RequestEvent {
	t.Helper()
	env := h.ExpectEvent("gateway.request", 5*time.Second)
	var ev types.RequestEvent
	if err := json.Unmarshal(env.InlinePayload, &ev); err != nil {
		t.Fatalf("decode event: %v raw=%s", err, env.InlinePayload)
	}
	return ev
}

// TestChangeProvider_RuleBeatsGroupBinding is the headline acceptance for
// #294: the request lands on the rule's provider, once, with the rewritten
// model, and the group records no attempt.
func TestChangeProvider_RuleBeatsGroupBinding(t *testing.T) {
	t.Parallel()
	policy := precedencePolicy(rewriteToThirdProviderRule, "re-write-qwen-coder-to-oss")
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: policy})
	sess := h.NewSession(t)

	// Only the third provider's path is staged. Had the request reached a
	// group member on /v1/chat/completions the mock would have answered 404
	// — which is in the group's failure set and would have driven a
	// failover attempt, exactly the reported symptom.
	sess.Stage(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   precedenceGeminiPath,
		Status: http.StatusOK,
		Body:   `{"id":"chatcmpl-third","object":"chat.completion","model":"` + precedenceRewrite + `","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
	})

	resp := sess.Post(precedenceGroupPath, precedenceBody(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	if !strings.Contains(string(resp.Body), `"chatcmpl-third"`) {
		t.Errorf("response did not come from the third provider's canned body: %s", resp.Body)
	}

	captured := sess.Captured()
	if len(captured) != 1 {
		t.Fatalf("upstream attempts = %d; want exactly 1 (no failover through the group). captured=%+v", len(captured), captured)
	}
	got := captured[0]
	if got.Path != precedenceGeminiPath {
		t.Errorf("upstream path = %q; want %q (the rule's provider), not the group members' path", got.Path, precedenceGeminiPath)
	}
	if auth := got.Headers["Authorization"]; auth != "Bearer "+precedenceThirdCred {
		t.Errorf("upstream Authorization = %q; want the rule's provider credential %q", auth, "Bearer "+precedenceThirdCred)
	}
	if !strings.Contains(got.Body, `"model":"`+precedenceRewrite+`"`) {
		t.Errorf("upstream body model not rewritten to %q: %s", precedenceRewrite, got.Body)
	}

	ev := decodeRequestEvent(t, h)
	if ev.PolicyRef == precedenceGroup {
		t.Errorf("event PolicyRef = %q; the bypassed group must not be reported as the policy that ran", ev.PolicyRef)
	}
	for _, a := range ev.Attempts {
		if a.Target == "openai" || a.Target == "anthropic" {
			t.Errorf("event records an attempt on group member %q; want none", a.Target)
		}
	}
	if ev.Provider != "gemini" {
		t.Errorf("event Provider = %q; want gemini", ev.Provider)
	}
}

// TestChangeProvider_ModelRewriteOnlyStillRunsGroup pins the boundary: a
// rule that only rewrites the model leaves the group in charge, so a
// retryable first attempt still fails over to the second member.
func TestChangeProvider_ModelRewriteOnlyStillRunsGroup(t *testing.T) {
	t.Parallel()
	policy := precedencePolicy(`
  - name: re-write-qwen-coder-model-only
    condition:
      type: modelName
      operator: Equals
      expectedModelName: `+precedenceModel+`
    actions:
      - type: changeModelName
        newModelName: `+precedenceRewrite+`
`, "re-write-qwen-coder-model-only")
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: policy})
	sess := h.NewSession(t)

	sess.Stage(harness.CannedResponse{
		Method:       http.MethodPost,
		Path:         precedenceGroupPath,
		Status:       http.StatusServiceUnavailable,
		Body:         `{"error":"down"}`,
		MaxResponses: 1,
	})
	sess.Stage(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   precedenceGroupPath,
		Status: http.StatusOK,
		Body:   `{"id":"chatcmpl-group","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`,
	})

	resp := sess.Post(precedenceGroupPath, precedenceBody(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}

	captured := sess.Captured()
	if len(captured) != 2 {
		t.Fatalf("upstream attempts = %d; want 2 (503 then re-roll onto the other member)", len(captured))
	}
	for i, c := range captured {
		if c.Path != precedenceGroupPath {
			t.Errorf("attempt %d path = %q; want %q (a group member)", i, c.Path, precedenceGroupPath)
		}
		if !strings.Contains(c.Body, `"model":"`+precedenceRewrite+`"`) {
			t.Errorf("attempt %d body model not rewritten: %s", i, c.Body)
		}
	}

	ev := decodeRequestEvent(t, h)
	if ev.PolicyRef != precedenceGroup {
		t.Errorf("event PolicyRef = %q; want %q (the group ran)", ev.PolicyRef, precedenceGroup)
	}
	if len(ev.Attempts) != 2 {
		t.Errorf("event Attempts = %d; want 2", len(ev.Attempts))
	}
}

// TestChangeProvider_UndeclaredProviderKeepsExistingFailure pins the
// unchanged edge: a rule that switches to a provider the configuration holds
// no credential for still fails at selection.ResolveTarget in the final
// handler (500, no upstream call) — the precedence fix does not widen what a
// rule may route to.
func TestChangeProvider_UndeclaredProviderKeepsExistingFailure(t *testing.T) {
	t.Parallel()
	policy := precedencePolicy(`
  - name: re-write-qwen-coder-to-undeclared
    condition:
      type: modelName
      operator: Equals
      expectedModelName: `+precedenceModel+`
    actions:
      - type: changeProvider
        newProvider: qwen36
`, "re-write-qwen-coder-to-undeclared")
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: policy})
	sess := h.NewSession(t)

	sess.Stage(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   precedenceGroupPath,
		Status: http.StatusOK,
		Body:   `{"id":"never","object":"chat.completion"}`,
	})

	resp := sess.Post(precedenceGroupPath, precedenceBody(), nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d; want 500 (qwen36 exists in providers.yaml but the configuration holds no credential for it). body=%s", resp.StatusCode, resp.Body)
	}
	if captured := sess.Captured(); len(captured) != 0 {
		t.Errorf("upstream attempts = %d; want 0 (the group must not serve a request a rule routed elsewhere)", len(captured))
	}
}
