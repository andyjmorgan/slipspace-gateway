//go:build e2e

// Package translate_test is the end-to-end differential matrix for
// cross-provider translation: an Anthropic Messages client routed (via a
// translate rule) to an OpenAI Chat upstream. Each case stages the upstream's
// NATURAL OpenAI shape at the mock LLM, drives the real gateway binary, and
// asserts both legs — the captured upstream request decodes as OpenAI Chat, and
// the client response decodes as Anthropic Messages. This is differential, not
// golden: there are no hand-written "expected" blobs, only typed round-trip
// equality against the shape the mock actually emitted.
package translate_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/andyjmorgan/sluice-gateway/protocols/anthropic/messages"
	openaichat "github.com/andyjmorgan/sluice-gateway/protocols/openai/chat"
	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

// translatePolicy binds the Anthropic Messages protocol but attaches a
// translate->chat rule. config-dev/providers.yaml's anthropic provider serves
// both messages and chat, so the translate resolves anthropic's chat endpoint.
// The dev configuration name + tags block match the harness defaults.
const translatePolicy = `
configurations:
  dev:
    credentials:
      anthropic: sk-dev-mock
    bindings:
      - { protocol: messages, models: ["claude-*"], provider: anthropic }
    rule_names:
      - translate-to-chat
    tags:
      tier: dev

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true

rules:
  - name: translate-to-chat
    condition:
      type: protocol
      operator: Equals
      expectedProtocol: messages
    actions:
      - type: translate
        targetProtocol: chat
`

func newTranslateHarness(t *testing.T) *harness.Harness {
	return harness.NewWithOptions(t, harness.Options{PolicyYAML: translatePolicy})
}

// capturedChatRequest decodes the last request the mock LLM received as an
// OpenAI Chat request, asserting it landed on the chat endpoint.
func capturedChatRequest(t *testing.T, h *harness.Harness) openaichat.ChatCompletionRequest {
	t.Helper()
	cap := h.LastCapturedRequest()
	if cap == nil {
		t.Fatal("mock LLM captured no upstream request")
	}
	if cap.Path != "/v1/chat/completions" {
		t.Errorf("upstream path = %q, want /v1/chat/completions (translate->chat)", cap.Path)
	}
	var req openaichat.ChatCompletionRequest
	if err := json.Unmarshal([]byte(cap.Body), &req); err != nil {
		t.Fatalf("upstream body is not an OpenAI Chat request: %v\nbody: %s", err, cap.Body)
	}
	return req
}

func TestTranslateE2E_NonStreamingRoundTrip(t *testing.T) {
	h := newTranslateHarness(t)
	h.StageMockResponse(harness.CannedResponse{
		Method:  http.MethodPost,
		Path:    "/v1/chat/completions",
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    `{"id":"chatcmpl-1","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"hello from openai"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`,
	})

	body := map[string]any{
		"model":      "claude-x",
		"max_tokens": 16,
		"messages":   []map[string]any{{"role": "user", "content": "hi"}},
	}
	resp := h.PostJSON("/v1/messages", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, resp.Body)
	}

	// Upstream leg: the mock received an OpenAI Chat request.
	chatReq := capturedChatRequest(t, h)
	if len(chatReq.Messages) != 1 || chatReq.Messages[0].Role() != "user" {
		t.Errorf("upstream messages = %+v, want one user message", chatReq.Messages)
	}
	if chatReq.Model != "claude-x" {
		t.Errorf("upstream model = %q, want claude-x (carried verbatim)", chatReq.Model)
	}

	// Client leg: the response decodes as Anthropic Messages.
	var msg messages.MessagesResponse
	if err := json.Unmarshal(resp.Body, &msg); err != nil {
		t.Fatalf("client body is not Anthropic Messages: %v\nbody: %s", err, resp.Body)
	}
	if msg.Type != "message" || msg.Role != "assistant" {
		t.Errorf("client envelope = %q/%q, want message/assistant", msg.Type, msg.Role)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("client content blocks = %d, want 1", len(msg.Content))
	}
	if tb, ok := msg.Content[0].(*messages.TextBlock); !ok || tb.Text != "hello from openai" {
		t.Errorf("client content[0] = %+v, want TextBlock 'hello from openai'", msg.Content[0])
	}
	if msg.StopReason == nil || *msg.StopReason != "end_turn" {
		t.Errorf("client stop_reason = %v, want end_turn", msg.StopReason)
	}
	if msg.Usage.InputTokens != 5 || msg.Usage.OutputTokens != 3 {
		t.Errorf("client usage = %d/%d, want 5/3", msg.Usage.InputTokens, msg.Usage.OutputTokens)
	}
}

