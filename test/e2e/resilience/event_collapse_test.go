//go:build e2e

package resilience_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
	"github.com/andyjmorgan/sluice-gateway/test/e2e/types"
)

// TestEventCollapse_MultiAttemptFailover_OneEventWithAttempts is the
// headline acceptance for PR-10a: a failover scenario that drives two
// upstream attempts must emit exactly ONE gateway.request event,
// carrying PolicyRef and a two-entry Attempts[] in declaration order.
// Pre-PR-10a the same scenario emitted two events (one per attempt).
func TestEventCollapse_MultiAttemptFailover_OneEventWithAttempts(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: failoverPolicy})
	sess := h.NewSession(t)

	sess.Stage(harness.CannedResponse{
		Method:       http.MethodPost,
		Path:         "/v1/chat/completions",
		Status:       http.StatusServiceUnavailable,
		Body:         `{"error":"upstream-down"}`,
		MaxResponses: 1,
	})
	sess.Stage(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Status: http.StatusOK,
		Body:   `{"id":"recovered","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"hi"}}]}`,
	})

	resp := sess.Post("/v1/chat/completions",
		map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("client status = %d; want 200. body=%s", resp.StatusCode, string(resp.Body))
	}

	env := h.ExpectEvent("gateway.request", 5*time.Second)

	var ev types.RequestEvent
	if err := json.Unmarshal(env.InlinePayload, &ev); err != nil {
		t.Fatalf("decode event: %v raw=%s", err, env.InlinePayload)
	}

	if ev.StatusCode != http.StatusOK {
		t.Errorf("event StatusCode = %d; want 200 (final client-observable status)", ev.StatusCode)
	}
	if ev.PolicyRef != "cross-provider-failover" {
		t.Errorf("event PolicyRef = %q; want cross-provider-failover", ev.PolicyRef)
	}

	if got := len(ev.Attempts); got != 2 {
		t.Fatalf("Attempts len = %d; want 2 (primary 503 → backup 200)", got)
	}

	if ev.Attempts[0].Target != "openai" {
		t.Errorf("Attempts[0].Target = %q; want openai", ev.Attempts[0].Target)
	}
	if ev.Attempts[0].StatusCode != http.StatusServiceUnavailable {
		t.Errorf("Attempts[0].StatusCode = %d; want 503", ev.Attempts[0].StatusCode)
	}
	if ev.Attempts[0].Outcome != "failure_status" {
		t.Errorf("Attempts[0].Outcome = %q; want failure_status", ev.Attempts[0].Outcome)
	}

	if ev.Attempts[1].Target != "anthropic" {
		t.Errorf("Attempts[1].Target = %q; want anthropic", ev.Attempts[1].Target)
	}
	if ev.Attempts[1].StatusCode != http.StatusOK {
		t.Errorf("Attempts[1].StatusCode = %d; want 200", ev.Attempts[1].StatusCode)
	}
	if ev.Attempts[1].Outcome != "success" {
		t.Errorf("Attempts[1].Outcome = %q; want success", ev.Attempts[1].Outcome)
	}

	// Durations are best-effort wall-clock measurements; on fast
	// loopback they can round to 0ms. Asserting >= 0 confirms the
	// fields are populated without imposing a machine-dependent
	// floor.
	if ev.Attempts[0].DurationMs < 0 || ev.Attempts[1].DurationMs < 0 {
		t.Errorf("Attempts durations negative: %d / %d", ev.Attempts[0].DurationMs, ev.Attempts[1].DurationMs)
	}
	if !ev.Attempts[0].StartedAt.Before(ev.Attempts[1].StartedAt) && !ev.Attempts[0].StartedAt.Equal(ev.Attempts[1].StartedAt) {
		t.Errorf("Attempts[0].StartedAt %v should be <= Attempts[1].StartedAt %v", ev.Attempts[0].StartedAt, ev.Attempts[1].StartedAt)
	}

	// No second event should arrive.
	h.ExpectNoEvent("gateway.request", 1500*time.Millisecond)
}

// TestEventCollapse_SingleShot_NoPolicyRef confirms the existing
// single-shot wire shape is unchanged: requests that the rules
// engine doesn't bind to a policy emit one event with no PolicyRef
// and no Attempts[].
func TestEventCollapse_SingleShot_NoPolicyRef(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: failoverPolicy})
	sess := h.NewSession(t)

	// Anthropic path — the failoverPolicy rule only matches openai.
	sess.Stage(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/messages",
		Status: http.StatusOK,
		Body:   `{"id":"x","content":[{"type":"text","text":"hi"}]}`,
	})

	resp := sess.Post("/v1/messages",
		map[string]any{"model": "claude-3-5-sonnet", "messages": []map[string]string{{"role": "user", "content": "."}}, "max_tokens": 16},
		nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("client status = %d; want 200", resp.StatusCode)
	}

	env := h.ExpectEvent("gateway.request", 5*time.Second)

	var ev types.RequestEvent
	if err := json.Unmarshal(env.InlinePayload, &ev); err != nil {
		t.Fatalf("decode event: %v", err)
	}

	if ev.PolicyRef != "" {
		t.Errorf("PolicyRef = %q; want empty for single-shot", ev.PolicyRef)
	}
	if len(ev.Attempts) != 0 {
		t.Errorf("Attempts len = %d; want 0 for single-shot", len(ev.Attempts))
	}

	h.ExpectNoEvent("gateway.request", 1500*time.Millisecond)
}
