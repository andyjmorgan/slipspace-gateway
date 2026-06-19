//go:build e2e

package rules_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/contracts/events"
	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

// matrixPolicy builds a policy.yaml with the dev configuration's stock
// credentials and the supplied rules YAML inlined as the rules library.
// The "dev" configuration's rule_names list is overridden to ruleNames
// so each test exercises only its declared rules.
func matrixPolicy(rulesYAML string, ruleNames ...string) string {
	names := ""
	for _, n := range ruleNames {
		names += "      - " + n + "\n"
	}
	// v2: the dev configuration carries credentials + bindings (providers come
	// from config-dev/providers.yaml, which the harness copies alongside). The
	// gpt-* chat binding routes chatBody() to openai; the rule library under
	// test is inlined verbatim.
	return `
configurations:
  dev:
    credentials:
      openai: sk-dev-mock
      anthropic: sk-ant-dev-mock
      gemini: dev-mock
    bindings:
      - { protocol: chat, models: ["gpt-*"], provider: openai }
      - { protocol: responses, models: ["gpt-*"], provider: openai }
      - { protocol: chat, models: ["claude-*"], provider: anthropic }
    rule_names:
` + names + `
api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true

rules:
` + rulesYAML + `
`
}

// stageChatOK installs a canned 200 response on the upstream's
// chat-completions path. Tests that route a chat request through
// the gateway need this to avoid 404s from the mock LLM.
func stageChatOK(h *harness.Harness) {
	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"chatcmpl","object":"chat.completion"}`,
	})
}

func chatBody() map[string]any {
	return map[string]any{
		"model":    "gpt-4o-mini",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
}

func fireChat(t *testing.T, h *harness.Harness, extraHeaders http.Header) *harness.Response {
	t.Helper()
	resp := h.PostJSON("/v1/chat/completions", chatBody(), extraHeaders)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status = %d (body=%s)", resp.StatusCode, string(resp.Body))
	}
	return resp
}

// ─── Conditions ────────────────────────────────────────────────────

func TestConditions_ProtocolCondition_Matches(t *testing.T) {
	t.Parallel()
	policy := matrixPolicy(`
  - name: tag-on-chat
    condition:
      type: protocol
      operator: Equals
      expectedProtocol: chat
    actions:
      - type: setHeader
        headerName: X-Test-Endpoint-Cond
        headerAction: Set
        headerValue: matched
`, "tag-on-chat")
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: policy})
	stageChatOK(h)
	fireChat(t, h, nil)

	cap := h.LastCapturedRequest()
	if cap == nil || cap.Headers["X-Test-Endpoint-Cond"] != "matched" {
		t.Fatalf("ProtocolCondition did not fire; captured = %+v", cap)
	}
}

func TestConditions_ModelNameCondition_StartsWith(t *testing.T) {
	t.Parallel()
	policy := matrixPolicy(`
  - name: tag-on-gpt
    condition:
      type: modelName
      operator: StartsWith
      expectedModelName: gpt-
    actions:
      - type: setHeader
        headerName: X-Test-Model-Cond
        headerAction: Set
        headerValue: matched
`, "tag-on-gpt")
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: policy})
	stageChatOK(h)
	fireChat(t, h, nil)

	cap := h.LastCapturedRequest()
	if cap == nil || cap.Headers["X-Test-Model-Cond"] != "matched" {
		t.Fatalf("ModelNameCondition StartsWith did not fire; captured = %+v", cap)
	}
}

func TestConditions_HeaderCondition_Matches(t *testing.T) {
	t.Parallel()
	policy := matrixPolicy(`
  - name: tag-on-header
    condition:
      type: header
      keyOperator: Equals
      keyPattern: X-Tenant-Id
      valueOperator: Equals
      valuePattern: acme
    actions:
      - type: setHeader
        headerName: X-Test-Header-Cond
        headerAction: Set
        headerValue: matched
`, "tag-on-header")
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: policy})
	stageChatOK(h)
	hdr := http.Header{}
	hdr.Set("X-Tenant-Id", "acme")
	fireChat(t, h, hdr)

	cap := h.LastCapturedRequest()
	if cap == nil || cap.Headers["X-Test-Header-Cond"] != "matched" {
		t.Fatalf("HeaderCondition did not fire; captured = %+v", cap)
	}
}

func TestConditions_RuleGroup_AndMatches(t *testing.T) {
	t.Parallel()
	policy := matrixPolicy(`
  - name: tag-on-and
    condition:
      type: group
      logicalOperator: And
      children:
        - type: provider
          operator: Equals
          expectedProvider: openai
        - type: modelName
          operator: StartsWith
          expectedModelName: gpt-
    actions:
      - type: setHeader
        headerName: X-Test-Group-Cond
        headerAction: Set
        headerValue: matched
`, "tag-on-and")
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: policy})
	stageChatOK(h)
	fireChat(t, h, nil)

	cap := h.LastCapturedRequest()
	if cap == nil || cap.Headers["X-Test-Group-Cond"] != "matched" {
		t.Fatalf("RuleGroup AND did not fire; captured = %+v", cap)
	}
}

// ─── Non-terminating actions ───────────────────────────────────────

func TestActions_ChangeModelName_BodyRemarshalled(t *testing.T) {
	t.Parallel()
	policy := matrixPolicy(`
  - name: pin-gpt-4o
    condition:
      type: provider
      operator: Equals
      expectedProvider: openai
    actions:
      - type: changeModelName
        newModelName: gpt-4o
