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

func TestAuth_EndpointNotAllowed_UnknownConfig(t *testing.T) {
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
