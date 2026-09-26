//go:build e2e

package providers_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

// These cases pin issue #409: a binding's `path` / `query` override and a
// group target's overrides, resolved by selection.Select, must survive the
// orchestrator's per-attempt state and the final handler's re-resolution
// (selection.ResolveTarget) so they reach the upstream wire. Before the fix
// the final handler re-resolved by provider only and the request landed on
// the provider's default /v1/chat/completions with no query string.
//
// The mock LLM serves /v1beta/openai/chat/completions (Gemini's compat path)
// alongside /v1/chat/completions, so pointing an openai binding at it is a
// path override the mock will actually answer.

const overridePath = "/v1beta/openai/chat/completions"

// overridesPolicy is a dev configuration whose gpt-* chat binding carries a
// path + query override, and whose grp-* chat binding routes to a failover
// group whose single target carries its own path + query override.
const overridesPolicy = `
groups:
  ha:
    mode: failover
    targets:
      - provider: openai
        path: ` + overridePath + `
        query:
          api-version: group-2025
          deployment: grp

configurations:
  dev:
    credentials:
      openai: sk-dev-mock
      anthropic: sk-ant-dev-mock
      gemini: dev-mock
    bindings:
      - protocol: chat
        models: ["gpt-*"]
        provider: openai
        path: ` + overridePath + `
        query:
          api-version: binding-2025
      - { protocol: chat, models: ["grp-*"], group: ha }
      - { protocol: chat, models: ["plain-*"], provider: openai }

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true
`

func stageOverrideOK(h *harness.Harness, id string) {
	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   overridePath,
		Status: http.StatusOK,
		Body:   `{"id":"` + id + `","object":"chat.completion","model":"x","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`,
	})
}

func postChat(t *testing.T, h *harness.Harness, model string) {
	t.Helper()
	body := map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	resp := h.PostJSON("/v1/chat/completions", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
}

func TestBinding_PathAndQueryOverride_ReachUpstream(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: overridesPolicy})
	stageOverrideOK(h, "chatcmpl-binding-override")

	postChat(t, h, "gpt-4o-mini")

	got := h.LastCapturedRequest()
	if got == nil {
		t.Fatal("no upstream capture")
	}
	if got.Path != overridePath {
		t.Errorf("upstream path = %q, want binding override %q", got.Path, overridePath)
	}
	q, err := url.ParseQuery(got.Query)
	if err != nil {
		t.Fatalf("parse upstream query %q: %v", got.Query, err)
	}
	if q.Get("api-version") != "binding-2025" {
		t.Errorf("upstream query = %q, want api-version=binding-2025 from the binding override", got.Query)
	}
	// Credential minting is unaffected by the transport override.
	if auth := got.Headers["Authorization"]; auth != "Bearer sk-dev-mock" {
		t.Errorf("upstream Authorization = %q, want openai's managed credential", auth)
	}
}

func TestGroupTarget_PathAndQueryOverride_ReachUpstream(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: overridesPolicy})
	stageOverrideOK(h, "chatcmpl-group-override")

	postChat(t, h, "grp-model")

	got := h.LastCapturedRequest()
	if got == nil {
		t.Fatal("no upstream capture")
	}
	if got.Path != overridePath {
		t.Errorf("upstream path = %q, want group target override %q", got.Path, overridePath)
	}
	q, err := url.ParseQuery(got.Query)
	if err != nil {
		t.Fatalf("parse upstream query %q: %v", got.Query, err)
	}
	if q.Get("api-version") != "group-2025" || q.Get("deployment") != "grp" {
		t.Errorf("upstream query = %q, want api-version=group-2025&deployment=grp from the group target", got.Query)
	}
}

// TestBinding_NoOverride_UsesProviderDefault guards the other direction: a
// binding without overrides in the same configuration still lands on the
// provider's protocol path with no query — the override plumbing must not
// bleed across bindings.
func TestBinding_NoOverride_UsesProviderDefault(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: overridesPolicy})
	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Status: http.StatusOK,
		Body:   `{"id":"chatcmpl-plain","object":"chat.completion","model":"x","choices":[]}`,
	})

	postChat(t, h, "plain-model")

	got := h.LastCapturedRequest()
	if got == nil {
		t.Fatal("no upstream capture")
	}
	if got.Path != "/v1/chat/completions" {
		t.Errorf("upstream path = %q, want provider default /v1/chat/completions", got.Path)
	}
	if got.Query != "" {
		t.Errorf("upstream query = %q, want empty", got.Query)
	}
}
