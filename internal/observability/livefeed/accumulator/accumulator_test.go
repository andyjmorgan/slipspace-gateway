package accumulator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/protocols/anthropic/messages"
	geminicontent "github.com/andyjmorgan/sluice-gateway/protocols/gemini/content"
	openaichat "github.com/andyjmorgan/sluice-gateway/protocols/openai/chat"
)

// parseOpenAI is a test helper that unmarshalls the assembled bytes
// back into the typed ChatCompletionResponse for assertion.
func parseOpenAI(t *testing.T, raw []byte) openaichat.ChatCompletionResponse {
	t.Helper()
	var got openaichat.ChatCompletionResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Assembled does not parse as ChatCompletionResponse: %v\nbytes: %s", err, raw)
	}
	return got
}

func parseAnthropic(t *testing.T, raw []byte) messages.MessagesResponse {
	t.Helper()
	var got messages.MessagesResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Assembled does not parse as MessagesResponse: %v\nbytes: %s", err, raw)
	}
	return got
}

func parseGemini(t *testing.T, raw []byte) geminicontent.GenerateContentResponse {
	t.Helper()
	var got geminicontent.GenerateContentResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Assembled does not parse as GenerateContentResponse: %v\nbytes: %s", err, raw)
	}
	return got
}

// openAIContent extracts the string content from choice[0] of the
// assembled response. The wire shape may be a bare string or null;
// tests use this as a shortcut.
func openAIContent(t *testing.T, resp openaichat.ChatCompletionResponse) string {
	t.Helper()
	if len(resp.Choices) == 0 {
		return ""
	}
	if len(resp.Choices[0].Message.Content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(resp.Choices[0].Message.Content, &s); err != nil {
		t.Fatalf("choice content not a string: %v (%s)", err, resp.Choices[0].Message.Content)
	}
	return s
}

func TestAccumulate_UnknownEndpointReturnsZeroResult(t *testing.T) {
	t.Parallel()
	got := Accumulate("openai", "models", []byte("data: {}\n\n"))
	if got.Recognised {
		t.Fatal("Recognised=true for unknown endpoint")
	}
	if len(got.Assembled) != 0 {
		t.Fatalf("expected empty Assembled, got %q", got.Assembled)
	}
}

func TestAccumulate_EmptyRawReturnsZero(t *testing.T) {
	t.Parallel()
	got := Accumulate("openai", "chat_completions", nil)
	if got.Recognised {
		t.Fatal("Recognised=true for empty raw")
	}
}

func TestParseSSE_BasicAndComments(t *testing.T) {
	t.Parallel()
	raw := []byte(": heartbeat\n" +
		"event: message\n" +
		"data: hello\n\n" +
		"data: world\n\n")
	got := parseSSE(raw)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2: %+v", len(got), got)
	}
	if got[0].Name != "message" || got[0].Data != "hello" {
		t.Errorf("frame 0 = %+v", got[0])
	}
	if got[1].Data != "world" {
		t.Errorf("frame 1 = %+v", got[1])
	}
}

func TestParseSSE_MultiLineData(t *testing.T) {
	t.Parallel()
	raw := []byte("data: line1\ndata: line2\n\n")
	got := parseSSE(raw)
	if len(got) != 1 {
		t.Fatalf("len=%d want 1", len(got))
	}
	if got[0].Data != "line1\nline2" {
		t.Errorf("Data=%q", got[0].Data)
	}
}

func TestAccumulate_OpenAIChat_ConcatsText(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"delta":{"role":"assistant","content":"Hello"},"index":0}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"content":", "},"index":0}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"content":"world!"},"index":0,"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]\n\n")
	got := Accumulate("openai", "chat_completions", raw)
	if !got.Recognised || got.Partial {
		t.Fatalf("got %+v", got)
	}
	resp := parseOpenAI(t, got.Assembled)
	if resp.ID != "chatcmpl-1" {
		t.Errorf("ID=%q", resp.ID)
	}
	if resp.Object != "chat.completion" {
		t.Errorf("Object=%q want chat.completion (rewritten from .chunk)", resp.Object)
	}
	if resp.Model != "gpt-4o" {
		t.Errorf("Model=%q", resp.Model)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("Choices=%d want 1", len(resp.Choices))
	}
	if openAIContent(t, resp) != "Hello, world!" {
		t.Errorf("content=%q", openAIContent(t, resp))
	}
	if resp.Choices[0].Message.Role != "assistant" {
		t.Errorf("Role=%q", resp.Choices[0].Message.Role)
	}
	if resp.Choices[0].FinishReason == nil || *resp.Choices[0].FinishReason != "stop" {
		t.Errorf("FinishReason=%v", resp.Choices[0].FinishReason)
	}
}

