//go:build e2e

// Reverse arm of the cross-provider translation matrix: an OpenAI Chat client
// routed (via a translate rule) to an Anthropic Messages upstream. Each case
// stages the upstream's NATURAL Anthropic shape at the mock LLM, drives the real
// gateway binary, and asserts both legs — the captured upstream request decodes
// as Anthropic Messages, and the client response decodes as OpenAI Chat. This is
// differential, not golden: no hand-written "expected" blobs, only typed
// round-trip equality against the shape the mock actually emitted.
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

// reverseTranslatePolicy binds the OpenAI Chat protocol but attaches a
// translate->messages rule. config-dev/providers.yaml's anthropic provider
// serves both chat and messages, so the translate resolves anthropic's messages
// endpoint. The dev configuration name + tags block match the harness defaults.
const reverseTranslatePolicy = `
configurations:
  dev:
    credentials:
      anthropic: sk-dev-mock
    bindings:
      - { protocol: chat, models: ["claude-*"], provider: anthropic }
      - { protocol: messages, models: ["claude-*"], provider: anthropic }
    rule_names:
      - translate-to-messages
    tags:
      tier: dev

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true

rules:
  - name: translate-to-messages
    condition:
      type: protocol
      operator: Equals
      expectedProtocol: chat
    actions:
      - type: translate
        targetProtocol: messages
`

func newReverseTranslateHarness(t *testing.T) *harness.Harness {
	return harness.NewWithOptions(t, harness.Options{PolicyYAML: reverseTranslatePolicy})
}

// capturedMessagesRequest decodes the last request the mock LLM received as an
// Anthropic Messages request, asserting it landed on the messages endpoint.
func capturedMessagesRequest(t *testing.T, h *harness.Harness) messages.MessagesRequest {
	t.Helper()
	cap := h.LastCapturedRequest()
	if cap == nil {
		t.Fatal("mock LLM captured no upstream request")
	}
	if cap.Path != "/v1/messages" {
		t.Errorf("upstream path = %q, want /v1/messages (translate->messages)", cap.Path)
	}
	var req messages.MessagesRequest
	if err := json.Unmarshal([]byte(cap.Body), &req); err != nil {
		t.Fatalf("upstream body is not an Anthropic Messages request: %v\nbody: %s", err, cap.Body)
	}
	return req
}

func TestReverseTranslateE2E_NonStreamingRoundTrip(t *testing.T) {
	h := newReverseTranslateHarness(t)
	h.StageMockResponse(harness.CannedResponse{
		Method:  http.MethodPost,
		Path:    "/v1/messages",
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    `{"id":"msg_1","type":"message","role":"assistant","model":"claude-x","content":[{"type":"text","text":"hello from anthropic"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":3}}`,
	})

	body := map[string]any{
		"model":    "claude-x",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}
	resp := h.PostJSON("/v1/chat/completions", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, resp.Body)
	}

	// Upstream leg: the mock received an Anthropic Messages request.
	msgReq := capturedMessagesRequest(t, h)
	if msgReq.Model != "claude-x" {
		t.Errorf("upstream model = %q, want claude-x (carried verbatim)", msgReq.Model)
	}
	if len(msgReq.Messages) != 1 || msgReq.Messages[0].Role != "user" {
		t.Errorf("upstream messages = %+v, want one user message", msgReq.Messages)
	}
	if msgReq.MaxTokens == 0 {
		t.Error("upstream max_tokens = 0; Anthropic requires a positive value (default synthesised)")
	}

	// Client leg: the response decodes as OpenAI Chat.
	var chatResp openaichat.ChatCompletionResponse
	if err := json.Unmarshal(resp.Body, &chatResp); err != nil {
		t.Fatalf("client body is not OpenAI Chat: %v\nbody: %s", err, resp.Body)
	}
	if chatResp.Object != "chat.completion" {
		t.Errorf("client object = %q, want chat.completion", chatResp.Object)
	}
	if len(chatResp.Choices) != 1 {
		t.Fatalf("client choices = %d, want 1", len(chatResp.Choices))
	}
	c := chatResp.Choices[0]
	var content string
	if err := json.Unmarshal(c.Message.Content, &content); err != nil || content != "hello from anthropic" {
		t.Errorf("client content = %s (err %v), want 'hello from anthropic'", c.Message.Content, err)
	}
	if c.FinishReason == nil || *c.FinishReason != "stop" {
		t.Errorf("client finish_reason = %v, want stop", c.FinishReason)
	}
	if chatResp.Usage == nil || chatResp.Usage.PromptTokens != 5 || chatResp.Usage.CompletionTokens != 3 {
		t.Errorf("client usage = %+v, want 5/3", chatResp.Usage)
	}
}