`, "pin-gpt-4o")
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: policy})
	stageChatOK(h)
	fireChat(t, h, nil)

	cap := h.LastCapturedRequest()
	if cap == nil {
		t.Fatal("upstream not called")
	}
	if !strings.Contains(cap.Body, `"model":"gpt-4o"`) {
		t.Errorf("re-marshalled body should carry new model; body = %s", cap.Body)
	}

	env := h.ExpectEvent("gateway.request", 5*time.Second)
	var ev events.Request
	if err := json.Unmarshal(env.InlinePayload, &ev); err != nil {
		t.Fatalf("decode request event: %v", err)
	}
	if ev.Model != "gpt-4o" {
		t.Errorf("gateway.request.model = %q, want gpt-4o", ev.Model)
	}
}

func TestActions_AppendQueryString_ExtendsQuery(t *testing.T) {
	t.Parallel()
	policy := matrixPolicy(`
  - name: tag-with-query
    condition:
      type: provider
      operator: Equals
      expectedProvider: openai
    actions:
      - type: appendQueryString
        key: trace
        value: slipspace-rules
`, "tag-with-query")
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: policy})
	stageChatOK(h)
	fireChat(t, h, nil)

	cap := h.LastCapturedRequest()
	if cap == nil {
		t.Fatal("upstream not called")
	}
	if !strings.Contains(cap.Query, "trace=slipspace-rules") {
		t.Errorf("upstream query missing appended param; got %q", cap.Query)
	}
}

// ─── Behavior + ordering ───────────────────────────────────────────

func TestRules_BehaviorExit_HaltsIteration(t *testing.T) {
	t.Parallel()
	policy := matrixPolicy(`
  - name: first-and-exit
    condition:
      type: provider
      operator: Equals
      expectedProvider: openai
    actions:
      - type: setHeader
        headerName: X-First
        headerAction: Set
        headerValue: yes
    behavior: exit
  - name: second-should-not-fire
    condition:
      type: provider
      operator: Equals
      expectedProvider: openai
    actions:
      - type: setHeader
        headerName: X-Second
        headerAction: Set
        headerValue: yes
`, "first-and-exit", "second-should-not-fire")
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: policy})
	stageChatOK(h)
	fireChat(t, h, nil)

	cap := h.LastCapturedRequest()
	if cap == nil {
		t.Fatal("upstream not called")
	}
	if cap.Headers["X-First"] != "yes" {
		t.Errorf("first rule did not fire; X-First = %q", cap.Headers["X-First"])
	}
	if got, present := cap.Headers["X-Second"]; present {
		t.Errorf("Behavior=Exit should have halted iteration; X-Second = %q", got)
	}

	// Only one gateway.rule.matched event should arrive (for the
	// first rule). Wait briefly for any second emission that should
	// not happen.
	first := h.ExpectEvent("gateway.rule.matched", 5*time.Second)
	var fm events.RuleMatched
	if err := json.Unmarshal(first.InlinePayload, &fm); err != nil {
		t.Fatalf("decode first rule.matched: %v", err)
	}
	if fm.RuleName != "first-and-exit" {
		t.Errorf("first rule.matched = %q, want first-and-exit", fm.RuleName)
	}
	h.ExpectNoEvent("gateway.rule.matched", 750*time.Millisecond)
}

func TestRules_ListOrderEvaluation(t *testing.T) {
	t.Parallel()
	// rule_names list position IS the evaluation order. The rules
	// library can declare them in any order; only the configuration's
	// rule_names sequence drives execution.
	policy := matrixPolicy(`
  - name: rule-c
    condition: {type: provider, operator: Equals, expectedProvider: openai}
    actions:
      - {type: setHeader, headerName: X-Order-C, headerAction: Set, headerValue: "3"}
  - name: rule-a
    condition: {type: provider, operator: Equals, expectedProvider: openai}
    actions:
      - {type: setHeader, headerName: X-Order-A, headerAction: Set, headerValue: "1"}
  - name: rule-b
    condition: {type: provider, operator: Equals, expectedProvider: openai}
    actions:
      - {type: setHeader, headerName: X-Order-B, headerAction: Set, headerValue: "2"}
`, "rule-a", "rule-b", "rule-c")
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: policy})
	stageChatOK(h)
	fireChat(t, h, nil)

	// Drain all three rule.matched events, then sort by MatchedAt
	// (set inside Evaluate, which runs sequentially per request) to
	// recover evaluation order. The harness fans each Record.RulesFired
	// entry out as a separate envelope, and receive order is not
	// guaranteed to match evaluation order; MatchedAt is the
	// authoritative per-event timestamp.
	want := []string{"rule-a", "rule-b", "rule-c"}
	matches := make([]events.RuleMatched, 0, len(want))
	for i := range want {
		env := h.ExpectEvent("gateway.rule.matched", 5*time.Second)
		var rm events.RuleMatched
		if err := json.Unmarshal(env.InlinePayload, &rm); err != nil {
			t.Fatalf("decode rule.matched[%d]: %v", i, err)
		}
		matches = append(matches, rm)
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].MatchedAt.Before(matches[j].MatchedAt)
	})
	for i, expect := range want {
		if matches[i].RuleName != expect {
			t.Errorf("matches[%d].RuleName = %q, want %q (sorted by MatchedAt)", i, matches[i].RuleName, expect)
		}
	}
}

// hard-coded sanity reference for fmt usage when test names get
// long enough to need formatted comparisons.
var _ = fmt.Sprintf