func TestAccumulate_OpenAIChat_AssemblesToolCalls(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]},"index":0}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]},"index":0}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Paris\"}"}}]},"index":0,"finish_reason":"tool_calls"}]}` + "\n\n" +
		"data: [DONE]\n\n")
	got := Accumulate("openai", "chat_completions", raw)
	if !got.Recognised || got.Partial {
		t.Fatalf("got %+v", got)
	}
	resp := parseOpenAI(t, got.Assembled)
	if len(resp.Choices) != 1 {
		t.Fatalf("Choices=%d", len(resp.Choices))
	}
	tools := resp.Choices[0].Message.ToolCalls
	if len(tools) != 1 {
		t.Fatalf("ToolCalls=%+v", tools)
	}
	tc := tools[0]
	if tc.ID != "call_1" || tc.Type != "function" {
		t.Errorf("ID/Type = %q/%q", tc.ID, tc.Type)
	}
	if tc.Function.Name != "get_weather" {
		t.Errorf("Name=%q", tc.Function.Name)
	}
	if tc.Function.Arguments != `{"city":"Paris"}` {
		t.Errorf("Arguments=%q", tc.Function.Arguments)
	}
}

func TestAccumulate_OpenAIChat_UsageFromTerminalChunk(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"choices":[{"delta":{"content":"hi"},"index":0}]}` + "\n\n" +
		`data: {"choices":[{"delta":{},"index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}` + "\n\n" +
		"data: [DONE]\n\n")
	got := Accumulate("openai", "chat_completions", raw)
	resp := parseOpenAI(t, got.Assembled)
	if resp.Usage == nil {
		t.Fatal("Usage=nil")
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 2 || resp.Usage.TotalTokens != 12 {
		t.Errorf("Usage=%+v", resp.Usage)
	}
}

func TestAccumulate_OpenAIChat_PartialOnMalformedChunk(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"choices":[{"delta":{"content":"Hello"},"index":0}]}` + "\n\n" +
		"data: not valid json\n\n" +
		`data: {"choices":[{"delta":{"content":" world"},"index":0}]}` + "\n\n")
	got := Accumulate("openai", "chat_completions", raw)
	if !got.Partial {
		t.Error("Partial=false despite malformed chunk")
	}
	resp := parseOpenAI(t, got.Assembled)
	if openAIContent(t, resp) != "Hello world" {
		t.Errorf("content=%q (should parse around bad chunk)", openAIContent(t, resp))
	}
}

func TestAccumulate_OpenAIChat_HonoursDONE(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"choices":[{"delta":{"content":"Hello"},"index":0}]}` + "\n\n" +
		"data: [DONE]\n\n" +
		`data: {"choices":[{"delta":{"content":" SHOULD NOT APPEAR"},"index":0}]}` + "\n\n")
	got := Accumulate("openai", "chat_completions", raw)
	resp := parseOpenAI(t, got.Assembled)
	if openAIContent(t, resp) != "Hello" {
		t.Errorf("content=%q want 'Hello' (anything after [DONE] should be ignored)", openAIContent(t, resp))
	}
}

func TestAccumulate_OpenAIChat_ContentAsContentParts(t *testing.T) {
	t.Parallel()
	raw := []byte(`data: {"choices":[{"delta":{"content":[{"type":"text","text":"part-one "},{"type":"text","text":"part-two"}]},"index":0}]}` + "\n\n")
	got := Accumulate("openai", "chat_completions", raw)
	resp := parseOpenAI(t, got.Assembled)
	if openAIContent(t, resp) != "part-one part-two" {
		t.Errorf("content=%q", openAIContent(t, resp))
	}
}

func TestAccumulate_OpenAIChat_MalformedContentSilentlySkipped(t *testing.T) {
	t.Parallel()
	raw := []byte(`data: {"choices":[{"delta":{"content":42},"index":0}]}` + "\n\n")
	got := Accumulate("openai", "chat_completions", raw)
	resp := parseOpenAI(t, got.Assembled)
	if openAIContent(t, resp) != "" {
		t.Errorf("content=%q want empty", openAIContent(t, resp))
	}
}

func TestAccumulate_AnthropicMessages_ConcatsTextBlocks(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`event: message_start` + "\n" +
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-3-5-sonnet","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":0}}}` + "\n\n" +
		`event: content_block_start` + "\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		`event: content_block_delta` + "\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}` + "\n\n" +
		`event: content_block_delta` + "\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":", world!"}}` + "\n\n" +
		`event: content_block_stop` + "\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		`event: message_delta` + "\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":10,"output_tokens":3}}` + "\n\n" +
		`event: message_stop` + "\n" +
		`data: {"type":"message_stop"}` + "\n\n")
	got := Accumulate("anthropic", "messages", raw)
	if !got.Recognised || got.Partial {
		t.Fatalf("got %+v", got)
	}
	resp := parseAnthropic(t, got.Assembled)
	if resp.ID != "msg_1" || resp.Type != "message" || resp.Role != "assistant" {
		t.Errorf("envelope = %+v", resp)
	}
	if resp.Model != "claude-3-5-sonnet" {
		t.Errorf("Model=%q", resp.Model)
	}
	if resp.StopReason == nil || *resp.StopReason != "end_turn" {
		t.Errorf("StopReason=%v", resp.StopReason)
	}
	if resp.Usage.OutputTokens != 3 {
		t.Errorf("OutputTokens=%d want 3 (from message_delta)", resp.Usage.OutputTokens)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("Content=%d want 1", len(resp.Content))
	}
	tb, ok := resp.Content[0].(*messages.TextBlock)
	if !ok {
		t.Fatalf("block[0] = %T", resp.Content[0])
	}
	if tb.Text != "Hello, world!" {
		t.Errorf("text=%q", tb.Text)
	}
}

