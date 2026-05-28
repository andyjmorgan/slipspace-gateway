package genaiattr_test

import (
	"strings"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/middleware/genaiattr"
)

func TestExtractResponse_OpenAIChat_JSON(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"id":"chatcmpl-1","model":"gpt-4o-mini-2024-07-18","choices":[{"finish_reason":"stop"}],"usage":{"completion_tokens_details":{"reasoning_tokens":12}}}`)
	got := genaiattr.ExtractResponse("chat_completions", raw)
	if got.ID != "chatcmpl-1" || got.Model != "gpt-4o-mini-2024-07-18" {
		t.Errorf("id/model = %q/%q", got.ID, got.Model)
	}
	if len(got.FinishReasons) != 1 || got.FinishReasons[0] != "stop" {
		t.Errorf("finish_reasons = %v", got.FinishReasons)
	}
	if got.ReasoningTokens == nil || *got.ReasoningTokens != 12 {
		t.Errorf("reasoning_tokens = %v, want 12", got.ReasoningTokens)
	}
}

func TestExtractResponse_OpenAIChat_SSE(t *testing.T) {
	t.Parallel()
	raw := []byte("data: {\"id\":\"chatcmpl-9\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chatcmpl-9\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\n" +
		"data: [DONE]\n\n")
	got := genaiattr.ExtractResponse("chat_completions", raw)
	if got.ID != "chatcmpl-9" || got.Model != "gpt-4o" {
		t.Errorf("id/model = %q/%q", got.ID, got.Model)
	}
	if len(got.FinishReasons) != 1 || got.FinishReasons[0] != "length" {
		t.Errorf("finish_reasons = %v, want [length]", got.FinishReasons)
	}
}

func TestExtractResponse_Anthropic_JSON(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"id":"msg_1","model":"claude-sonnet-4-6","stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`)
	got := genaiattr.ExtractResponse("messages", raw)
	if got.ID != "msg_1" || got.Model != "claude-sonnet-4-6" {
		t.Errorf("id/model = %q/%q", got.ID, got.Model)
	}
	if len(got.FinishReasons) != 1 || got.FinishReasons[0] != "end_turn" {
		t.Errorf("finish_reasons = %v", got.FinishReasons)
	}
}

func TestExtractResponse_Anthropic_SSE(t *testing.T) {
	t.Parallel()
	raw := []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_7\",\"model\":\"claude-opus-4-7\"}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"}}\n\n")
	got := genaiattr.ExtractResponse("messages", raw)
	if got.ID != "msg_7" || got.Model != "claude-opus-4-7" {
		t.Errorf("id/model = %q/%q", got.ID, got.Model)
	}
	if len(got.FinishReasons) != 1 || got.FinishReasons[0] != "max_tokens" {
		t.Errorf("finish_reasons = %v", got.FinishReasons)
	}
}

func TestExtractResponse_Anthropic_ThinkingJSON(t *testing.T) {
	t.Parallel()
	// Non-streaming: a thinking block precedes the text block in Content.
	raw := []byte(`{"id":"msg_1","model":"claude-opus-4-7","content":[{"type":"thinking","thinking":"deliberating","signature":"sig"},{"type":"text","text":"the answer"}],"stop_reason":"end_turn"}`)
	got := genaiattr.ExtractResponse("messages", raw)
	if len(got.OutputParts) != 2 {
		t.Fatalf("parts = %+v, want [reasoning, text]", got.OutputParts)
	}
	if got.OutputParts[0].Type != "reasoning" || got.OutputParts[0].Content != "deliberating" {
		t.Errorf("part0 = %+v, want reasoning/deliberating", got.OutputParts[0])
	}
	if got.OutputParts[1].Type != "text" || got.OutputParts[1].Content != "the answer" {
		t.Errorf("part1 = %+v, want text/the answer", got.OutputParts[1])
	}
	if got.OutputText != "the answer" {
		t.Errorf("output text = %q, want 'the answer' (text blocks only)", got.OutputText)
	}
}