func TestReverseTranslateE2E_ToolCallRoundTrip(t *testing.T) {
	h := newReverseTranslateHarness(t)
	h.StageMockResponse(harness.CannedResponse{
		Method:  http.MethodPost,
		Path:    "/v1/messages",
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    `{"id":"m1","type":"message","role":"assistant","model":"claude-x","content":[{"type":"tool_use","id":"tu_1","name":"get_weather","input":{"city":"SF"}}],"stop_reason":"tool_use","usage":{"input_tokens":3,"output_tokens":2}}`,
	})

	body := map[string]any{
		"model": "claude-x",
		"tools": []map[string]any{{
			"type": "function",
			"function": map[string]any{
				"name":        "get_weather",
				"description": "look up weather",
				"parameters":  map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}},
			},
		}},
		"messages": []map[string]any{{"role": "user", "content": "weather?"}},
	}
	resp := h.PostJSON("/v1/chat/completions", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, resp.Body)
	}

	// Upstream leg: the OpenAI function tool became an Anthropic tool.
	msgReq := capturedMessagesRequest(t, h)
	if len(msgReq.Tools) != 1 || msgReq.Tools[0].Name != "get_weather" {
		t.Errorf("upstream tools = %+v, want one get_weather tool", msgReq.Tools)
	}

	// Client leg: the Anthropic tool_use became an OpenAI tool_call.
	var chatResp openaichat.ChatCompletionResponse
	if err := json.Unmarshal(resp.Body, &chatResp); err != nil {
		t.Fatalf("client body is not OpenAI Chat: %v\nbody: %s", err, resp.Body)
	}
	c := chatResp.Choices[0]
	if c.FinishReason == nil || *c.FinishReason != "tool_calls" {
		t.Errorf("client finish_reason = %v, want tool_calls", c.FinishReason)
	}
	if len(c.Message.ToolCalls) != 1 {
		t.Fatalf("client tool_calls = %d, want 1", len(c.Message.ToolCalls))
	}
	tc := c.Message.ToolCalls[0]
	if tc.ID != "tu_1" || tc.Function.Name != "get_weather" {
		t.Errorf("tool_call = %q/%q, want tu_1/get_weather", tc.ID, tc.Function.Name)
	}
	var input map[string]string
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil || input["city"] != "SF" {
		t.Errorf("tool_call args = %q (err %v), want {city:SF}", tc.Function.Arguments, err)
	}
}