func TestAccumulate_AnthropicMessages_ReassemblesThinking(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}` + "\n\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me ","estimated_tokens":10}}` + "\n\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"reason."}}` + "\n\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"EpkECmMID"}}` + "\n\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}` + "\n\n" +
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"The answer."}}` + "\n\n")
	got := Accumulate("anthropic", "messages", raw)
	if !got.Recognised {
		t.Fatalf("got %+v", got)
	}
	resp := parseAnthropic(t, got.Assembled)
	if len(resp.Content) != 2 {
		t.Fatalf("Content=%d want 2", len(resp.Content))
	}
	tb, ok := resp.Content[0].(*messages.ThinkingBlock)
	if !ok {
		t.Fatalf("block[0] = %T want *ThinkingBlock", resp.Content[0])
	}
	if tb.Thinking != "Let me reason." {
		t.Errorf("thinking=%q", tb.Thinking)
	}
	if tb.Signature != "EpkECmMID" {
		t.Errorf("signature=%q", tb.Signature)
	}
	txt, ok := resp.Content[1].(*messages.TextBlock)
	if !ok || txt.Text != "The answer." {
		t.Fatalf("block[1] = %T %+v", resp.Content[1], resp.Content[1])
	}
}

func TestAccumulate_AnthropicMessages_InterleavedThinkTextThink(t *testing.T) {
	t.Parallel()
	// think, text, think again — each index must stay its own ordered block.
	raw := []byte("" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}` + "\n\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"first"}}` + "\n\n" +
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}` + "\n\n" +
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"mid"}}` + "\n\n" +
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"thinking","thinking":"","signature":""}}` + "\n\n" +
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"thinking_delta","thinking":"second"}}` + "\n\n" +
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"signature_delta","signature":"sig2"}}` + "\n\n")
	resp := parseAnthropic(t, Accumulate("anthropic", "messages", raw).Assembled)
	if len(resp.Content) != 3 {
		t.Fatalf("Content=%d want 3", len(resp.Content))
	}
	t0, _ := resp.Content[0].(*messages.ThinkingBlock)
	t1, _ := resp.Content[1].(*messages.TextBlock)
	t2, _ := resp.Content[2].(*messages.ThinkingBlock)
	if t0 == nil || t0.Thinking != "first" {
		t.Errorf("block[0]=%+v", resp.Content[0])
	}
	if t1 == nil || t1.Text != "mid" {
		t.Errorf("block[1]=%+v", resp.Content[1])
	}
	if t2 == nil || t2.Thinking != "second" || t2.Signature != "sig2" {
		t.Errorf("block[2]=%+v", resp.Content[2])
	}
}

func TestAccumulate_AnthropicMessages_AssemblesToolUse(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`event: content_block_start` + "\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{}}}` + "\n\n" +
		`event: content_block_delta` + "\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}` + "\n\n" +
		`event: content_block_delta` + "\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"Paris\"}"}}` + "\n\n")
	got := Accumulate("anthropic", "messages", raw)
	resp := parseAnthropic(t, got.Assembled)
	if len(resp.Content) != 1 {
		t.Fatalf("Content=%d", len(resp.Content))
	}
	tu, ok := resp.Content[0].(*messages.ToolUseBlock)
	if !ok {
		t.Fatalf("block[0] = %T", resp.Content[0])
	}
	if tu.ID != "toolu_1" || tu.Name != "get_weather" {
		t.Errorf("ID/Name = %q/%q", tu.ID, tu.Name)
	}
	if string(tu.Input) != `{"city":"Paris"}` {
		t.Errorf("Input=%q", tu.Input)
	}
}

// TestAccumulate_AnthropicMessages_ServerToolUseRollup exercises the
// web_search streaming shape captured from a real claude-opus-4-8 response:
// a server_tool_use block whose query streams across input_json_delta
// fragments, followed by a web_search_tool_result block delivered complete in
// its content_block_start, then the terminal usage carrying the server-tool
// counters. The server_tool_use input MUST survive the rollup — before the
// accumulator special-cased it, the streamed query was silently dropped and
// the rollup emitted an empty {} input.
func TestAccumulate_AnthropicMessages_ServerToolUseRollup(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`event: message_start` + "\n" +
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-4-8","content":[],"usage":{"input_tokens":10,"output_tokens":1}}}` + "\n\n" +
		`event: content_block_start` + "\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{}}}` + "\n\n" +
		`event: content_block_delta` + "\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\":"}}` + "\n\n" +
		`event: content_block_delta` + "\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":" \"tool_calls schema\"}"}}` + "\n\n" +
		`event: content_block_stop` + "\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		`event: content_block_start` + "\n" +
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","caller":{"type":"direct"},"content":[{"type":"web_search_result","title":"Docs","url":"https://example.com","encrypted_content":"abc"}]}}` + "\n\n" +
		`event: content_block_stop` + "\n" +
		`data: {"type":"content_block_stop","index":1}` + "\n\n" +
		`event: content_block_start` + "\n" +
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}` + "\n\n" +
		`event: content_block_delta` + "\n" +
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"Here is the answer."}}` + "\n\n" +
		`event: message_delta` + "\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":42,"server_tool_use":{"web_search_requests":1,"web_fetch_requests":0}}}` + "\n\n" +
		`event: message_stop` + "\n" +
		`data: {"type":"message_stop"}` + "\n\n")
	got := Accumulate("anthropic", "messages", raw)
	if !got.Recognised || got.Partial {
		t.Fatalf("got %+v", got)
	}
	resp := parseAnthropic(t, got.Assembled)
	if len(resp.Content) != 3 {
		t.Fatalf("Content=%d want 3", len(resp.Content))
	}

	stu, ok := resp.Content[0].(*messages.ServerToolUseBlock)
	if !ok {
		t.Fatalf("block[0] = %T want *ServerToolUseBlock", resp.Content[0])
	}
	if stu.ID != "srvtoolu_1" || stu.Name != "web_search" {
		t.Errorf("server_tool_use ID/Name = %q/%q", stu.ID, stu.Name)
	}
	// The load-bearing assertion: the streamed query is preserved, not dropped.
	// (json.RawMessage is compacted on marshal, so the inter-token space the
	// fragments carried is dropped — the value is what matters.)
	if string(stu.Input) != `{"query":"tool_calls schema"}` {
		t.Errorf("server_tool_use Input=%q want the reassembled query", stu.Input)
	}

	res, ok := resp.Content[1].(*messages.WebSearchToolResultBlock)
	if !ok {
		t.Fatalf("block[1] = %T want *WebSearchToolResultBlock", resp.Content[1])
	}
	if res.ToolUseID != "srvtoolu_1" {
		t.Errorf("web_search_tool_result tool_use_id=%q", res.ToolUseID)
	}
	if res.Caller == nil || res.Caller.Type != "direct" {
		t.Errorf("web_search_tool_result caller=%+v", res.Caller)
	}
	if !json.Valid(res.Content) || !strings.Contains(string(res.Content), "web_search_result") {
		t.Errorf("web_search_tool_result content not preserved: %s", res.Content)
	}

	if tb, ok := resp.Content[2].(*messages.TextBlock); !ok || tb.Text != "Here is the answer." {
		t.Errorf("block[2] = %+v", resp.Content[2])
	}

	if resp.Usage.ServerToolUse == nil ||
		resp.Usage.ServerToolUse.WebSearchRequests == nil || *resp.Usage.ServerToolUse.WebSearchRequests != 1 ||
		resp.Usage.ServerToolUse.WebFetchRequests == nil || *resp.Usage.ServerToolUse.WebFetchRequests != 0 {
		t.Errorf("usage.server_tool_use=%+v", resp.Usage.ServerToolUse)
	}
}

func TestAccumulate_AnthropicMessages_MultipleTextBlocksInOrder(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`event: content_block_start` + "\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		`event: content_block_delta` + "\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"alpha"}}` + "\n\n" +
		`event: content_block_start` + "\n" +
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}` + "\n\n" +
		`event: content_block_delta` + "\n" +
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"beta"}}` + "\n\n")
	got := Accumulate("anthropic", "messages", raw)
	resp := parseAnthropic(t, got.Assembled)
	if len(resp.Content) != 2 {
		t.Fatalf("Content=%d want 2", len(resp.Content))
	}
	if tb, ok := resp.Content[0].(*messages.TextBlock); !ok || tb.Text != "alpha" {
		t.Errorf("block[0] = %+v", resp.Content[0])
	}
	if tb, ok := resp.Content[1].(*messages.TextBlock); !ok || tb.Text != "beta" {
		t.Errorf("block[1] = %+v", resp.Content[1])
	}
}