func TestExtractResponse_Anthropic_ThinkingSSE(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\",\"signature\":\"\"}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"step \"}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"one\"}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"sig\"}}\n\n" +
		"data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"done\"}}\n\n")
	got := genaiattr.ExtractResponse("messages", raw)
	if len(got.OutputParts) != 2 {
		t.Fatalf("parts = %+v, want [reasoning, text]", got.OutputParts)
	}
	if got.OutputParts[0].Type != "reasoning" || got.OutputParts[0].Content != "step one" {
		t.Errorf("part0 = %+v, want reasoning/'step one'", got.OutputParts[0])
	}
	if got.OutputParts[1].Type != "text" || got.OutputParts[1].Content != "done" {
		t.Errorf("part1 = %+v, want text/done", got.OutputParts[1])
	}
}

func TestExtractResponse_Anthropic_InterleavedThinkTextThink(t *testing.T) {
	t.Parallel()
	// think, text, think again — the parts must preserve wire order, not be
	// collapsed into one aggregate reasoning part.
	raw := []byte(`{"id":"m","model":"claude","content":[{"type":"thinking","thinking":"A","signature":"s1"},{"type":"text","text":"B"},{"type":"thinking","thinking":"C","signature":"s2"}]}`)
	got := genaiattr.ExtractResponse("messages", raw)
	if len(got.OutputParts) != 3 {
		t.Fatalf("parts = %+v, want 3", got.OutputParts)
	}
	want := []struct{ typ, content string }{
		{"reasoning", "A"},
		{"text", "B"},
		{"reasoning", "C"},
	}
	for i, w := range want {
		if got.OutputParts[i].Type != w.typ || got.OutputParts[i].Content != w.content {
			t.Errorf("part%d = %+v, want %s/%s", i, got.OutputParts[i], w.typ, w.content)
		}
	}
}

func TestExtractResponse_Anthropic_RedactedThinking(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"id":"m","model":"claude","content":[{"type":"redacted_thinking","data":"blob"},{"type":"text","text":"hi"}]}`)
	got := genaiattr.ExtractResponse("messages", raw)
	if len(got.OutputParts) != 2 {
		t.Fatalf("parts = %+v, want [reasoning, text]", got.OutputParts)
	}
	if got.OutputParts[0].Type != "reasoning" || got.OutputParts[0].Content != "" {
		t.Errorf("part0 = %+v, want reasoning with empty content", got.OutputParts[0])
	}
}

func TestExtractResponse_Gemini(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"responseId":"resp-1","modelVersion":"gemini-2.0-flash-001","candidates":[{"finishReason":"STOP"}],"usageMetadata":{"thoughtsTokenCount":33}}`)
	got := genaiattr.ExtractResponse("generate_content", raw)
	if got.ID != "resp-1" || got.Model != "gemini-2.0-flash-001" {
		t.Errorf("id/model = %q/%q", got.ID, got.Model)
	}
	if len(got.FinishReasons) != 1 || got.FinishReasons[0] != "STOP" {
		t.Errorf("finish_reasons = %v", got.FinishReasons)
	}
	if got.ReasoningTokens == nil || *got.ReasoningTokens != 33 {
		t.Errorf("reasoning_tokens = %v, want 33", got.ReasoningTokens)
	}
}

func TestExtractResponse_OpenAIResponses_Wrapped(t *testing.T) {
	t.Parallel()
	// Responses SSE wraps the object under "response" on some events.
	raw := []byte("data: {\"response\":{\"id\":\"resp_abc\",\"model\":\"gpt-4.1\"}}\n\n")
	got := genaiattr.ExtractResponse("responses", raw)
	if got.ID != "resp_abc" || got.Model != "gpt-4.1" {
		t.Errorf("id/model = %q/%q", got.ID, got.Model)
	}
}

