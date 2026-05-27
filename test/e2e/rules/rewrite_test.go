//go:build e2e

package rules_test

import (
	"net/http"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

// rewritePolicy defines a `dev` configuration whose rules exercise the
// request-side body-rewrite primitive end-to-end:
//   - force-stream-usage: gated on bodyField stream==true, sets a nested
//     field, proving ensure-path creates the intermediate object.
//   - strip-user: removeField deletes a top-level key.
//   - tag-tracking: rewriteField sets an UNKNOWN field, proving the byte
//     patch reaches the wire alongside DynamicProperties round-trip.
//   - inject-system: appendField pushes onto the messages array.
//
// The harness hardcodes the `dev` configuration name and the dev api
// key, so this block stays compatible with those defaults while
// replacing the rule set.
const rewritePolicy = `
configurations:
  dev:
    upstream_credentials:
      openai: sk-dev-mock
    rule_names:
      - force-stream-usage
      - strip-user
      - tag-tracking
      - inject-system
    tags:
      tier: dev

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true

rules:
  - name: force-stream-usage
    condition:
      type: group
      logicalOperator: And
      children:
        - type: provider
          operator: Equals
          expectedProvider: openai
        - type: endpoint
          operator: Equals
          expectedEndpoint: chat_completions
        - type: bodyField
          target: request.body.stream
          operator: Equals
          value: "true"
    actions:
      - type: rewriteField
        target: request.body.stream_options.include_usage
        value: true
    behavior: continue

  - name: strip-user
    condition:
      type: provider
      operator: Equals
      expectedProvider: openai
    actions:
      - type: removeField
        target: request.body.user
    behavior: continue

  - name: tag-tracking
    condition:
      type: provider
      operator: Equals
      expectedProvider: openai
    actions:
      - type: rewriteField
        target: request.body.x_sluice_tracking
        value: "t1"
    behavior: continue

  - name: inject-system
    condition:
      type: provider
      operator: Equals
      expectedProvider: openai
    actions:
      - type: appendField
        target: request.body.messages
        value:
          role: system
          content: be good
    behavior: continue
`

// TestRewrite_RequestSide_Streaming proves a streaming request flows
// through the rewrite engine and the upstream sees every mutation: the
// nested stream_options created, the user key stripped, an unknown
// field set, and a system message appended.
func TestRewrite_RequestSide_Streaming(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: rewritePolicy})

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"chatcmpl-rw","object":"chat.completion"}`,
	})

	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{
			"model":    "gpt-4o-mini",
			"stream":   true,
			"user":     "alice",
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		},
		nil,
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	captured := h.LastCapturedRequest()
	if captured == nil {
		t.Fatal("no captured upstream request")
	}
	body := captured.Body

	if got := gjson.Get(body, "stream_options.include_usage"); !got.Exists() || !got.Bool() {
		t.Errorf("stream_options.include_usage = %q (exists=%v), want true; body=%s", got.String(), got.Exists(), body)
	}
	if gjson.Get(body, "user").Exists() {
		t.Errorf("user should have been removed; body=%s", body)
	}
	if got := gjson.Get(body, "x_sluice_tracking").String(); got != "t1" {
		t.Errorf("x_sluice_tracking = %q, want t1; body=%s", got, body)
	}
	msgs := gjson.Get(body, "messages").Array()
	if len(msgs) != 2 {
		t.Fatalf("messages len = %d, want 2 (one appended); body=%s", len(msgs), body)
	}
	if role := msgs[1].Get("role").String(); role != "system" {
		t.Errorf("appended message role = %q, want system; body=%s", role, body)
	}
}

// TestRewrite_RequestSide_NonStreaming proves the bodyField gate is
// honoured end-to-end: a non-streaming request does NOT get
// stream_options injected (the gate fails), while the ungated rules
// (strip-user, tag-tracking) still fire.
func TestRewrite_RequestSide_NonStreaming(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: rewritePolicy})

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"chatcmpl-rw2","object":"chat.completion"}`,
	})

	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{
			"model":    "gpt-4o-mini",
			"stream":   false,
			"user":     "bob",
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		},
		nil,
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	body := h.LastCapturedRequest().Body
	if gjson.Get(body, "stream_options").Exists() {
		t.Errorf("stream_options should NOT be set on a non-streaming request; body=%s", body)
	}
	if gjson.Get(body, "user").Exists() {
		t.Errorf("user should still be removed regardless of stream; body=%s", body)
	}
	if gjson.Get(body, "x_sluice_tracking").String() != "t1" {
		t.Errorf("tracking field missing; body=%s", body)
	}
}