func TestAccumulate_GeminiContent_ConcatsTextParts(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"candidates":[{"content":{"parts":[{"text":"Hello"}],"role":"model"},"index":0}]}` + "\n\n" +
		`data: {"candidates":[{"content":{"parts":[{"text":", "}]},"index":0}]}` + "\n\n" +
		`data: {"candidates":[{"content":{"parts":[{"text":"world!"}]},"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"totalTokenCount":8},"modelVersion":"gemini-2.0-flash"}` + "\n\n")
	got := Accumulate("gemini", "generate_content", raw)
	if !got.Recognised || got.Partial {
		t.Fatalf("got %+v", got)
	}
	resp := parseGemini(t, got.Assembled)
	if len(resp.Candidates) != 1 {
		t.Fatalf("Candidates=%d", len(resp.Candidates))
	}
	cand := resp.Candidates[0]
	if cand.Content == nil || len(cand.Content.Parts) != 1 {
		t.Fatalf("Parts=%+v", cand.Content)
	}
	tp, ok := cand.Content.Parts[0].(*geminicontent.TextPart)
	if !ok {
		t.Fatalf("part[0] = %T", cand.Content.Parts[0])
	}
	if tp.Text != "Hello, world!" {
		t.Errorf("text=%q", tp.Text)
	}
	if cand.FinishReason == nil || *cand.FinishReason != "STOP" {
		t.Errorf("FinishReason=%v", cand.FinishReason)
	}
	if resp.UsageMetadata == nil || resp.UsageMetadata.TotalTokenCount == nil || *resp.UsageMetadata.TotalTokenCount != 8 {
		t.Errorf("Usage=%+v", resp.UsageMetadata)
	}
	if resp.ModelVersion != "gemini-2.0-flash" {
		t.Errorf("ModelVersion=%q", resp.ModelVersion)
	}
}

func TestAccumulate_GeminiContent_FunctionCallPart(t *testing.T) {
	t.Parallel()
	raw := []byte(`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"get_weather","args":{"city":"Paris"}}}]},"index":0}]}` + "\n\n")
	got := Accumulate("gemini", "generate_content", raw)
	resp := parseGemini(t, got.Assembled)
	if len(resp.Candidates) != 1 || resp.Candidates[0].Content == nil {
		t.Fatalf("Candidates=%+v", resp.Candidates)
	}
	parts := resp.Candidates[0].Content.Parts
	if len(parts) != 1 {
		t.Fatalf("Parts=%d want 1", len(parts))
	}
	fc, ok := parts[0].(*geminicontent.FunctionCallPart)
	if !ok {
		t.Fatalf("part[0] = %T", parts[0])
	}
	if fc.FunctionCall.Name != "get_weather" {
		t.Errorf("Name=%q", fc.FunctionCall.Name)
	}
}

func TestAccumulate_AnthropicMessages_IgnoresPingAndUnknownEvents(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`event: ping` + "\n" +
		`data: {"type":"ping"}` + "\n\n" +
		`event: content_block_start` + "\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		`event: content_block_delta` + "\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}` + "\n\n" +
		`event: message_delta` + "\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}` + "\n\n" +
		`event: message_stop` + "\n" +
		`data: {"type":"message_stop"}` + "\n\n")
	got := Accumulate("anthropic", "messages", raw)
	if got.Partial {
		t.Error("Partial=true despite all events being valid")
	}
	resp := parseAnthropic(t, got.Assembled)
	if len(resp.Content) != 1 {
		t.Fatalf("Content=%d", len(resp.Content))
	}
	if tb, ok := resp.Content[0].(*messages.TextBlock); !ok || tb.Text != "ok" {
		t.Errorf("block[0] = %+v", resp.Content[0])
	}
}