func TestExtractResponse_MultiChoiceFinishReasonsDedup(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"id":"c1","model":"m","choices":[{"finish_reason":"stop"},{"finish_reason":"stop"},{"finish_reason":"length"}]}`)
	got := genaiattr.ExtractResponse("chat_completions", raw)
	if strings.Join(got.FinishReasons, ",") != "stop,length" {
		t.Errorf("finish_reasons = %v, want [stop length] in order, deduped", got.FinishReasons)
	}
}

func TestExtractResponse_OutputText(t *testing.T) {
	t.Parallel()

	// OpenAI non-streaming: message.content.
	if got := genaiattr.ExtractResponse("chat_completions", []byte(`{"choices":[{"message":{"content":"hello world"},"finish_reason":"stop"}]}`)); got.OutputText != "hello world" {
		t.Errorf("openai non-stream output = %q, want 'hello world'", got.OutputText)
	}
	// OpenAI streaming: delta.content accumulated across chunks.
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\ndata: [DONE]\n\n"
	if got := genaiattr.ExtractResponse("chat_completions", []byte(sse)); got.OutputText != "hello" {
		t.Errorf("openai stream output = %q, want 'hello'", got.OutputText)
	}
	// OpenAI array (multimodal) content flattens to its text parts.
	if got := genaiattr.ExtractResponse("chat_completions", []byte(`{"choices":[{"message":{"content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}}]}`)); got.OutputText != "ab" {
		t.Errorf("openai array content output = %q, want 'ab'", got.OutputText)
	}
	// Anthropic non-streaming: content[].text.
	if got := genaiattr.ExtractResponse("messages", []byte(`{"content":[{"type":"text","text":"hi"},{"type":"text","text":" there"}],"stop_reason":"end_turn"}`)); got.OutputText != "hi there" {
		t.Errorf("anthropic non-stream output = %q, want 'hi there'", got.OutputText)
	}
	// Anthropic streaming: content_block_delta text_delta.
	asse := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"foo\"}}\n\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"bar\"}}\n\n"
	if got := genaiattr.ExtractResponse("messages", []byte(asse)); got.OutputText != "foobar" {
		t.Errorf("anthropic stream output = %q, want 'foobar'", got.OutputText)
	}
	// Gemini: candidates[].content.parts[].text.
	if got := genaiattr.ExtractResponse("generate_content", []byte(`{"candidates":[{"content":{"parts":[{"text":"g1"},{"text":"g2"}]},"finishReason":"STOP"}]}`)); got.OutputText != "g1g2" {
		t.Errorf("gemini output = %q, want 'g1g2'", got.OutputText)
	}
}

func TestExtractResponse_ResponsesOutputText(t *testing.T) {
	t.Parallel()
	// Non-streaming: output[].content[].text.
	if got := genaiattr.ExtractResponse("responses", []byte(`{"id":"r","output":[{"content":[{"type":"output_text","text":"hi from responses"}]}]}`)); got.OutputText != "hi from responses" {
		t.Errorf("responses non-stream output = %q", got.OutputText)
	}
	// Streaming: response.output_text.delta events accumulate.
	sse := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"foo\"}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"bar\"}\n\n"
	if got := genaiattr.ExtractResponse("responses", []byte(sse)); got.OutputText != "foobar" {
		t.Errorf("responses stream output = %q, want foobar", got.OutputText)
	}
	// Wrapped under "response" (response.completed), incl. nested output.
	wrapped := "data: {\"response\":{\"id\":\"r2\",\"output\":[{\"content\":[{\"text\":\"wrapped\"}]}]}}\n\n"
	if got := genaiattr.ExtractResponse("responses", []byte(wrapped)); got.OutputText != "wrapped" || got.ID != "r2" {
		t.Errorf("responses wrapped output = %q id=%q", got.OutputText, got.ID)
	}
}

func TestExtractResponse_OpenAIServiceTierFingerprint(t *testing.T) {
	t.Parallel()
	got := genaiattr.ExtractResponse("chat_completions", []byte(`{"id":"c","model":"m","service_tier":"default","system_fingerprint":"fp_abc","choices":[]}`))
	if got.ServiceTier != "default" || got.SystemFingerprint != "fp_abc" {
		t.Errorf("service_tier=%q system_fingerprint=%q", got.ServiceTier, got.SystemFingerprint)
	}
	// Responses API nests the object (and the tier) under "response".
	got2 := genaiattr.ExtractResponse("responses", []byte("data: {\"response\":{\"id\":\"r\",\"service_tier\":\"flex\"}}\n\n"))
	if got2.ServiceTier != "flex" {
		t.Errorf("responses service_tier = %q, want flex", got2.ServiceTier)
	}
}

// toolCalls returns the tool_call parts of an output parts slice.
func toolCalls(parts []genaiattr.Part) []genaiattr.Part {
	var tc []genaiattr.Part
	for _, p := range parts {
		if p.Type == "tool_call" {
			tc = append(tc, p)
		}
	}
	return tc
}

func TestExtractResponse_ToolCalls(t *testing.T) {
	t.Parallel()

	// OpenAI non-streaming: choices[].message.tool_calls.
	t.Run("openai non-stream", func(t *testing.T) {
		t.Parallel()
		got := genaiattr.ExtractResponse("chat_completions", []byte(`{"choices":[{"message":{"content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"loc\":\"Paris\"}"}}]},"finish_reason":"tool_calls"}]}`))
		tc := toolCalls(got.OutputParts)
		if len(tc) != 1 || tc[0].ID != "call_1" || tc[0].Name != "get_weather" {
			t.Fatalf("tool calls = %+v", tc)
		}
		if string(tc[0].Arguments) != `{"loc":"Paris"}` {
			t.Errorf("arguments = %s", tc[0].Arguments)
		}
		// Tool-only response keeps its tool_call and emits no text part.
		if len(got.OutputParts) != 1 {
			t.Errorf("output parts = %+v, want only the tool_call", got.OutputParts)
		}
	})

	// OpenAI streaming: id+name on the first delta, arguments fragmented.
	t.Run("openai stream fragmented args", func(t *testing.T) {
		t.Parallel()
		sse := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_9\",\"function\":{\"name\":\"f\",\"arguments\":\"{\\\"lo\"}}]}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"c\\\":\\\"Paris\\\"}\"}}]}}]}\n\n" +
			"data: [DONE]\n\n"
		got := genaiattr.ExtractResponse("chat_completions", []byte(sse))
		tc := toolCalls(got.OutputParts)
		if len(tc) != 1 || tc[0].ID != "call_9" || tc[0].Name != "f" {
			t.Fatalf("tool calls = %+v", tc)
		}
		if string(tc[0].Arguments) != `{"loc":"Paris"}` {
			t.Errorf("reassembled arguments = %s, want {\"loc\":\"Paris\"}", tc[0].Arguments)
		}
	})

	// Text followed by a tool call: both parts present, text first.
	t.Run("openai text plus tool call", func(t *testing.T) {
		t.Parallel()
		got := genaiattr.ExtractResponse("chat_completions", []byte(`{"choices":[{"message":{"content":"let me check","tool_calls":[{"id":"c","function":{"name":"f","arguments":"{}"}}]}}]}`))
		if len(got.OutputParts) != 2 || got.OutputParts[0].Type != "text" || got.OutputParts[1].Type != "tool_call" {
			t.Fatalf("parts = %+v, want [text, tool_call]", got.OutputParts)
		}
	})

	// Anthropic non-streaming: tool_use content block.
	t.Run("anthropic non-stream", func(t *testing.T) {
		t.Parallel()
		got := genaiattr.ExtractResponse("messages", []byte(`{"content":[{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"loc":"Paris"}}],"stop_reason":"tool_use"}`))
		tc := toolCalls(got.OutputParts)
		if len(tc) != 1 || tc[0].ID != "toolu_1" || tc[0].Name != "get_weather" || !strings.Contains(string(tc[0].Arguments), "Paris") {
			t.Fatalf("tool calls = %+v", tc)
		}
	})

	// Anthropic streaming: tool_use header then input_json_delta fragments.
	t.Run("anthropic stream input_json_delta", func(t *testing.T) {
		t.Parallel()
		sse := "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_7\",\"name\":\"f\"}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"loc\\\":\"}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"Paris\\\"}\"}}\n\n"
		got := genaiattr.ExtractResponse("messages", []byte(sse))
		tc := toolCalls(got.OutputParts)
		if len(tc) != 1 || tc[0].ID != "toolu_7" || tc[0].Name != "f" {
			t.Fatalf("tool calls = %+v", tc)
		}
		if string(tc[0].Arguments) != `{"loc":"Paris"}` {
			t.Errorf("reassembled arguments = %s", tc[0].Arguments)
		}
	})

	// Gemini: functionCall part.
	t.Run("gemini function call", func(t *testing.T) {
		t.Parallel()
		got := genaiattr.ExtractResponse("generate_content", []byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"get_weather","args":{"loc":"Paris"}}}]},"finishReason":"STOP"}]}`))
		tc := toolCalls(got.OutputParts)
		if len(tc) != 1 || tc[0].Name != "get_weather" || !strings.Contains(string(tc[0].Arguments), "Paris") {
			t.Fatalf("tool calls = %+v", tc)
		}
	})

	// Responses non-streaming: function_call output item.
	t.Run("responses function_call item", func(t *testing.T) {
		t.Parallel()
		got := genaiattr.ExtractResponse("responses", []byte(`{"output":[{"type":"function_call","name":"get_weather","arguments":"{\"loc\":\"Paris\"}","call_id":"fc_1"}]}`))
		tc := toolCalls(got.OutputParts)
		if len(tc) != 1 || tc[0].ID != "fc_1" || tc[0].Name != "get_weather" {
			t.Fatalf("tool calls = %+v", tc)
		}
		if string(tc[0].Arguments) != `{"loc":"Paris"}` {
			t.Errorf("arguments = %s", tc[0].Arguments)
		}
	})

	// Non-JSON arguments are wrapped as a JSON string so the value stays
	// well-formed.
	t.Run("openai invalid args wrapped", func(t *testing.T) {
		t.Parallel()
		got := genaiattr.ExtractResponse("chat_completions", []byte(`{"choices":[{"message":{"tool_calls":[{"id":"c","function":{"name":"f","arguments":"not json"}}]}}]}`))
		tc := toolCalls(got.OutputParts)
		if len(tc) != 1 || string(tc[0].Arguments) != `"not json"` {
			t.Fatalf("wrapped args = %s", tc[0].Arguments)
		}
	})

	// A tool call with no arguments yields a nil Arguments (not "").
	t.Run("openai tool call no args", func(t *testing.T) {
		t.Parallel()
		got := genaiattr.ExtractResponse("chat_completions", []byte(`{"choices":[{"message":{"tool_calls":[{"id":"c","function":{"name":"f"}}]}}]}`))
		tc := toolCalls(got.OutputParts)
		if len(tc) != 1 || tc[0].Arguments != nil {
			t.Fatalf("no-arg tool call args = %v", tc[0].Arguments)
		}
	})

	// Responses streaming: output_item.added carries name/call_id, the
	// arguments arrive on function_call_arguments.delta keyed by item_id.
	t.Run("responses stream function call", func(t *testing.T) {
		t.Parallel()
		sse := "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"fc_2\",\"name\":\"f\",\"call_id\":\"call_abc\"}}\n\n" +
			"data: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"fc_2\",\"delta\":\"{\\\"a\\\":1}\"}\n\n"
		got := genaiattr.ExtractResponse("responses", []byte(sse))
		tc := toolCalls(got.OutputParts)
		if len(tc) != 1 || tc[0].Name != "f" || tc[0].ID != "call_abc" {
			t.Fatalf("tool calls = %+v", tc)
		}
		if string(tc[0].Arguments) != `{"a":1}` {
			t.Errorf("arguments = %s", tc[0].Arguments)
		}
	})
}

func TestExtractResponse_EdgeCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		endpoint string
		raw      string
	}{
		{"empty", "chat_completions", ""},
		{"unrecognised endpoint", "models", `{"id":"x"}`},
		{"malformed json", "chat_completions", `{nope`},
		{"sse with only done", "chat_completions", "data: [DONE]\n\n"},
		{"sse malformed frame skipped", "messages", "data: {bad\n\n"},
		{"gemini no usage/candidates", "generate_content", `{"modelVersion":"m"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := genaiattr.ExtractResponse(tc.endpoint, []byte(tc.raw))
			if len(got.FinishReasons) != 0 || got.ReasoningTokens != nil {
				t.Errorf("expected no finish/reasoning, got %+v", got)
			}
			// model may be set in the gemini-no-usage case; that's fine.
			if tc.name == "gemini no usage/candidates" && got.Model != "m" {
				t.Errorf("gemini model = %q, want m", got.Model)
			}
		})
	}
}
