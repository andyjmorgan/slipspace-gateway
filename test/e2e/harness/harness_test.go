//go:build e2e

package harness_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

func TestHarness_Smoke(t *testing.T) {
	t.Parallel()

	h := harness.New(t)

	req, err := http.NewRequest(http.MethodGet, h.GatewayURL+"/healthz", http.NoBody)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		t.Fatalf("get /healthz: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz status=%d, want 200", resp.StatusCode)
	}

	if !strings.HasPrefix(h.MockLLMURL, "http://127.0.0.1:") {
		t.Errorf("MockLLMURL=%q, want http://127.0.0.1: prefix", h.MockLLMURL)
	}
}
