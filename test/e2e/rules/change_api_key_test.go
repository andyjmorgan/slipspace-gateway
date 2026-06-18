//go:build e2e

package rules_test

import (
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

// TestChangeApiKey_LiteralOverride proves the wired changeApiKey action
// substitutes the literal upstream credential end-to-end through the binary.
// The dev configuration holds openai credential "sk-dev-mock"; a rule firing
// changeApiKey:{apiKey: sk-override} on the openai chat request must make the
// mock LLM observe Authorization: Bearer sk-override (openai's bearer format),
// never the managed default.
func TestChangeApiKey_LiteralOverride(t *testing.T) {
	t.Parallel()
	policy := matrixPolicy(`
  - name: override-openai-key
    condition:
      type: provider
      operator: Equals
      expectedProvider: openai
    actions:
      - type: changeApiKey
        apiKey: sk-override
`, "override-openai-key")
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: policy})
	stageChatOK(h)
	fireChat(t, h, nil)

	cap := h.LastCapturedRequest()
	if cap == nil {
		t.Fatal("no upstream request captured")
	}
	if got := cap.Headers["Authorization"]; got != "Bearer sk-override" {
		t.Fatalf("upstream Authorization = %q, want %q (changeApiKey literal override)", got, "Bearer sk-override")
	}
	if got := cap.Headers["Authorization"]; got == "Bearer sk-dev-mock" {
		t.Fatal("managed default credential leaked despite changeApiKey override")
	}
}

// TestChangeApiKey_UseSluiceKey proves the UseSluiceKey branch forwards the
// inbound Sluice bearer verbatim instead of substituting the managed upstream
// credential. The harness sends Authorization: Bearer <h.APIKey>; the rule sets
// changeApiKey:{useSluiceKey: true}, so the mock LLM must see that exact bearer
// rather than the dev configuration's openai credential.
func TestChangeApiKey_UseSluiceKey(t *testing.T) {
	t.Parallel()
	policy := matrixPolicy(`
  - name: use-sluice-key
    condition:
      type: provider
      operator: Equals
      expectedProvider: openai
    actions:
      - type: changeApiKey
        useSluiceKey: true
`, "use-sluice-key")
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: policy})
	stageChatOK(h)
	fireChat(t, h, nil)

	cap := h.LastCapturedRequest()
	if cap == nil {
		t.Fatal("no upstream request captured")
	}
	want := "Bearer " + h.APIKey
	if got := cap.Headers["Authorization"]; got != want {
		t.Fatalf("upstream Authorization = %q, want %q (UseSluiceKey forwards inbound bearer)", got, want)
	}
	if cap.Headers["Authorization"] == "Bearer sk-dev-mock" {
		t.Fatal("managed credential substituted despite UseSluiceKey")
	}
}
