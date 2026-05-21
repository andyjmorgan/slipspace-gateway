//go:build e2e

package harness_test

import (
	"net/http"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

// TestSession_PopSequenceThroughGateway proves the end-to-end session
// flow: client → gateway → mockllm — including X-Sluice-Session-Id
// being forwarded to the upstream and MaxResponses popping the staged
// response so subsequent calls hit the next entry. This is the
// foundation v1.2's resilience E2E tests will build on.
func TestSession_PopSequenceThroughGateway(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	sess := h.NewSession(t)

	sess.Stage(harness.CannedResponse{
		Method:       http.MethodPost,
		Path:         "/v1/chat/completions",
		Status:       http.StatusServiceUnavailable,
		Body:         `{"error":"down"}`,
		MaxResponses: 2,
	})
	sess.Stage(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Status: http.StatusOK,
		Body:   `{"id":"ok"}`,
	})

	statuses := make([]int, 0, 3)
	for i := 0; i < 3; i++ {
		resp := sess.Post("/v1/chat/completions",
			map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "."}}},
			nil)
		statuses = append(statuses, resp.StatusCode)
	}

	want := []int{http.StatusServiceUnavailable, http.StatusServiceUnavailable, http.StatusOK}
	for i, s := range statuses {
		if s != want[i] {
			t.Errorf("statuses[%d] = %d; want %d (full=%v)", i, s, want[i], statuses)
		}
	}

	captured := sess.Captured()
	if len(captured) != 3 {
		t.Fatalf("captured = %d requests; want 3", len(captured))
	}
	for i, c := range captured {
		if c.SessionID != sess.ID() {
			t.Errorf("captured[%d].SessionID = %q; want %q", i, c.SessionID, sess.ID())
		}
	}
}

// TestSession_IndependentScenarios proves two sessions inside one
// test run see independent pools and pop counters.
func TestSession_IndependentScenarios(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	alice := h.NewSession(t)
	bob := h.NewSession(t)

	alice.Stage(harness.CannedResponse{
		Method:       http.MethodPost,
		Path:         "/v1/chat/completions",
		Status:       http.StatusServiceUnavailable,
		Body:         `{"who":"alice-fail"}`,
		MaxResponses: 1,
	})
	bob.Stage(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Status: http.StatusOK,
		Body:   `{"who":"bob-ok"}`,
	})

	body := map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "."}}}

	a1 := alice.Post("/v1/chat/completions", body, nil)
	b1 := bob.Post("/v1/chat/completions", body, nil)

	if a1.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("alice first = %d; want 503", a1.StatusCode)
	}
	if b1.StatusCode != http.StatusOK {
		t.Errorf("bob first = %d; want 200", b1.StatusCode)
	}

	// Alice's 503 popped; her pool is now empty so the next call
	// must 404 (no fall-back to global — global is empty too).
	a2 := alice.Post("/v1/chat/completions", body, nil)
	if a2.StatusCode != http.StatusNotFound {
		t.Errorf("alice second = %d; want 404 (pool exhausted, no global fall-back)", a2.StatusCode)
	}

	// Bob's pool is unaffected by alice's pop.
	b2 := bob.Post("/v1/chat/completions", body, nil)
	if b2.StatusCode != http.StatusOK {
		t.Errorf("bob second = %d; want 200", b2.StatusCode)
	}

	if got := len(alice.Captured()); got != 2 {
		t.Errorf("alice captured = %d; want 2", got)
	}
	if got := len(bob.Captured()); got != 2 {
		t.Errorf("bob captured = %d; want 2", got)
	}
}
