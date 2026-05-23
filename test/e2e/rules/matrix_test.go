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

	"github.com/andyjmorgan/sluice-gateway/contracts/events"
	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
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
	return `
configurations:
  dev:
    upstream_credentials:
      openai: sk-dev-mock
      anthropic: sk-ant-dev-mock
      gemini: dev-mock
    rule_names:
` + names + `    resilience_name: high-availability

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true

rules:
` + rulesYAML + `
resilience_policies:
  - name: high-availability
    mode: failover
    timeout_seconds: 30
    targets:
      - name: openai-primary
        provider: openai
        order: 1
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

func TestConditions_EndpointCondition_Matches(t *testing.T) {
	t.Parallel()
	policy := matrixPolicy(`
  - name: tag-on-chat
    condition:
      type: endpoint
      operator: Equals
      expectedEndpoint: chat_completions
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
		t.Fatalf("EndpointCondition did not fire; captured = %+v", cap)
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

func TestActions_ChangeProvider_RetargetsUpstream(t *testing.T) {
	t.Parallel()
	// ChangeProvider on its own does NOT remap endpoint names — the
	// .NET predecessor's deliberate behaviour, which we match. With
	// v1.0.2 the destination builder re-resolves the endpoint on the
	// new provider after changeProvider fires, so when both providers
	// define the same endpoint name (here `chat_completions`), the
	// rewritten request picks up the destination provider's endpoint
	// — including its `auth_header`/`auth_format` override. Anthropic's
	// OpenAI-compat chat surface uses `Authorization: Bearer`, not the
	// native `x-api-key`, and that's what should land on the wire.
	policy := matrixPolicy(`
  - name: reroute-to-anthropic
    condition:
      type: provider
      operator: Equals
      expectedProvider: openai
    actions:
      - type: changeProvider
        newProvider: anthropic
      - type: changeUrl
        newUrl: http://mockllm:5555/v1/chat/completions
`, "reroute-to-anthropic")
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: policy})
	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"chatcmpl","object":"chat.completion"}`,
	})
	fireChat(t, h, nil)

	// The gateway.request event records the post-rule provider —
	// confirms changeProvider was honoured at destination time.
	env := h.ExpectEvent("gateway.request", 5*time.Second)
	var ev events.Request
	if err := json.Unmarshal(env.InlinePayload, &ev); err != nil {
		t.Fatalf("decode request event: %v", err)
	}
	if ev.Provider != "anthropic" {
		t.Errorf("post-rule provider = %q, want anthropic", ev.Provider)
	}

	// The upstream request lands on the rewritten path (changeUrl
	// target) with the configured anthropic credential minted into
	// the Authorization header. ChangeProvider does not remap the
	// endpoint name — the destination builder still resolves
	// anthropic.chat_completions, whose auth_header / auth_format
	// override (Authorization: Bearer {key}) is what fires here,
	// not anthropic's native x-api-key. Rules that need the native
	// messages auth must point at an endpoint without the override.
	cap := h.LastCapturedRequest()
	if cap == nil {
		t.Fatal("upstream not called")
	}
	if cap.Path != "/v1/chat/completions" {
		t.Errorf("upstream path = %q, want /v1/chat/completions", cap.Path)
	}
	if got := cap.Headers["Authorization"]; got != "Bearer sk-ant-dev-mock" {
		t.Errorf("Authorization header = %q, want Bearer sk-ant-dev-mock; got headers %+v", got, cap.Headers)
	}
	if got, present := cap.Headers["X-Api-Key"]; present {
		t.Errorf("native x-api-key should not be set when endpoint declares Authorization override; got %q", got)
	}
}

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

func TestActions_ChangeUrl_OverridesUpstream(t *testing.T) {
	t.Parallel()
	// Rewrite the upstream URL to the OpenAI /v1/responses endpoint
	// on the same mockllm host. The mockllm handles both paths, so
	// the success criterion is "captured request lands on the
	// rewritten path".
	policy := matrixPolicy(`
  - name: reroute-url
    condition:
      type: provider
      operator: Equals
      expectedProvider: openai
    actions:
      - type: changeUrl
        newUrl: http://mockllm:5555/v1/responses
`, "reroute-url")
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: policy})
	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/responses",
		Body:   `{"id":"resp","object":"response"}`,
	})
	fireChat(t, h, nil)

	cap := h.LastCapturedRequest()
	if cap == nil || cap.Path != "/v1/responses" {
		t.Fatalf("upstream path = %q, want /v1/responses", cap.Path)
	}
}

func TestActions_ChangeApiKey_OverridesCredential(t *testing.T) {
	t.Parallel()
	// changeApiKey writes the override credential; the destination
	// builder uses it in place of Configuration.UpstreamCredentials.
	policy := matrixPolicy(`
  - name: swap-key
    condition:
      type: provider
      operator: Equals
      expectedProvider: openai
    actions:
      - type: changeApiKey
        apiKey: sk-rule-injected
`, "swap-key")
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: policy})
	stageChatOK(h)
	fireChat(t, h, nil)

	cap := h.LastCapturedRequest()
	if cap == nil {
		t.Fatal("upstream not called")
	}
	if !strings.Contains(cap.Headers["Authorization"], "sk-rule-injected") {
		t.Errorf("Authorization header should carry rule-injected key; got %q", cap.Headers["Authorization"])
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
        value: sluice-rules
`, "tag-with-query")
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: policy})
	stageChatOK(h)
	fireChat(t, h, nil)

	cap := h.LastCapturedRequest()
	if cap == nil {
		t.Fatal("upstream not called")
	}
	if !strings.Contains(cap.Query, "trace=sluice-rules") {
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
