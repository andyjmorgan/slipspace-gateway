//go:build e2e

package errors_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

func TestErrors_Upstream4xx(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Status: http.StatusBadRequest,
		Body:   `{"error":{"message":"bad request","type":"invalid_request_error"}}`,
	})

	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "x", "messages": []map[string]string{{"role": "user", "content": "."}}}, nil)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", resp.StatusCode, resp.Body)
	}
	if !strings.Contains(string(resp.Body), "invalid_request_error") {
		t.Errorf("body missing upstream error: %s", resp.Body)
	}
}

func TestErrors_Upstream5xx(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Status: http.StatusBadGateway,
		Body:   `{"error":{"message":"upstream exploded"}}`,
	})

	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "x", "messages": []map[string]string{{"role": "user", "content": "."}}}, nil)

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status=%d want 502", resp.StatusCode)
	}
}

func TestErrors_MalformedUpstreamBody(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method:  http.MethodPost,
		Path:    "/v1/chat/completions",
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    `{this is :: not json`,
	})

	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "x", "messages": []map[string]string{{"role": "user", "content": "."}}}, nil)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200 (gateway forwards upstream byte-for-byte) body=%s", resp.StatusCode, resp.Body)
	}
	if string(resp.Body) != `{this is :: not json` {
		t.Errorf("body=%q, want verbatim upstream", resp.Body)
	}
}

func TestErrors_NoCannedResponse_UpstreamReturns404(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "x", "messages": []map[string]string{{"role": "user", "content": "."}}}, nil)

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d want 404 (mockllm 'no canned response')", resp.StatusCode)
	}
}

func TestErrors_UnknownRoute(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	resp := h.PostJSON("/does/not/exist", map[string]any{}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d want 404", resp.StatusCode)
	}
}

func TestErrors_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	req, err := http.NewRequest(http.MethodGet, h.GatewayURL+"/v1/chat/completions", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.APIKey)
	resp, err := h.HTTP.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d want 405", resp.StatusCode)
	}
}
