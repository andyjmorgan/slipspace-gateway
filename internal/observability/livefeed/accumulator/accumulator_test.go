package accumulator

import (
	"strings"
	"testing"
)

func TestAccumulate_UnknownEndpointReturnsZeroResult(t *testing.T) {
	t.Parallel()
	got := Accumulate("openai", "models", []byte("data: {}\n\n"))
	if got.Recognised {
		t.Fatal("Recognised=true for unknown endpoint")
	}
	if got.Text != "" || len(got.ToolCalls) != 0 {
		t.Fatalf("expected empty Result, got %+v", got)
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
		`data: {"choices":[{"delta":{"role":"assistant","content":"Hello"},"index":0}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"content":", "},"index":0}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"content":"world!"},"index":0}]}` + "\n\n" +
		"data: [DONE]\n\n")
	got := Accumulate("openai", "chat_completions", raw)
	if !got.Recognised {
		t.Fatal("Recognised=false")
	}
	if got.Text != "Hello, world!" {
		t.Errorf("Text=%q", got.Text)
	}
	if got.Partial {
		t.Error("Partial=true")
	}
	if len(got.ToolCalls) != 0 {
		t.Errorf("ToolCalls=%+v", got.ToolCalls)
	}
}

func TestAccumulate_OpenAIChat_AssemblesToolCalls(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]},"index":0}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]},"index":0}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Paris\"}"}}]},"index":0}]}` + "\n\n" +
		"data: [DONE]\n\n")
	got := Accumulate("openai", "chat_completions", raw)
	if !got.Recognised || got.Partial {
		t.Fatalf("got %+v", got)
	}
	if len(got.ToolCalls) != 1 {
		t.Fatalf("ToolCalls=%+v want 1", got.ToolCalls)
	}
	tc := got.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "get_weather" {
		t.Errorf("ID/Name = %q/%q", tc.ID, tc.Name)
	}
	if tc.Arguments != `{"city":"Paris"}` {
		t.Errorf("Arguments=%q", tc.Arguments)
	}
}

func TestAccumulate_OpenAIChat_PartialOnMalformedChunk(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"choices":[{"delta":{"content":"Hello"},"index":0}]}` + "\n\n" +
		"data: not valid json\n\n" +
		`data: {"choices":[{"delta":{"content":" world"},"index":0}]}` + "\n\n")
	got := Accumulate("openai", "chat_completions", raw)
	if got.Text != "Hello world" {
		t.Errorf("Text=%q (should have parsed around the bad chunk)", got.Text)
	}
	if !got.Partial {
		t.Error("Partial=false despite malformed chunk")
	}
}

func TestAccumulate_OpenAIChat_HonoursDONE(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"choices":[{"delta":{"content":"Hello"},"index":0}]}` + "\n\n" +
		"data: [DONE]\n\n" +
		`data: {"choices":[{"delta":{"content":" SHOULD NOT APPEAR"},"index":0}]}` + "\n\n")
	got := Accumulate("openai", "chat_completions", raw)
	if got.Text != "Hello" {
		t.Errorf("Text=%q want 'Hello' (anything after [DONE] should be ignored)", got.Text)
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
		`event: message_stop` + "\n" +
		`data: {"type":"message_stop"}` + "\n\n")
	got := Accumulate("anthropic", "messages", raw)
	if !got.Recognised {
		t.Fatal("Recognised=false")
	}
	if got.Text != "Hello, world!" {
		t.Errorf("Text=%q", got.Text)
	}
	if got.Partial {
		t.Error("Partial=true")
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
	if len(got.ToolCalls) != 1 {
		t.Fatalf("ToolCalls=%+v", got.ToolCalls)
	}
	tc := got.ToolCalls[0]
	if tc.ID != "toolu_1" || tc.Name != "get_weather" || tc.Arguments != `{"city":"Paris"}` {
		t.Errorf("tool = %+v", tc)
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
	if got.Text != "alphabeta" {
		t.Errorf("Text=%q want 'alphabeta'", got.Text)
	}
}

func TestAccumulate_GeminiContent_ConcatsTextParts(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"candidates":[{"content":{"parts":[{"text":"Hello"}]},"index":0}]}` + "\n\n" +
		`data: {"candidates":[{"content":{"parts":[{"text":", "}]},"index":0}]}` + "\n\n" +
		`data: {"candidates":[{"content":{"parts":[{"text":"world!"}]},"index":0,"finishReason":"STOP"}]}` + "\n\n")
	got := Accumulate("gemini", "generate_content", raw)
	if !got.Recognised {
		t.Fatal("Recognised=false")
	}
	if got.Text != "Hello, world!" {
		t.Errorf("Text=%q", got.Text)
	}
	if got.Partial {
		t.Error("Partial=true")
	}
}

