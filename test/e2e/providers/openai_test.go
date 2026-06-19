//go:build e2e

package providers_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

func TestOpenAI_ChatCompletions_NonStreaming(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	canned := `{"id":"chatcmpl-1","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Status: http.StatusOK,
		Body:   canned,
	})

	body := map[string]any{
		"model":    "gpt-4o-mini",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	resp := h.PostJSON("/v1/chat/completions", body, nil)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	if resp.Header.Get("X-Slipspace-Correlation-Id") == "" {
		t.Errorf("missing X-Slipspace-Correlation-Id")
	}
	var decoded map[string]any
	if err := resp.JSONUnmarshal(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded["object"] != "chat.completion" {
		t.Errorf("object=%v, want chat.completion", decoded["object"])
	}
}

func TestOpenAI_ChatCompletions_Streaming(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method:    http.MethodPost,
		Path:      "/v1/chat/completions",
		Streaming: true,
		Status:    http.StatusOK,
		Headers:   map[string]string{"Content-Type": "text/event-stream"},
		StreamChunks: []harness.CannedStreamChunk{
			{Data: `{"id":"c-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`},
			{Data: `{"id":"c-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`},
			{Data: `{"id":"c-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`},
			{Data: "[DONE]"},
		},
	})

	body := map[string]any{
		"model":    "gpt-4o-mini",
		"stream":   true,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	stream := h.PostStream("/v1/chat/completions", body, nil)
	chunks := stream.CollectAll(5 * time.Second)

	if got := stream.Status(); got != http.StatusOK {
		t.Fatalf("status=%d", got)
	}
	if len(chunks) < 4 {
		t.Fatalf("chunks=%d want >=4: %+v", len(chunks), chunks)
	}
	if !chunks[len(chunks)-1].IsDone() {
		t.Errorf("last chunk not [DONE]: %+v", chunks[len(chunks)-1])
	}
	for i := 0; i < len(chunks)-1; i++ {
		var msg map[string]any
		if err := json.Unmarshal([]byte(chunks[i].Data), &msg); err != nil {
			t.Errorf("chunk %d unparseable: %v data=%q", i, err, chunks[i].Data)
		}
	}
}

func TestOpenAI_Responses_NonStreaming(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	canned := `{"id":"resp-1","object":"response","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`
	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/responses",
		Body:   canned,
	})

	body := map[string]any{"model": "gpt-4o-mini", "input": "hi"}
	resp := h.PostJSON("/v1/responses", body, nil)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if !strings.Contains(string(resp.Body), `"resp-1"`) {
		t.Errorf("body missing response id: %s", resp.Body)
	}
}

func TestOpenAI_Responses_Streaming(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method:    http.MethodPost,
		Path:      "/v1/responses",
		Streaming: true,
		Headers:   map[string]string{"Content-Type": "text/event-stream"},
		StreamChunks: []harness.CannedStreamChunk{
			{Data: `{"type":"response.created","response":{"id":"r-1"}}`},
			{Data: `{"type":"response.output_text.delta","delta":"hi"}`},
			{Data: `{"type":"response.completed","response":{"id":"r-1"}}`},
		},
	})

	stream := h.PostStream("/v1/responses", map[string]any{"model": "gpt-4o-mini", "input": "hi", "stream": true}, nil)
	chunks := stream.CollectAll(5 * time.Second)
	if len(chunks) != 3 {
		t.Fatalf("chunks=%d want 3", len(chunks))
	}
}

func TestOpenAI_Models_List(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodGet,
		Path:   "/v1/models",
		Body:   `{"object":"list","data":[{"id":"gpt-4o-mini","object":"model"}]}`,
	})

	hdr := http.Header{}
	req, err := http.NewRequest(http.MethodGet, h.GatewayURL+"/v1/models", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.APIKey)
	for k, v := range hdr {
		req.Header[k] = v
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}