func TestAccumulate_AnthropicMessages_MalformedEventMarksPartial(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`event: content_block_start` + "\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		`event: content_block_delta` + "\n" +
		`data: not json` + "\n\n" +
		`event: content_block_delta` + "\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}` + "\n\n")
	got := Accumulate("anthropic", "messages", raw)
	if !got.Partial {
		t.Error("Partial=false despite malformed event")
	}
	resp := parseAnthropic(t, got.Assembled)
	if tb, ok := resp.Content[0].(*messages.TextBlock); !ok || tb.Text != "ok" {
		t.Errorf("block[0] = %+v (should still parse around bad event)", resp.Content[0])
	}
}

func TestAccumulate_GeminiContent_HandlesEmptyAndMissingFields(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"candidates":[]}` + "\n\n" +
		`data: {"candidates":[{"index":0}]}` + "\n\n" +
		`data: {"candidates":[{"content":{"parts":[{"text":"hi"}]},"index":0}]}` + "\n\n")
	got := Accumulate("gemini", "generate_content", raw)
	resp := parseGemini(t, got.Assembled)
	if len(resp.Candidates) != 1 {
		t.Fatalf("Candidates=%d", len(resp.Candidates))
	}
	tp, ok := resp.Candidates[0].Content.Parts[0].(*geminicontent.TextPart)
	if !ok || tp.Text != "hi" {
		t.Errorf("text=%+v", resp.Candidates[0].Content.Parts[0])
	}
}

func TestAccumulate_GeminiContent_MalformedChunkMarksPartial(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: not json` + "\n\n" +
		`data: {"candidates":[{"content":{"parts":[{"text":"ok"}]},"index":0}]}` + "\n\n")
	got := Accumulate("gemini", "generate_content", raw)
	if !got.Partial {
		t.Error("Partial=false despite malformed chunk")
	}
	resp := parseGemini(t, got.Assembled)
	if tp, ok := resp.Candidates[0].Content.Parts[0].(*geminicontent.TextPart); !ok || tp.Text != "ok" {
		t.Errorf("text=%+v", resp.Candidates[0].Content.Parts[0])
	}
}

func TestAccumulate_AnthropicMessages_ContentBlockStartPrefilledText(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`event: content_block_start` + "\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"prefilled"}}` + "\n\n" +
		`event: content_block_delta` + "\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" more"}}` + "\n\n")
	got := Accumulate("anthropic", "messages", raw)
	resp := parseAnthropic(t, got.Assembled)
	if tb, ok := resp.Content[0].(*messages.TextBlock); !ok || tb.Text != "prefilled more" {
		t.Errorf("text=%+v", resp.Content[0])
	}
}

