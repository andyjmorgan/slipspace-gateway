//go:build e2e

package rules_test

import (
	"net/http"
	"os"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

// batchID is the id carried by the captured ended-batch fixture
// (test/fixtures/anthropic/messages_batch_ended.json).
const batchID = "msgbatch_01YPjqLcfEGCQPhw7jrj8awq"

const batchesRebasePolicy = `
configurations:
  dev:
    credentials:
      anthropic: sk-ant-dev-mock
    passthrough_bindings:
      - { family: messages_batches, provider: anthropic }
    rule_names:
      - rebase-batches-results-url
    tags:
      tier: dev

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true

rules:
  - name: rebase-batches-results-url
    condition:
      type: group
      logicalOperator: And
      children:
        - type: provider
          operator: Equals
          expectedProvider: anthropic
        - type: protocol
          operator: Equals
          expectedProtocol: messages_batches
    actions:
      - type: rewriteField
        target: response.body.results_url
        value: "{external_url}/anthropic/v1/messages/batches/{response.body.id}/results"
    behavior: continue
`

// TestRewrite_Batches_ResultsURLRebase drives the catalyst end-to-end:
// a real captured Anthropic ended-batch retrieve response (results_url
// pointing at api.anthropic.com) flows through the gateway, and the
// response-side rule rebases results_url to the gateway's external URL
// so a client following it comes back through Sluice — without
// bypassing telemetry/auth. Every other field of the real payload must
// round-trip untouched.
func TestRewrite_Batches_ResultsURLRebase(t *testing.T) {
	t.Parallel()

	fixture, err := os.ReadFile("../../fixtures/anthropic/messages_batch_ended.json")
	if err != nil {
		t.Fatalf("read batch fixture: %v", err)
	}

	h := harness.NewWithOptions(t, harness.Options{
		PolicyYAML:  batchesRebasePolicy,
		ExternalURL: "https://sluice.donkeywork.dev",
	})

	// The mock serves the captured ended-batch body for the upstream
	// retrieve path (gateway strips the /anthropic prefix and renders
	// the {batch_id} path template).
	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodGet,
		Path:   "/v1/messages/batches/" + batchID,
		Body:   string(fixture),
	})

	resp := h.Get("/v1/messages/batches/"+batchID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d body=%s", resp.StatusCode, resp.Body)
	}

	const wantURL = "https://sluice.donkeywork.dev/anthropic/v1/messages/batches/" + batchID + "/results"
	if got := gjson.GetBytes(resp.Body, "results_url").String(); got != wantURL {
		t.Errorf("results_url = %s, want %s; body=%s", got, wantURL, resp.Body)
	}

	// The rest of the real payload must survive byte-for-byte (the
	// rewrite is surgical — only results_url changes).
	if got := gjson.GetBytes(resp.Body, "id").String(); got != batchID {
		t.Errorf("id mutated: %s", got)
	}
	if got := gjson.GetBytes(resp.Body, "processing_status").String(); got != "ended" {
		t.Errorf("processing_status = %s, want ended", got)
	}
	if got := gjson.GetBytes(resp.Body, "request_counts.succeeded").Int(); got != 11 {
		t.Errorf("request_counts.succeeded = %d, want 11", got)
	}
	if !gjson.GetBytes(resp.Body, "archived_at").Exists() || gjson.GetBytes(resp.Body, "archived_at").Type != gjson.Null {
		t.Errorf("archived_at should round-trip as null; body=%s", resp.Body)
	}
}