func TestTranslateE2E_ToolCallRoundTrip(t *testing.T) {
	h := newTranslateHarness(t)
	h.StageMockResponse(harness.CannedResponse{
		Method:  http.MethodPost,
		Path:    "/v1/chat/completions",
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    `{"id":"c1","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
	})

	body := map[string]any{
		"model":      "claude-x",
		"max_tokens": 16,
		"tools": []map[string]any{{
			"name":         "get_weather",
			"description":  "look up weather",
			"input_schema": map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}},
		}},
		"messages": []map[string]any{{"role": "user", "content": "weather?"}},
	}
	resp := h.PostJSON("/v1/messages", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, resp.Body)
	}

	// Upstream leg: the Anthropic tool became an OpenAI function tool.
	chatReq := capturedChatRequest(t, h)
	if len(chatReq.Tools) != 1 || chatReq.Tools[0].Function == nil || chatReq.Tools[0].Function.Name != "get_weather" {
		t.Errorf("upstream tools = %+v, want one get_weather function tool", chatReq.Tools)
	}

	// Client leg: the OpenAI tool_call became an Anthropic tool_use block.
	var msg messages.MessagesResponse
	if err := json.Unmarshal(resp.Body, &msg); err != nil {
		t.Fatalf("client body is not Anthropic Messages: %v\nbody: %s", err, resp.Body)
	}
	if msg.StopReason == nil || *msg.StopReason != "tool_use" {
		t.Errorf("client stop_reason = %v, want tool_use", msg.StopReason)
	}
	var tu *messages.ToolUseBlock
	for _, b := range msg.Content {
		if t2, ok := b.(*messages.ToolUseBlock); ok {
			tu = t2
		}
	}
	if tu == nil {
		t.Fatalf("client content has no tool_use block: %+v", msg.Content)
	}
	if tu.ID != "call_1" || tu.Name != "get_weather" {
		t.Errorf("tool_use = %q/%q, want call_1/get_weather", tu.ID, tu.Name)
	}
	var input map[string]string
	if err := json.Unmarshal(tu.Input, &input); err != nil || input["city"] != "SF" {
		t.Errorf("tool_use input = %s (err %v), want {city:SF}", tu.Input, err)
	}
}

func TestTranslateE2E_StreamingRoundTrip(t *testing.T) {
	h := newTranslateHarness(t)
	h.StageMockResponse(harness.CannedResponse{
		Method:    http.MethodPost,
		Path:      "/v1/chat/completions",
		Status:    http.StatusOK,
		Streaming: true,
		Headers:   map[string]string{"Content-Type": "text/event-stream"},
		StreamChunks: []harness.CannedStreamChunk{
			{Data: `{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant"}}]}`},
			{Data: `{"choices":[{"index":0,"delta":{"content":"hi stream"}}]}`},
			{Data: `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`},
			{Data: "[DONE]"},
		},
	})

	body := map[string]any{
		"model":      "claude-x",
		"max_tokens": 16,
		"stream":     true,
		"messages":   []map[string]any{{"role": "user", "content": "hi"}},
	}
	stream := h.PostStream("/v1/messages", body, nil)
	defer func() { _ = stream.Close() }()
	if stream.Status() != http.StatusOK {
		t.Fatalf("stream status = %d", stream.Status())
	}

	chunks := stream.CollectAll(5 * time.Second)
	var gotTypes []string
	var sawText bool
	for _, c := range chunks {
		if c.Event != "" {
			gotTypes = append(gotTypes, c.Event)
		}
		if c.Event == "content_block_delta" && containsText(c.Data, "hi stream") {
			sawText = true
		}
	}
	want := []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if join(gotTypes) != join(want) {
		t.Errorf("client SSE event types = %v\nwant %v", gotTypes, want)
	}
	if !sawText {
		t.Errorf("client stream missing translated text delta 'hi stream': %+v", chunks)
	}

	// Upstream leg: the mock received a streaming OpenAI Chat request.
	chatReq := capturedChatRequest(t, h)
	if !chatReq.Stream {
		t.Errorf("upstream request not streaming: %+v", chatReq)
	}
}

func containsText(data, want string) bool {
	var d struct {
		Delta struct {
			Text string `json:"text"`
		} `json:"delta"`
	}
	_ = json.Unmarshal([]byte(data), &d)
	return d.Delta.Text == want
}

func join(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}
