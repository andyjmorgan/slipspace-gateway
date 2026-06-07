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
		map[string]any{"model": "gpt-4o", "messages": []map[string]string{{"role": "user", "content": "."}}},
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
	if ev.Protocol != "chat" {
		t.Errorf("Protocol=%q want chat", ev.Protocol)
	}
	if ev.Method != http.MethodPost {
		t.Errorf("Method=%q want POST", ev.Method)
	}
	if ev.Model != "gpt-4o" {
		t.Errorf("Model=%q want gpt", ev.Model)
	}
	if ev.StatusCode != http.StatusOK {
		t.Errorf("StatusCode=%d want 200", ev.StatusCode)
	}
	if ev.DurationMs < 0 {
		t.Errorf("DurationMs negative: %d", ev.DurationMs)
	}
}

// TestReporting_RequestEvent_AgentID proves a client-supplied agent id is
// resolved by the gateway and lands on the emitted connector Record (the
// audit/report channel), with its provenance header. Mirrors the session-id
// capture path.
func TestReporting_RequestEvent_AgentID(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"chatcmpl-1","object":"chat.completion"}`,
	})

	const agentID = "agent-report-7c3"
	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt-4o", "messages": []map[string]string{{"role": "user", "content": "."}}},
		http.Header{"X-Claude-Code-Agent-Id": []string{agentID}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	env := h.ExpectEvent("gateway.request", 5*time.Second)
	if env.Record == nil {
		t.Fatalf("expected a connector Record on the request envelope")
	}
	if env.Record.AgentID != agentID {
		t.Errorf("Record.AgentID=%q want %q", env.Record.AgentID, agentID)
	}
	if env.Record.AgentIDSource != "X-Claude-Code-Agent-Id" {
		t.Errorf("Record.AgentIDSource=%q want X-Claude-Code-Agent-Id", env.Record.AgentIDSource)
	}
}

// TestReporting_RequestEvent_UserID proves a client-supplied end-user id is
// resolved by the gateway and lands on the emitted connector Record, with its
// provenance header. There is no shipped client default for user id, so it is
// sent under the authoritative X-Sluice-User-Id. Mirrors the agent-id path.
func TestReporting_RequestEvent_UserID(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"chatcmpl-1","object":"chat.completion"}`,
	})

	const userID = "user-report-7c3"
	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt-4o", "messages": []map[string]string{{"role": "user", "content": "."}}},
		http.Header{"X-Sluice-User-Id": []string{userID}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	env := h.ExpectEvent("gateway.request", 5*time.Second)
	if env.Record == nil {
		t.Fatalf("expected a connector Record on the request envelope")
	}
	if env.Record.UserID != userID {
		t.Errorf("Record.UserID=%q want %q", env.Record.UserID, userID)
	}
	if env.Record.UserIDSource != "X-Sluice-User-Id" {
		t.Errorf("Record.UserIDSource=%q want X-Sluice-User-Id", env.Record.UserIDSource)
	}
}

// TestReporting_RequestEvent_CapturesGetMethod drives a GET /v1/models
// and asserts the captured record carries the real verb. Guards against
// the old hardcoded Method: "POST" — a model list must not masquerade as
// a completion in audit feeds.
func TestReporting_RequestEvent_CapturesGetMethod(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodGet,
		Path:   "/v1/models",
		Body:   `{"object":"list","data":[{"id":"gpt-4o-mini","object":"model"}]}`,
	})

	req, err := http.NewRequest(http.MethodGet, h.GatewayURL+"/v1/models", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.APIKey)
	resp, err := h.HTTP.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	env := h.ExpectEvent("gateway.request", 5*time.Second)
	var ev types.RequestEvent
	if err := json.Unmarshal(env.InlinePayload, &ev); err != nil {
		t.Fatalf("decode payload: %v raw=%s", err, env.InlinePayload)
	}
	if ev.Protocol != "models" {
		t.Errorf("Protocol=%q want models", ev.Protocol)
	}
	if ev.Method != http.MethodGet {
		t.Errorf("Method=%q want GET", ev.Method)
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
		map[string]any{"model": "gpt-4o", "messages": []map[string]string{{"role": "user", "content": "."}}}, nil)
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
		map[string]any{"model": "gpt-4o", "stream": true, "messages": []map[string]string{{"role": "user", "content": "."}}},
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
	if ev.Provider != "openai" || ev.Protocol != "chat" {
		t.Errorf("provider/endpoint=%q/%q", ev.Provider, ev.Protocol)
	}
}