func TestReverseTranslateE2E_StreamingRoundTrip(t *testing.T) {
	h := newReverseTranslateHarness(t)
	h.StageMockResponse(harness.CannedResponse{
		Method:    http.MethodPost,
		Path:      "/v1/messages",
		Status:    http.StatusOK,
		Streaming: true,
		Headers:   map[string]string{"Content-Type": "text/event-stream"},
		StreamChunks: []harness.CannedStreamChunk{
			{Data: `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude-x","content":[],"usage":{"input_tokens":4,"output_tokens":0}}}`},
			{Data: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			{Data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi stream"}}`},
			{Data: `{"type":"content_block_stop","index":0}`},
			{Data: `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`},
			{Data: `{"type":"message_stop"}`},
		},
	})

	body := map[string]any{
		"model":    "claude-x",
		"stream":   true,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}
	stream := h.PostStream("/v1/chat/completions", body, nil)
	defer func() { _ = stream.Close() }()
	if stream.Status() != http.StatusOK {
		t.Fatalf("stream status = %d", stream.Status())
	}

	var text string
	var sawRole, sawDone bool
	for _, c := range stream.CollectAll(5 * time.Second) {
		if c.Data == "[DONE]" {
			sawDone = true
			continue
		}
		chunk := decodeChunk(t, c.Data)
		if len(chunk.Choices) == 1 {
			if chunk.Choices[0].Delta.Role == "assistant" {
				sawRole = true
			}
			text += chunkContent(chunk.Choices[0])
		}
	}
	if !sawRole {
		t.Error("client stream missing leading assistant role delta")
	}
	if text != "hi stream" {
		t.Errorf("client streamed text = %q, want 'hi stream'", text)
	}
	if !sawDone {
		t.Error("client stream missing [DONE] sentinel")
	}

	// Upstream leg: the mock received a streaming Anthropic Messages request.
	msgReq := capturedMessagesRequest(t, h)
	if !msgReq.Stream {
		t.Errorf("upstream request not streaming: %+v", msgReq)
	}
}

func TestReverseTranslateE2E_ErrorResponse(t *testing.T) {
	h := newReverseTranslateHarness(t)
	h.StageMockResponse(harness.CannedResponse{
		Method:  http.MethodPost,
		Path:    "/v1/messages",
		Status:  http.StatusTooManyRequests,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`,
	})

	resp := h.PostJSON("/v1/chat/completions", map[string]any{
		"model":    "claude-x",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}, nil)

	// Status preserved; body translated to the OpenAI error envelope.
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	var env struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body, &env); err != nil {
		t.Fatalf("client body is not an OpenAI error envelope: %v\nbody: %s", err, resp.Body)
	}
	if env.Error.Type != "rate_limit_error" || env.Error.Message != "slow down" {
		t.Errorf("error envelope = %+v, want rate_limit_error/'slow down'", env.Error)
	}
}

func TestReverseTranslateE2E_StreamNonStreamParity(t *testing.T) {
	h := newReverseTranslateHarness(t)

	// Non-streaming: full text in one response.
	h.StageMockResponse(harness.CannedResponse{
		Method:  http.MethodPost,
		Path:    "/v1/messages",
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    `{"id":"m","type":"message","role":"assistant","model":"claude-x","content":[{"type":"text","text":"the quick brown fox"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":4}}`,
	})
	resp := h.PostJSON("/v1/chat/completions", map[string]any{
		"model":    "claude-x",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}, nil)
	var nonStream openaichat.ChatCompletionResponse
	if err := json.Unmarshal(resp.Body, &nonStream); err != nil {
		t.Fatalf("non-stream body not OpenAI Chat: %v\n%s", err, resp.Body)
	}
	var nonStreamText string
	_ = json.Unmarshal(nonStream.Choices[0].Message.Content, &nonStreamText)

	// Streaming: the same text fragmented across deltas.
	h.ResetMockResponses()
	h.StageMockResponse(harness.CannedResponse{
		Method:    http.MethodPost,
		Path:      "/v1/messages",
		Status:    http.StatusOK,
		Streaming: true,
		Headers:   map[string]string{"Content-Type": "text/event-stream"},
		StreamChunks: []harness.CannedStreamChunk{
			{Data: `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude-x","content":[]}}`},
			{Data: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			{Data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"the quick "}}`},
			{Data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"brown fox"}}`},
			{Data: `{"type":"content_block_stop","index":0}`},
			{Data: `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`},
			{Data: `{"type":"message_stop"}`},
		},
	})
	stream := h.PostStream("/v1/chat/completions", map[string]any{
		"model": "claude-x", "stream": true,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}, nil)
	defer func() { _ = stream.Close() }()
	var streamText string
	for _, c := range stream.CollectAll(5 * time.Second) {
		if c.Data == "[DONE]" {
			continue
		}
		chunk := decodeChunk(t, c.Data)
		if len(chunk.Choices) == 1 {
			streamText += chunkContent(chunk.Choices[0])
		}
	}

	if streamText != nonStreamText {
		t.Errorf("stream/non-stream parity broken: stream=%q non-stream=%q", streamText, nonStreamText)
	}
}

// decodeChunk decodes one OpenAI SSE data payload as a streaming chunk.
func decodeChunk(t *testing.T, data string) openaichat.ChatCompletionChunk {
	t.Helper()
	var c openaichat.ChatCompletionChunk
	if err := json.Unmarshal([]byte(data), &c); err != nil {
		t.Fatalf("chunk %q not an OpenAI chunk: %v", data, err)
	}
	return c
}

// chunkContent extracts the text fragment from a streaming choice's content
// delta (empty when the delta carries no text).
func chunkContent(c openaichat.ChunkChoice) string {
	if len(c.Delta.Content) == 0 {
		return ""
	}
	var s string
	_ = json.Unmarshal(c.Delta.Content, &s)
	return s
}