func TestAccumulate_AnthropicMessages_TextDeltaBeforeBlockStart(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`event: content_block_delta` + "\n" +
		`data: {"type":"content_block_delta","index":7,"delta":{"type":"text_delta","text":"orphan"}}` + "\n\n")
	got := Accumulate("anthropic", "messages", raw)
	resp := parseAnthropic(t, got.Assembled)
	if len(resp.Content) != 1 {
		t.Fatalf("Content=%d", len(resp.Content))
	}
	if tb, ok := resp.Content[0].(*messages.TextBlock); !ok || tb.Text != "orphan" {
		t.Errorf("text=%+v", resp.Content[0])
	}
}

func TestAccumulate_AnthropicMessages_ContentBlockStartWithNilBlock(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`event: content_block_start` + "\n" +
		`data: {"type":"content_block_start","index":0}` + "\n\n")
	got := Accumulate("anthropic", "messages", raw)
	resp := parseAnthropic(t, got.Assembled)
	if len(resp.Content) != 0 {
		t.Errorf("Content=%+v want 0", resp.Content)
	}
}

func TestAccumulate_EmptyDataFrameSkipped(t *testing.T) {
	t.Parallel()
	for _, endpoint := range []string{"chat_completions", "messages", "generate_content"} {
		got := Accumulate("any", endpoint, []byte("data:\n\n"))
		if got.Partial {
			t.Errorf("endpoint=%q: Partial=true on empty frame", endpoint)
		}
	}
}

func TestAccumulate_OpenAICompat_AnthropicProvider(t *testing.T) {
	t.Parallel()
	raw := []byte(`data: {"choices":[{"delta":{"content":"compat"},"index":0}]}` + "\n\n")
	got := Accumulate("anthropic", "chat_completions", raw)
	resp := parseOpenAI(t, got.Assembled)
	if openAIContent(t, resp) != "compat" {
		t.Errorf("content=%q (OpenAI-compat surface should reuse the OpenAI accumulator)", openAIContent(t, resp))
	}
}

func TestAccumulate_OpenAIChat_RefusalAndServiceTier(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"service_tier":"flex","choices":[{"delta":{"role":"assistant","refusal":"I can't help with that."},"index":0,"finish_reason":"content_filter"}]}` + "\n\n" +
		"data: [DONE]\n\n")
	got := Accumulate("openai", "chat_completions", raw)
	resp := parseOpenAI(t, got.Assembled)
	if resp.ServiceTier != "flex" {
		t.Errorf("ServiceTier=%q", resp.ServiceTier)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("Choices=%d", len(resp.Choices))
	}
	if resp.Choices[0].Message.Refusal == nil || *resp.Choices[0].Message.Refusal != "I can't help with that." {
		t.Errorf("Refusal=%v", resp.Choices[0].Message.Refusal)
	}
}

func TestAccumulate_OpenAIChat_MultipleChoicesPreserveIndex(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"choices":[{"delta":{"role":"assistant","content":"first"},"index":0},{"delta":{"role":"assistant","content":"second"},"index":1}]}` + "\n\n" +
		`data: {"choices":[{"delta":{},"index":0,"finish_reason":"stop"},{"delta":{},"index":1,"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]\n\n")
	got := Accumulate("openai", "chat_completions", raw)
	resp := parseOpenAI(t, got.Assembled)
	if len(resp.Choices) != 2 {
		t.Fatalf("Choices=%d want 2", len(resp.Choices))
	}
	if resp.Choices[0].Index != 0 || resp.Choices[1].Index != 1 {
		t.Errorf("indices = %d, %d", resp.Choices[0].Index, resp.Choices[1].Index)
	}
}

func TestAccumulate_AnthropicMessages_MessageDeltaCarriesCacheTokens(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`event: message_start` + "\n" +
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-3-5-sonnet","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":0}}}` + "\n\n" +
		`event: message_delta` + "\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"stop_sequence","stop_sequence":"</end>"},"usage":{"input_tokens":12,"output_tokens":5,"cache_creation_input_tokens":3,"cache_read_input_tokens":7}}` + "\n\n" +
		`event: message_stop` + "\n" +
		`data: {"type":"message_stop"}` + "\n\n")
	got := Accumulate("anthropic", "messages", raw)
	resp := parseAnthropic(t, got.Assembled)
	if resp.StopReason == nil || *resp.StopReason != "stop_sequence" {
		t.Errorf("StopReason=%v", resp.StopReason)
	}
	if resp.StopSequence == nil || *resp.StopSequence != "</end>" {
		t.Errorf("StopSequence=%v", resp.StopSequence)
	}
	if resp.Usage.InputTokens != 12 || resp.Usage.OutputTokens != 5 {
		t.Errorf("tokens = %d/%d", resp.Usage.InputTokens, resp.Usage.OutputTokens)
	}
	if resp.Usage.CacheCreationInputTokens == nil || *resp.Usage.CacheCreationInputTokens != 3 {
		t.Errorf("CacheCreationInputTokens=%v", resp.Usage.CacheCreationInputTokens)
	}
	if resp.Usage.CacheReadInputTokens == nil || *resp.Usage.CacheReadInputTokens != 7 {
		t.Errorf("CacheReadInputTokens=%v", resp.Usage.CacheReadInputTokens)
	}
}

