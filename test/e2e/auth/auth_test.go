//go:build e2e

package auth_test

import (
	"net/http"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

// TestAuth_Matrix walks the six auth modes against one representative
// endpoint (openai.chat_completions). Each row sends a single request with
// the headers the case requires and asserts the status code. The harness is
// shared so the mockllm registry only needs to be staged once.
//
// managed_disabled is represented by an absent Authorization header — the
// config-dev fixture does not ship a disabled key (one is covered by the
// in-process tests at cmd/gateway/gateway_test.go), so the closest
// black-box approximation is an unauthenticated managed request.
func TestAuth_Matrix(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"x","object":"chat.completion"}`,
	})

	body := map[string]any{
		"model":    "gpt",
		"messages": []map[string]string{{"role": "user", "content": "."}},
	}

	cases := []struct {
		name     string
		headers  http.Header
		wantCode int
	}{
		{
			name:     "managed_valid",
			headers:  http.Header{"Authorization": []string{"Bearer " + h.APIKey}},
			wantCode: http.StatusOK,
		},
		{
			name:     "managed_invalid",
			headers:  http.Header{"Authorization": []string{"Bearer sk_does_not_exist_at_all"}},
			wantCode: http.StatusUnauthorized,
		},
		{
			name:     "managed_no_bearer",
			headers:  http.Header{"Authorization": []string{""}},
			wantCode: http.StatusUnauthorized,
		},
		{
			// v1.0.7: native Anthropic-style header carries the Sluice
			// secret. Vanilla anthropic SDK pointed at sluice just works.
			// Empty Authorization overrides the harness's auto-injection
			// of a Bearer fallback so we exercise the x-api-key path
			// alone.
			name: "managed_via_x_api_key",
			headers: http.Header{
				"Authorization": []string{""},
				"X-Api-Key":     []string{h.APIKey},
			},
			wantCode: http.StatusOK,
		},
		{
			// v1.0.7: native Gemini-style header carries the Sluice secret.
			// Vanilla google-genai client pointed at sluice just works.
			name: "managed_via_x_goog_api_key",
			headers: http.Header{
				"Authorization":  []string{""},
				"X-Goog-Api-Key": []string{h.APIKey},
			},
			wantCode: http.StatusOK,
		},
		{
			// Authorization wins when present alongside native headers.
			name: "managed_authorization_wins_over_native",
			headers: http.Header{
				"Authorization": []string{"Bearer " + h.APIKey},
				"X-Api-Key":     []string{"sk_live_does_not_exist"},
			},
			wantCode: http.StatusOK,
		},
		{
			// Unknown secret on x-api-key does NOT fall through to
			// x-goog-api-key. Anti-stuffing guard.
			name: "managed_x_api_key_unknown_no_fallthrough",
			headers: http.Header{
				"Authorization":  []string{""},
				"X-Api-Key":      []string{"sk_live_does_not_exist"},
				"X-Goog-Api-Key": []string{h.APIKey},
			},
			wantCode: http.StatusUnauthorized,
		},
		{
			name: "passthrough_valid",
			headers: http.Header{
				"Authorization":          []string{"Bearer customer-supplied-token"},
				"X-Sluice-Configuration": []string{"dev"},
			},
			wantCode: http.StatusOK,
		},
		{
			name: "passthrough_unknown_config",
			headers: http.Header{
				"Authorization":          []string{"Bearer customer-supplied-token"},
				"X-Sluice-Configuration": []string{"does-not-exist"},
			},
			wantCode: http.StatusForbidden,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resp := h.PostJSON("/v1/chat/completions", body, tc.headers)
			if resp.StatusCode != tc.wantCode {
				t.Fatalf("status=%d want %d body=%s", resp.StatusCode, tc.wantCode, resp.Body)
			}
		})
	}
}

// TestAuth_Passthrough_PreservesXAPIKey covers the v1.0.7 load-bearing
// invariant: when X-Sluice-Configuration selects passthrough mode, the
// client's native API-key header (x-api-key carrying a real upstream
// Anthropic key, for example) MUST be forwarded verbatim and NOT consumed
// by sluice's managed-mode discovery. We assert by reading back the
// header the mockllm received.
func TestAuth_Passthrough_PreservesXAPIKey(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/messages",
		Body:   `{"id":"msg_pt","type":"message","role":"assistant","content":[],"model":"x","stop_reason":"end_turn"}`,
	})

	const customerKey = "sk-ant-customer-upstream-byok"
	body := map[string]any{"model": "x", "max_tokens": 1, "messages": []map[string]string{{"role": "user", "content": "."}}}
	resp := h.PostJSON("/anthropic/v1/messages", body, http.Header{
		"X-Api-Key":              []string{customerKey},
		"X-Sluice-Configuration": []string{"dev"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("passthrough w/ x-api-key: status=%d body=%s", resp.StatusCode, resp.Body)
	}

	cap := h.LastCapturedRequest()
	if cap == nil {
		t.Fatal("no upstream request captured")
	}
	if got := cap.Headers["X-Api-Key"]; got != customerKey {
		t.Fatalf("upstream x-api-key = %q, want passthrough verbatim %q", got, customerKey)
	}
	if cap.Headers["X-Sluice-Configuration"] != "" {
		t.Errorf("X-Sluice-Configuration must not leak upstream")
	}
}

func TestAuth_UnknownConfig(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	body := map[string]any{
		"contents": []map[string]any{{"role": "user", "parts": []map[string]string{{"text": "hi"}}}},
	}
	resp := h.PostJSON("/gemini/v1beta/models/gemini-1.5-flash:generateContent", body,
		http.Header{
			"Authorization":          []string{"Bearer customer-token"},
			"X-Sluice-Configuration": []string{"does-not-exist"},
		})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status=%d want 403", resp.StatusCode)
	}
}
