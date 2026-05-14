//go:build e2e

package providers_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

func TestAnthropic_Messages_NonStreaming_PrefixRouting(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/messages",
		Body:   `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hello"}],"model":"claude-3-haiku-20240307","stop_reason":"end_turn"}`,
	})

	body := map[string]any{
		"model":      "claude-3-haiku-20240307",
		"max_tokens": 64,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	}
	resp := h.PostJSON("/anthropic/v1/messages", body, nil)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	if !strings.Contains(string(resp.Body), `"msg_1"`) {
		t.Errorf("response missing id: %s", resp.Body)
	}
}

func TestAnthropic_Messages_PrefixRequired(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/messages",
		Body:   `{"id":"msg_2"}`,
	})

	body := map[string]any{"model": "claude-3-haiku-20240307", "max_tokens": 1, "messages": []map[string]string{{"role": "user", "content": "hi"}}}
	resp := h.PostJSON("/v1/messages", body, nil)

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("anthropic without prefix should not route as anthropic; got 200 body=%s", resp.Body)
	}
}

func TestAnthropic_Messages_Streaming(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method:    http.MethodPost,
		Path:      "/v1/messages",
		Streaming: true,
		Headers:   map[string]string{"Content-Type": "text/event-stream"},
		StreamChunks: []harness.CannedStreamChunk{
			{Data: `{"type":"message_start","message":{"id":"msg_s","role":"assistant","content":[]}}`},
			{Data: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			{Data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`},
			{Data: `{"type":"message_stop"}`},
		},
	})

	body := map[string]any{
		"model":      "claude-3-haiku-20240307",
		"max_tokens": 64,
		"stream":     true,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	}
	stream := h.PostStream("/anthropic/v1/messages", body, nil)
	chunks := stream.CollectAll(5 * time.Second)
	if len(chunks) != 4 {
		t.Fatalf("chunks=%d want 4", len(chunks))
	}
}

func TestAnthropic_Models_PrefixRouting(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodGet,
		Path:   "/v1/models",
		Body:   `{"data":[{"id":"claude-3-haiku-20240307","type":"model"}]}`,
	})

	req, err := http.NewRequest(http.MethodGet, h.GatewayURL+"/anthropic/v1/models", http.NoBody)
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