func TestAccumulate_GeminiContent_FunctionCallPart(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"get_weather","args":{"city":"Paris"}}}]},"index":0}]}` + "\n\n")
	got := Accumulate("gemini", "generate_content", raw)
	if len(got.ToolCalls) != 1 {
		t.Fatalf("ToolCalls=%+v", got.ToolCalls)
	}
	tc := got.ToolCalls[0]
	if tc.Name != "get_weather" {
		t.Errorf("Name=%q", tc.Name)
	}
	// Args are stored as JSON string; check it parses something sane.
	if !strings.Contains(tc.Arguments, `"city"`) || !strings.Contains(tc.Arguments, `"Paris"`) {
		t.Errorf("Arguments=%q", tc.Arguments)
	}
}

func TestAccumulate_OpenAIChat_ContentAsContentParts(t *testing.T) {
	t.Parallel()
	// content arrives as an array of {type:"text",text:"…"} parts —
	// vanishingly rare on streaming responses but the wire shape is
	// shared with non-streaming chat completion messages and the
	// decoder needs to handle it.
	raw := []byte(`data: {"choices":[{"delta":{"content":[{"type":"text","text":"part-one "},{"type":"text","text":"part-two"}]},"index":0}]}` + "\n\n")
	got := Accumulate("openai", "chat_completions", raw)
	if got.Text != "part-one part-two" {
		t.Errorf("Text=%q", got.Text)
	}
}

func TestAccumulate_OpenAIChat_MalformedContentSilentlySkipped(t *testing.T) {
	t.Parallel()
	// content is a number — neither bare string nor array of parts.
	// Decoder returns "", chunk contributes nothing.
	raw := []byte(`data: {"choices":[{"delta":{"content":42},"index":0}]}` + "\n\n")
	got := Accumulate("openai", "chat_completions", raw)
	if got.Text != "" {
		t.Errorf("Text=%q want empty", got.Text)
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
	if got.Text != "ok" {
		t.Errorf("Text=%q", got.Text)
	}
	if got.Partial {
		t.Error("Partial=true despite all events being valid")
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
	if got.Text != "ok" {
		t.Errorf("Text=%q (should still parse around the bad event)", got.Text)
	}
}

func TestAccumulate_GeminiContent_HandlesEmptyAndMissingFields(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`data: {"candidates":[]}` + "\n\n" +
		`data: {"candidates":[{"index":0}]}` + "\n\n" +
		`data: {"candidates":[{"content":{"parts":[{"text":"hi"}]},"index":0}]}` + "\n\n")
	got := Accumulate("gemini", "generate_content", raw)
	if got.Text != "hi" {
		t.Errorf("Text=%q want 'hi'", got.Text)
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
	if got.Text != "ok" {
		t.Errorf("Text=%q", got.Text)
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
	if got.Text != "prefilled more" {
		t.Errorf("Text=%q", got.Text)
	}
}

func TestAccumulate_AnthropicMessages_TextDeltaBeforeBlockStart(t *testing.T) {
	t.Parallel()
	// Streams can be truncated such that a content_block_delta arrives
	// without a prior content_block_start. The accumulator should
	// initialise on the fly rather than dropping the text.
	raw := []byte("" +
		`event: content_block_delta` + "\n" +
		`data: {"type":"content_block_delta","index":7,"delta":{"type":"text_delta","text":"orphan"}}` + "\n\n")
	got := Accumulate("anthropic", "messages", raw)
	if got.Text != "orphan" {
		t.Errorf("Text=%q", got.Text)
	}
}

func TestAccumulate_AnthropicMessages_ContentBlockStartWithNilBlock(t *testing.T) {
	t.Parallel()
	// Defensive: gateway should not crash if upstream emits a
	// content_block_start with no content_block (this would normally
	// be caught by the typed unmarshaller, but we forward anything
	// that parses cleanly).
	raw := []byte("" +
		`event: content_block_start` + "\n" +
		`data: {"type":"content_block_start","index":0}` + "\n\n")
	got := Accumulate("anthropic", "messages", raw)
	if got.Text != "" || len(got.ToolCalls) != 0 {
		t.Errorf("expected empty result, got %+v", got)
	}
}

func TestAccumulate_EmptyDataFrameSkipped(t *testing.T) {
	t.Parallel()
	// SSE frames whose `data:` lines are empty (e.g. `data:\n\n`)
	// should be silently skipped across every accumulator.
	for _, endpoint := range []string{"chat_completions", "messages", "generate_content"} {
		got := Accumulate("any", endpoint, []byte("data:\n\n"))
		if got.Text != "" || got.Partial {
			t.Errorf("endpoint=%q: expected empty/no-partial, got %+v", endpoint, got)
		}
	}
}

func TestAccumulate_OpenAICompat_AnthropicProvider(t *testing.T) {
	t.Parallel()
	// /anthropic/v1/chat/completions emits OpenAI-shaped chunks even
	// though provider=anthropic. The dispatcher keys on endpoint, so
	// the OpenAI accumulator should run.
	raw := []byte(`data: {"choices":[{"delta":{"content":"compat"},"index":0}]}` + "\n\n")
	got := Accumulate("anthropic", "chat_completions", raw)
	if got.Text != "compat" {
		t.Errorf("OpenAI-compat surface on anthropic should reuse the OpenAI accumulator; got Text=%q", got.Text)
	}
}