func TestAccumulate_AnthropicMessages_NoMessageStart_EmitsMinimalShell(t *testing.T) {
	t.Parallel()
	// Stream truncated before message_start arrived. Accumulator should
	// still emit a parseable MessagesResponse with type/role set so
	// the live viewer renders something sensible.
	raw := []byte("" +
		`event: content_block_start` + "\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		`event: content_block_delta` + "\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}` + "\n\n")
	got := Accumulate("anthropic", "messages", raw)
	resp := parseAnthropic(t, got.Assembled)
	if resp.Type != "message" || resp.Role != "assistant" {
		t.Errorf("envelope = %+v want type=message role=assistant", resp)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("Content=%d", len(resp.Content))
	}
}

func TestAccumulate_GeminiContent_MultipleCandidates(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"candidates":[{"content":{"parts":[{"text":"alpha"}]},"index":0},{"content":{"parts":[{"text":"beta"}]},"index":1}],"promptFeedback":{"blockReason":"OTHER"}}` + "\n\n" +
		`data: {"candidates":[{"content":{"parts":[{"text":"-end"}]},"index":0,"finishReason":"STOP","tokenCount":4}]}` + "\n\n")
	got := Accumulate("gemini", "generate_content", raw)
	resp := parseGemini(t, got.Assembled)
	if resp.PromptFeedback == nil {
		t.Error("PromptFeedback=nil — should have been carried forward")
	}
	if len(resp.Candidates) != 2 {
		t.Fatalf("Candidates=%d want 2", len(resp.Candidates))
	}
	tp0, _ := resp.Candidates[0].Content.Parts[0].(*geminicontent.TextPart)
	if tp0 == nil || tp0.Text != "alpha-end" {
		t.Errorf("cand[0] text = %+v", resp.Candidates[0].Content.Parts[0])
	}
	tp1, _ := resp.Candidates[1].Content.Parts[0].(*geminicontent.TextPart)
	if tp1 == nil || tp1.Text != "beta" {
		t.Errorf("cand[1] text = %+v", resp.Candidates[1].Content.Parts[0])
	}
	if resp.Candidates[0].TokenCount == nil || *resp.Candidates[0].TokenCount != 4 {
		t.Errorf("cand[0] tokenCount=%v", resp.Candidates[0].TokenCount)
	}
}

func TestAccumulate_OpenAIChat_RoleFallsThroughFromFirstDelta(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"choices":[{"delta":{"role":"assistant"},"index":0}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"f","arguments":"{}"}}]},"index":0,"finish_reason":"tool_calls"}]}` + "\n\n" +
		"data: [DONE]\n\n")
	got := Accumulate("openai", "chat_completions", raw)
	resp := parseOpenAI(t, got.Assembled)
	if resp.Choices[0].Message.Role != "assistant" {
		t.Errorf("Role=%q", resp.Choices[0].Message.Role)
	}
	// No content delta arrived — content should be absent (not "")
	if len(resp.Choices[0].Message.Content) != 0 {
		t.Errorf("Content=%q expected absent", resp.Choices[0].Message.Content)
	}
}

// --- regression coverage for the rollup-fidelity audit (modeled-field loss) ---

// TestAccumulate_AnthropicMessages_AccumulatesCitations proves citations_delta
// events are collected onto TextBlock.Citations rather than dropped.
func TestAccumulate_AnthropicMessages_AccumulatesCitations(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"The sky is blue."}}` + "\n\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"citations_delta","citation":{"type":"web_search_result_location","url":"https://e.com","title":"Sky","cited_text":"blue"}}}` + "\n\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n")
	got := Accumulate("anthropic", "messages", raw)
	resp := parseAnthropic(t, got.Assembled)
	tb, ok := resp.Content[0].(*messages.TextBlock)
	if !ok {
		t.Fatalf("block[0] = %T", resp.Content[0])
	}
	if len(tb.Citations) != 1 {
		t.Fatalf("citations = %d want 1 (citations_delta dropped)", len(tb.Citations))
	}
	if tb.Citations[0].URL != "https://e.com" || tb.Citations[0].CitedText != "blue" {
		t.Errorf("citation = %+v", tb.Citations[0])
	}
}

// TestAccumulate_AnthropicMessages_PreservesToolCaller proves the modeled
// ToolUseBlock.Caller (a typed field, not DynamicProperties) survives assembly.
func TestAccumulate_AnthropicMessages_PreservesToolCaller(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"Edit","input":{},"caller":{"type":"direct"}}}` + "\n\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"a\":1}"}}` + "\n\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n")
	got := Accumulate("anthropic", "messages", raw)
	resp := parseAnthropic(t, got.Assembled)
	tu, ok := resp.Content[0].(*messages.ToolUseBlock)
	if !ok {
		t.Fatalf("block[0] = %T", resp.Content[0])
	}
	if tu.Caller == nil {
		t.Fatalf("tool caller dropped on assembly")
	}
	callerJSON, _ := json.Marshal(tu.Caller)
	if string(callerJSON) != `{"type":"direct"}` {
		t.Errorf("caller = %s", callerJSON)
	}
	if string(tu.Input) != `{"a":1}` {
		t.Errorf("input = %s", tu.Input)
	}
}

