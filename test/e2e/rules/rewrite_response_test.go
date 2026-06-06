//go:build e2e

package rules_test

import (
	"net/http"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

// responseRewritePolicy matches OpenAI chat completions at request time
// and queues a response-phase rewrite that rebases a URL field through
// the gateway's external URL — the same shape as the Anthropic batches
// results_url rebase, exercised on an endpoint that exists today. It
// proves the full response path: request-time match -> response-phase
// queue -> ModifyResponse hook -> client sees the rewritten body.
const responseRewritePolicy = `
configurations:
  dev:
    credentials:
      openai: sk-dev-mock
    bindings:
      - { protocol: chat, models: ["gpt-*"], provider: openai }
    rule_names:
      - rebase-result-url
    tags:
      tier: dev

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true

rules:
  - name: rebase-result-url
    condition:
      type: group
      logicalOperator: And
      children:
        - type: provider
          operator: Equals
          expectedProvider: openai
        - type: protocol
          operator: Equals
          expectedProtocol: chat
    actions:
      - type: rewriteField
        target: response.body.result_url
        value: "{external_url}/replay/{response.body.id}"
    behavior: continue
`

// TestRewrite_ResponseSide rebases a response field through the gateway
// external URL and asserts the client receives the rewritten body.
func TestRewrite_ResponseSide(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{
		PolicyYAML:  responseRewritePolicy,
		ExternalURL: "https://sluice.example.com",
	})

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"resp_abc","object":"chat.completion","result_url":"https://api.openai.com/internal/resp_abc"}`,
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

	// The client-visible body must carry the rebased URL — proving the
	// response-phase rewrite ran in ModifyResponse before the client
	// saw the bytes.
	const want = "https://sluice.example.com/replay/resp_abc"
	if got := gjson.GetBytes(resp.Body, "result_url").String(); got != want {
		t.Errorf("result_url = %s, want %s; body=%s", got, want, resp.Body)
	}
	// The untouched field round-trips intact.
	if got := gjson.GetBytes(resp.Body, "id").String(); got != "resp_abc" {
		t.Errorf("id = %s, want resp_abc", got)
	}
}
