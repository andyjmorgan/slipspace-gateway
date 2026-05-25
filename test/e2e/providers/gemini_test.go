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

// TestGemini_GenerateContent_BareRoute_PrefixOptional covers v1.0.6:
// gemini.generate_content sets prefix_optional: true so a vanilla google-genai
// client pointed at the gateway root resolves /v1beta/models/{model}:
// generateContent without the /gemini prefix. The prefixed form is exercised
// by TestGemini_GenerateContent_NonStreaming_PrefixRouting.
func TestGemini_GenerateContent_BareRoute_PrefixOptional(t *testing.T) {
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
	resp := h.PostJSON("/v1beta/models/gemini-1.5-flash:generateContent", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bare gemini generate_content must route via prefix_optional; status=%d body=%s", resp.StatusCode, resp.Body)
	}
	if !strings.Contains(string(resp.Body), `"candidates"`) {
		t.Errorf("body missing candidates: %s", resp.Body)
	}
}

// TestGemini_GenerateContent_BareRoute_Streaming proves the bare-routed form
// preserves SSE end-to-end.
func TestGemini_GenerateContent_BareRoute_Streaming(t *testing.T) {
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
	stream := h.PostStream("/v1beta/models/gemini-1.5-flash:generateContent", body, nil)
	chunks := stream.CollectAll(5 * time.Second)
	if len(chunks) != 2 {
		t.Fatalf("chunks=%d want 2 (bare route)", len(chunks))
	}
}

// TestGemini_Models_PrefixOnly asserts gemini.models stays prefix-required:
// bare /v1beta/models must NOT route since the endpoint did not opt in to
// prefix_optional. Locks in the asymmetric design where only generate_content
// is bare-routable.
func TestGemini_Models_PrefixOnly(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	req, err := http.NewRequest(http.MethodGet, h.GatewayURL+"/v1beta/models", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.APIKey)
	resp, err := h.HTTP.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("bare /v1beta/models must not route; gemini.models is prefix-only (status=200)")
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

// TestGemini_StreamGenerateContent_FoldedEndpoint exercises the
// path-mirror fold: config-dev's gemini.generate_content lists both
// :generateContent and :streamGenerateContent in accepted_paths and
// omits the explicit Path so each matched URL forwards verbatim.
// Verifies (a) the streaming variant routes through the same endpoint
// key (so the accumulator dispatches) and (b) the upstream sees
// :streamGenerateContent (not :generateContent).
func TestGemini_StreamGenerateContent_FoldedEndpoint(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	upstreamPath := "/v1beta/models/gemini-1.5-flash:streamGenerateContent"
	h.StageMockResponse(harness.CannedResponse{
		Method:    http.MethodPost,
		Path:      upstreamPath,
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
	stream := h.PostStream("/gemini/v1beta/models/gemini-1.5-flash:streamGenerateContent", body, nil)
	if got := len(stream.CollectAll(5 * time.Second)); got != 2 {
		t.Fatalf("chunks=%d want 2", got)
	}

	captured := h.LastCapturedRequest()
	if captured == nil {
		t.Fatal("no upstream request captured")
	}
	if captured.Path != upstreamPath {
		t.Errorf("upstream path = %q, want %q (path-mirror must forward verbatim)", captured.Path, upstreamPath)
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
