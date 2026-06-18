//go:build e2e

package admin_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	adminc "github.com/andyjmorgan/slipspace-gateway/contracts/admin"
	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

// failoverPolicyYAML matches the policy used by the resilience E2E
// suite — 2-target cross-provider-failover gated by openai provider.
const failoverPolicyYAML = `
configurations:
  dev:
    credentials:
      openai: sk-dev-mock
      anthropic: sk-ant-dev-mock
      gemini: dev-mock
    bindings:
      - { protocol: chat, models: ["gpt-*"], group: cross-provider-failover }

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true

groups:
  cross-provider-failover:
    mode: failover
    failure_status_codes: [503]
    targets:
      - { provider: openai }
      - { provider: anthropic }
`

// TestAdmin_Policies_ReturnsConfiguredPolicy hits /admin/api/v1/policies
// with valid credentials and asserts the wire shape matches the
// configured resilience policy.
func TestAdmin_Policies_ReturnsConfiguredPolicy(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{
		AdminEnabled: true,
		PolicyYAML:   failoverPolicyYAML,
	})

	req, _ := http.NewRequest(http.MethodGet, h.AdminURL+"/api/v1/policies", nil)
	req.SetBasicAuth(adminc.Username, h.AdminPassword)
	resp, err := h.HTTP.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/policies: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body adminc.PoliciesResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.Pod == "" {
		t.Errorf("Pod label empty")
	}
	if len(body.Policies) != 1 {
		t.Fatalf("policies = %d; want 1", len(body.Policies))
	}
	p := body.Policies[0]
	if p.Name != "cross-provider-failover" || p.Mode != "failover" {
		t.Errorf("policy = %+v; want cross-provider-failover/failover", p)
	}
	if len(p.Targets) != 2 {
		t.Fatalf("targets = %d; want 2", len(p.Targets))
	}
	for _, target := range p.Targets {
		if target.CircuitState != "closed" {
			t.Errorf("target %s state = %q; want closed (no CB activity yet)", target.Name, target.CircuitState)
		}
	}
}

// TestAdmin_MessagesRecent_CarriesAttemptsForMultiAttempt drives a
// 2-attempt failover, then fetches /api/v1/messages/recent and
// asserts the resulting MessageEntry carries PolicyRef + a
// 2-entry Attempts[]. Pre-PR-10b+c the entry had no attempt surface.
func TestAdmin_MessagesRecent_CarriesAttemptsForMultiAttempt(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{
		AdminEnabled: true,
		PolicyYAML:   failoverPolicyYAML,
	})
	sess := h.NewSession(t)

	sess.Stage(harness.CannedResponse{
		Method:       http.MethodPost,
		Path:         "/v1/chat/completions",
		Status:       http.StatusServiceUnavailable,
		Body:         `{"error":"down"}`,
		MaxResponses: 1,
	})
	sess.Stage(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Status: http.StatusOK,
		Body:   `{"id":"ok"}`,
	})

	resp := sess.Post("/v1/chat/completions",
		map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("client status = %d; want 200", resp.StatusCode)
	}

	// The reporter appends the live-feed entry from OnComplete on the server
	// goroutine after the response is written, so poll for it.
	var entry adminc.MessageEntry
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, h.AdminURL+"/api/v1/messages/recent", nil)
		req.SetBasicAuth(adminc.Username, h.AdminPassword)
		r, err := h.HTTP.Do(req)
		if err != nil {
			t.Fatalf("messages/recent: %v", err)
		}
		var got adminc.MessagesRecentResponse
		if derr := json.NewDecoder(r.Body).Decode(&got); derr != nil {
			_ = r.Body.Close()
			t.Fatalf("decode: %v", derr)
		}
		_ = r.Body.Close()
		for i := len(got.Entries) - 1; i >= 0; i-- {
			if got.Entries[i].Protocol == "chat" {
				entry = got.Entries[i]
				break
			}
		}
		if entry.EventID != "" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if entry.EventID == "" {
		t.Fatal("no chat entry appeared in messages/recent from the failover run")
	}

	if entry.PolicyRef != "cross-provider-failover" {
		t.Errorf("PolicyRef = %q; want cross-provider-failover", entry.PolicyRef)
	}
	if len(entry.Attempts) != 2 {
		t.Fatalf("Attempts len = %d; want 2", len(entry.Attempts))
	}
	if entry.Attempts[0].Target != "openai" || entry.Attempts[0].Outcome != "failure_status" {
		t.Errorf("attempt[0] = %+v; want openai/failure_status", entry.Attempts[0])
	}
	if entry.Attempts[1].Target != "anthropic" || entry.Attempts[1].Outcome != "success" {
		t.Errorf("attempt[1] = %+v; want anthropic/success", entry.Attempts[1])
	}
}
