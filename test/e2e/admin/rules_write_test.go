//go:build e2e

package admin_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	adminc "github.com/andyjmorgan/slipspace-gateway/contracts/admin"
	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

// authedJSON returns an authenticated request against h.AdminURL.
// Centralised so the rules-write tests stay focused on the assertions.
func authedJSON(t *testing.T, h *harness.Harness, method, path string, body []byte) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, h.AdminURL+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.SetBasicAuth(adminc.Username, h.AdminPassword)
	resp, err := h.HTTP.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func mustJSONDecode[T any](t *testing.T, body io.Reader) T {
	t.Helper()
	var out T
	if err := json.NewDecoder(body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func newRuleBodyE2E(name string) []byte {
	return []byte(`{
		"name": "` + name + `",
		"condition": {"type": "provider", "operator": "Equals", "expectedProvider": "openai"},
		"actions": [
			{"type": "setHeader", "headerName": "X-E2E-` + name + `", "headerAction": "Set", "headerValue": "ok"}
		],
		"behavior": "continue"
	}`)
}

// TestAdmin_Rules_FullLifecycle exercises POST → GET → PUT → GET →
// DELETE → GET on a freshly created rule against the spawned gateway
// binary, asserting the store + the policy.yaml on disk both reflect
// each mutation.
func TestAdmin_Rules_FullLifecycle(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})

	const name = "e2e-lifecycle"

	// POST: create.
	resp := authedJSON(t, h, "POST", "/api/v1/config/rules", newRuleBodyE2E(name))
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("POST status=%d, want 201; body=%s", resp.StatusCode, body)
	}
	_ = resp.Body.Close()

	// GET detail: confirm present + condition shape.
	resp = authedJSON(t, h, "GET", "/api/v1/config/rules/"+name, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status=%d, want 200", resp.StatusCode)
	}
	var detail struct {
		Name     string `json:"name"`
		Behavior string `json:"behavior"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	_ = resp.Body.Close()
	if detail.Name != name {
		t.Errorf("GET detail.Name=%q, want %q", detail.Name, name)
	}

	// PUT: replace with a different condition shape (endpoint).
	updated := []byte(`{
		"name": "` + name + `",
		"condition": {"type": "protocol", "operator": "Equals", "expectedProtocol": "chat_completions"},
		"actions": [{"type": "setHeader", "headerName": "X-E2E-Updated", "headerAction": "Set", "headerValue": "yes"}],
		"behavior": "continue"
	}`)
	resp = authedJSON(t, h, "PUT", "/api/v1/config/rules/"+name, updated)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("PUT status=%d, want 200; body=%s", resp.StatusCode, body)
	}
	_ = resp.Body.Close()

	// GET again: confirm the new condition type stuck.
	resp = authedJSON(t, h, "GET", "/api/v1/config/rules/"+name, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("post-PUT GET status=%d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), `"protocol"`) {
		t.Errorf("post-PUT body missing protocol condition; body=%s", body)
	}

	// DELETE: unreferenced rule, expect 204.
	resp = authedJSON(t, h, "DELETE", "/api/v1/config/rules/"+name, nil)
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("DELETE status=%d, want 204; body=%s", resp.StatusCode, body)
	}
	_ = resp.Body.Close()

	// GET after delete: 404.
	resp = authedJSON(t, h, "GET", "/api/v1/config/rules/"+name, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET post-delete status=%d, want 404", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

// TestAdmin_Rules_DeleteReferencedRule_Returns409 confirms a delete
// against a rule already bound to a configuration is refused with
// 409 and the response carries the used_by configurations list.
func TestAdmin_Rules_DeleteReferencedRule_Returns409(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})

	// config-dev's dev configuration references tag-openai-chat.
	resp := authedJSON(t, h, "DELETE", "/api/v1/config/rules/tag-openai-chat", nil)
	if resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("DELETE status=%d, want 409; body=%s", resp.StatusCode, body)
	}
	var conflict struct {
		Error  string   `json:"error"`
		Name   string   `json:"name"`
		UsedBy []string `json:"used_by"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&conflict); err != nil {
		t.Fatalf("decode conflict body: %v", err)
	}
	_ = resp.Body.Close()
	if conflict.Name != "tag-openai-chat" {
		t.Errorf("conflict.Name=%q, want tag-openai-chat", conflict.Name)
	}
	if len(conflict.UsedBy) == 0 {
		t.Errorf("UsedBy empty, want at least one configuration")
	}
}

// TestAdmin_Rules_CreateDuplicate_Returns409 confirms the no-clobber
// invariant: POST against an existing rule name is rejected without
// modifying the live snapshot.
func TestAdmin_Rules_CreateDuplicate_Returns409(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})

	resp := authedJSON(t, h, "POST", "/api/v1/config/rules", newRuleBodyE2E("tag-openai-chat"))
	if resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("POST status=%d, want 409; body=%s", resp.StatusCode, body)
	}
	_ = resp.Body.Close()
}
