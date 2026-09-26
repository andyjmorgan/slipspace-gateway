//go:build e2e

package rules_test

import (
	"net/http"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

// TestSetHeader_RemoveStripsInboundClientHeader proves, through the real
// binary, that setHeader Remove strips a header the CLIENT sent — not just a
// value an earlier rule wrote (issue #564). Before the fix the inbound
// X-Internal-Token was forwarded verbatim and the mock LLM observed it.
func TestSetHeader_RemoveStripsInboundClientHeader(t *testing.T) {
	t.Parallel()
	policy := matrixPolicy(`
  - name: strip-internal-token
    condition:
      type: provider
      operator: Equals
      expectedProvider: openai
    actions:
      - type: setHeader
        headerName: X-Internal-Token
        headerAction: Remove
`, "strip-internal-token")
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: policy})
	stageChatOK(h)

	hdr := http.Header{}
	hdr.Set("X-Internal-Token", "super-secret")
	hdr.Set("X-Keep-Me", "yes")
	fireChat(t, h, hdr)

	cap := h.LastCapturedRequest()
	if cap == nil {
		t.Fatal("no upstream request captured")
	}
	if got, ok := cap.Headers["X-Internal-Token"]; ok || got != "" {
		t.Fatalf("X-Internal-Token reached upstream (%q); setHeader Remove must strip the inbound header", got)
	}
	if got := cap.Headers["X-Keep-Me"]; got != "yes" {
		t.Fatalf("X-Keep-Me = %q, want yes (unrelated inbound headers still forward)", got)
	}
}

// TestSetHeader_RemoveThenSet_SetWins proves rule order composes: a later
// Set of the same header in the same request un-removes it and the set
// value reaches the upstream.
func TestSetHeader_RemoveThenSet_SetWins(t *testing.T) {
	t.Parallel()
	policy := matrixPolicy(`
  - name: strip-tier
    condition:
      type: provider
      operator: Equals
      expectedProvider: openai
    actions:
      - type: setHeader
        headerName: X-Tier
        headerAction: Remove
  - name: set-tier
    condition:
      type: provider
      operator: Equals
      expectedProvider: openai
    actions:
      - type: setHeader
        headerName: X-Tier
        headerAction: Set
        headerValue: gateway-assigned
`, "strip-tier", "set-tier")
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: policy})
	stageChatOK(h)

	hdr := http.Header{}
	hdr.Set("X-Tier", "client-supplied")
	fireChat(t, h, hdr)

	cap := h.LastCapturedRequest()
	if cap == nil {
		t.Fatal("no upstream request captured")
	}
	if got := cap.Headers["X-Tier"]; got != "gateway-assigned" {
		t.Fatalf("X-Tier = %q, want gateway-assigned (Set after Remove must win)", got)
	}
}
