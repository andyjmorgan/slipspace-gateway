//go:build e2e

package reporting_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
	"github.com/andyjmorgan/sluice-gateway/test/e2e/types"
)

func TestReporting_RequestEvent_Inline(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"chatcmpl-1","object":"chat.completion"}`,
	})

	const correlationID = "report-test-correlation"
	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt", "messages": []map[string]string{{"role": "user", "content": "."}}},
		http.Header{"X-Sluice-Correlation-Id": []string{correlationID}})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	env := h.ExpectEvent("gateway.request", 5*time.Second)
	if env.EventType != "request" {
		t.Errorf("EventType=%q want request", env.EventType)
	}
	if env.Mode != harness.PayloadInline {
		t.Errorf("Mode=%d want PayloadInline", env.Mode)
	}

	var ev types.RequestEvent
	if err := json.Unmarshal(env.InlinePayload, &ev); err != nil {
		t.Fatalf("decode payload: %v raw=%s", err, env.InlinePayload)
	}
	if ev.CorrelationID != correlationID {
		t.Errorf("CorrelationID=%q want %q", ev.CorrelationID, correlationID)
	}
	if ev.Provider != "openai" {
		t.Errorf("Provider=%q want openai", ev.Provider)
	}
	if ev.Endpoint != "chat_completions" {
		t.Errorf("Endpoint=%q want chat_completions", ev.Endpoint)
	}
	if ev.StatusCode != http.StatusOK {
		t.Errorf("StatusCode=%d want 200", ev.StatusCode)
	}
	if ev.DurationMs < 0 {
		t.Errorf("DurationMs negative: %d", ev.DurationMs)
	}
}

func TestReporting_Disabled_NoEvent(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{
		ReportingEnabled: harness.BoolPtr(false),
	})

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"x","object":"chat.completion"}`,
	})

	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt", "messages": []map[string]string{{"role": "user", "content": "."}}}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	h.ExpectNoEvent("gateway.request", 1500*time.Millisecond)
}

// TestReporting_Streaming_EventEmitted asserts a streaming request also
// produces a gateway.request envelope once the stream terminates, and that
// the Streaming bit is set on the RequestEvent payload.
func TestReporting_Streaming_EventEmitted(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method:    http.MethodPost,
		Path:      "/v1/chat/completions",
		Streaming: true,
		Headers:   map[string]string{"Content-Type": "text/event-stream"},
		StreamChunks: []harness.CannedStreamChunk{
			{Data: `{"choices":[{"delta":{"content":"hi"}}]}`},
			{Data: "[DONE]"},
		},
	})

	stream := h.PostStream("/v1/chat/completions",
		map[string]any{"model": "gpt", "stream": true, "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)
	_ = stream.CollectAll(5 * time.Second)

	env := h.ExpectEvent("gateway.request", 5*time.Second)
	var ev types.RequestEvent
	if err := json.Unmarshal(env.InlinePayload, &ev); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if !ev.Streaming {
		t.Errorf("Streaming=false, want true for SSE response")
	}
	if ev.Provider != "openai" || ev.Endpoint != "chat_completions" {
		t.Errorf("provider/endpoint=%q/%q", ev.Provider, ev.Endpoint)
	}
}