// TestAccumulate_AnthropicMessages_UsageBreakdownsSurvive proves the modeled
// usage breakdowns (output_tokens_details, server_tool_use, and the now-typed
// iterations) survive the terminal message_delta, AND that a still-unmodeled
// usage field round-trips via DynamicProperties.
func TestAccumulate_AnthropicMessages_UsageBreakdownsSurvive(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"x","content":[],"usage":{"input_tokens":5,"output_tokens":1}}}` + "\n\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":42,"output_tokens_details":{"thinking_tokens":7},"server_tool_use":{"web_search_requests":3},"iterations":[{"type":"message","output_tokens":42}],"future_usage_field":{"x":1}}}` + "\n\n")
	got := Accumulate("anthropic", "messages", raw)
	resp := parseAnthropic(t, got.Assembled)
	if resp.Usage.OutputTokensDetails == nil || resp.Usage.OutputTokensDetails.ThinkingTokens == nil || *resp.Usage.OutputTokensDetails.ThinkingTokens != 7 {
		t.Errorf("output_tokens_details dropped: %+v", resp.Usage.OutputTokensDetails)
	}
	if resp.Usage.ServerToolUse == nil || resp.Usage.ServerToolUse.WebSearchRequests == nil || *resp.Usage.ServerToolUse.WebSearchRequests != 3 {
		t.Errorf("server_tool_use dropped: %+v", resp.Usage.ServerToolUse)
	}
	if len(resp.Usage.Iterations) != 1 || resp.Usage.Iterations[0].Type != "message" || resp.Usage.Iterations[0].OutputTokens != 42 {
		t.Errorf("modeled usage field 'iterations' dropped: %+v", resp.Usage.Iterations)
	}
	if len(resp.Usage.Extra["future_usage_field"]) == 0 {
		t.Errorf("unmodeled usage field 'future_usage_field' dropped: %v", resp.Usage.Extra)
	}
}

// TestAccumulate_AnthropicMessages_UsageIsCumulativeNotSummed is the billing
// guard: message_delta.usage carries final cumulative totals, so values must
// REPLACE the message_start placeholders, never accumulate. A "+=" regression
// would double cache_read (identical in both events) and over-count output.
func TestAccumulate_AnthropicMessages_UsageIsCumulativeNotSummed(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"x","content":[],"usage":{"input_tokens":10,"output_tokens":1,"cache_read_input_tokens":2000}}}` + "\n\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":10,"output_tokens":50,"cache_read_input_tokens":2000}}` + "\n\n")
	got := Accumulate("anthropic", "messages", raw)
	resp := parseAnthropic(t, got.Assembled)
	if resp.Usage.OutputTokens != 50 {
		t.Errorf("output_tokens = %d want 50 (terminal value)", resp.Usage.OutputTokens)
	}
	if resp.Usage.InputTokens != 10 {
		t.Errorf("input_tokens = %d want 10 (not summed to 20)", resp.Usage.InputTokens)
	}
	if resp.Usage.CacheReadInputTokens == nil || *resp.Usage.CacheReadInputTokens != 2000 {
		t.Errorf("cache_read = %v want 2000 (a += regression would double it to 4000 — billing corruption)", resp.Usage.CacheReadInputTokens)
	}
}

// TestAccumulate_AnthropicMessages_TruncatedToolInputStaysValidJSON proves a
// stream truncated mid input_json_delta does not emit a structurally-invalid
// body: the unterminated fragment is rejected in favour of valid JSON.
func TestAccumulate_AnthropicMessages_TruncatedToolInputStaysValidJSON(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{}}}` + "\n\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}` + "\n\n")
	got := Accumulate("anthropic", "messages", raw)
	resp := parseAnthropic(t, got.Assembled) // would fail if body were invalid JSON
	tu, ok := resp.Content[0].(*messages.ToolUseBlock)
	if !ok {
		t.Fatalf("block[0] = %T", resp.Content[0])
	}
	if !json.Valid(tu.Input) {
		t.Fatalf("truncated tool input is not valid JSON: %s", tu.Input)
	}
}

// TestAssemble_DefensiveFallbacksWhenRawMissing exercises the assemble()
// fallback paths that build a fresh block when no content_block_start was
// observed for an index (raw == nil) — a truncated/garbled capture. White-box
// because the SSE path always seeds raw for tool_use/thinking.
func TestAssemble_DefensiveFallbacksWhenRawMissing(t *testing.T) {
	t.Parallel()
	s := newAnthropicState()
	for i, kind := range map[int]string{0: "text", 1: "tool_use", 2: "thinking"} {
		s.blocks[i] = &anthropicBlockState{kind: kind} // raw nil on purpose
		s.blockOrder = append(s.blockOrder, i)
	}
	resp := s.assemble()
	if len(resp.Content) != 3 {
		t.Fatalf("content = %d want 3", len(resp.Content))
	}
	for _, blk := range resp.Content {
		switch b := blk.(type) {
		case *messages.TextBlock:
		case *messages.ToolUseBlock:
			if string(b.Input) != "{}" {
				t.Errorf("tool_use fallback input = %s want {}", b.Input)
			}
		case *messages.ThinkingBlock:
		default:
			t.Errorf("unexpected block type %T", blk)
		}
	}
}
