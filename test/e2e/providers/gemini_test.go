//go:build e2e

package providers_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

func TestGemini_GenerateContent_NonStreaming_PrefixRouting(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1beta/models/gemini-1.5-flash:generateContent",
		Body:   `{"candidates":[{"content":{"parts":[{"text":"hi"}],"role":"model"},"finishReason":"STOP"}]}`,
	})

	body := map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]string{{"text": "hi"}}},
		},
	}
	resp := h.PostJSON("/gemini/v1beta/models/gemini-1.5-flash:generateContent", body, nil)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	if !strings.Contains(string(resp.Body), `"candidates"`) {
		t.Errorf("body missing candidates: %s", resp.Body)
	}
}

func TestGemini_GenerateContent_PrefixRequired(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	resp := h.PostJSON("/v1beta/models/gemini-1.5-flash:generateContent", map[string]any{}, nil)
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("gemini without prefix routed unexpectedly; status=200 body=%s", resp.Body)
	}
}

func TestGemini_GenerateContent_Streaming(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method:    http.MethodPost,
		Path:      "/v1beta/models/gemini-1.5-flash:generateContent",
		Streaming: true,
		Headers:   map[string]string{"Content-Type": "text/event-stream"},
		StreamChunks: []harness.CannedStreamChunk{
			{Data: `{"candidates":[{"content":{"parts":[{"text":"hi"}],"role":"model"}}]}`},
			{Data: `{"candidates":[{"content":{"parts":[{"text":"!"}],"role":"model"},"finishReason":"STOP"}]}`},
		},
	})

	body := map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]string{{"text": "hi"}}},
		},
	}
	stream := h.PostStream("/gemini/v1beta/models/gemini-1.5-flash:generateContent", body, nil)
	chunks := stream.CollectAll(5 * time.Second)
	if len(chunks) != 2 {
		t.Fatalf("chunks=%d want 2", len(chunks))
	}
}

func TestGemini_Models_PrefixRouting(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodGet,
		Path:   "/v1beta/models",
		Body:   `{"models":[{"name":"models/gemini-1.5-flash"}]}`,
	})

	req, err := http.NewRequest(http.MethodGet, h.GatewayURL+"/gemini/v1beta/models", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.APIKey)
	resp, err := h.HTTP.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}
