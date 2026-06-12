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
		// A plain tool_use is client-executed (distinct from server_tool_use).
		if tc[0].Executor != "client" {
			t.Errorf("tool_use executor = %q, want client", tc[0].Executor)
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

func TestExtractResponse_Anthropic_ServerTools(t *testing.T) {
	t.Parallel()

	// Non-streaming: server_tool_use + web_search_tool_result + text, modeled
	// on real captured traffic (srvtoolu_ id prefix, web_search name, query
	// input, result entries with title/url/encrypted_content/page_age). The
	// parts must preserve wire order and the result must drop the bulky
	// encrypted_content blobs.
	t.Run("non-stream web_search round", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`{"id":"msg_1","model":"claude-opus-4-8","content":[` +
			`{"type":"server_tool_use","id":"srvtoolu_01AAA","name":"web_search","input":{"query":"julien thibeaut"}},` +
			`{"type":"web_search_tool_result","tool_use_id":"srvtoolu_01AAA","content":[` +
			`{"type":"web_search_result","title":"Site One","url":"https://example.com/one","encrypted_content":"OPAQUEBLOB1","page_age":null},` +
			`{"type":"web_search_result","title":"Site Two","url":"https://example.com/two","encrypted_content":"OPAQUEBLOB2","page_age":"April 30, 2025"}` +
			`],"caller":{"type":"direct"}},` +
			`{"type":"text","text":"Found it."}],"stop_reason":"end_turn"}`)
		got := genaiattr.ExtractResponse("messages", raw)
		if len(got.OutputParts) != 3 {
			t.Fatalf("parts = %+v, want [tool_call, tool_call_response, text]", got.OutputParts)
		}
		call := got.OutputParts[0]
		if call.Type != "tool_call" || call.ID != "srvtoolu_01AAA" || call.Name != "web_search" {
			t.Errorf("part0 = %+v, want tool_call srvtoolu_01AAA/web_search", call)
		}
		if call.Executor != "server" {
			t.Errorf("server_tool_use executor = %q, want server", call.Executor)
		}
		if string(call.Arguments) != `{"query":"julien thibeaut"}` {
			t.Errorf("arguments = %s", call.Arguments)
		}
		res := got.OutputParts[1]
		if res.Type != "tool_call_response" || res.ID != "srvtoolu_01AAA" {
			t.Errorf("part1 = %+v, want tool_call_response srvtoolu_01AAA", res)
		}
		if res.Executor != "server" {
			t.Errorf("*_tool_result executor = %q, want server", res.Executor)
		}
		if strings.Contains(res.Result, "OPAQUEBLOB") {
			t.Errorf("encrypted_content survived into result: %q", res.Result)
		}
		for _, want := range []string{"Site One", "https://example.com/one", "Site Two", "April 30, 2025"} {
			if !strings.Contains(res.Result, want) {
				t.Errorf("result = %q, want it to contain %q", res.Result, want)
			}
		}
		if got.OutputParts[2].Type != "text" || got.OutputParts[2].Content != "Found it." {
			t.Errorf("part2 = %+v, want text part", got.OutputParts[2])
		}
	})

	// Streaming: server_tool_use streams like tool_use (content_block_start
	// header with empty input, then input_json_delta fragments); the
	// *_tool_result block arrives whole inside content_block_start with no
	// deltas. Mirrors the captured SSE shape, including pause_turn-style
	// stop_reason passthrough.
	t.Run("stream web_search round", func(t *testing.T) {
		t.Parallel()
		raw := []byte("" +
			"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_s\",\"model\":\"claude-opus-4-8\"}}\n\n" +
			"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"server_tool_use\",\"id\":\"srvtoolu_01BBB\",\"name\":\"web_search\",\"input\":{}}}\n\n" +
			"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\"}}\n\n" +
			"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"quer\"}}\n\n" +
			"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"y\\\": \\\"ruvnet github\\\"}\"}}\n\n" +
			"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
			"data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"web_search_tool_result\",\"tool_use_id\":\"srvtoolu_01BBB\",\"content\":[{\"type\":\"web_search_result\",\"title\":\"rUv\",\"url\":\"https://example.com/ruv\",\"encrypted_content\":\"OPAQUEBLOB\",\"page_age\":null}],\"caller\":{\"type\":\"direct\"}}}\n\n" +
			"data: {\"type\":\"content_block_stop\",\"index\":1}\n\n" +
			"data: {\"type\":\"content_block_start\",\"index\":2,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
			"data: {\"type\":\"content_block_delta\",\"index\":2,\"delta\":{\"type\":\"text_delta\",\"text\":\"summary\"}}\n\n" +
			"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"pause_turn\"},\"usage\":{\"output_tokens\":42}}\n\n")
		got := genaiattr.ExtractResponse("messages", raw)
		if len(got.OutputParts) != 3 {
			t.Fatalf("parts = %+v, want [tool_call, tool_call_response, text]", got.OutputParts)
		}
		call := got.OutputParts[0]
		if call.Type != "tool_call" || call.ID != "srvtoolu_01BBB" || call.Name != "web_search" {
			t.Errorf("part0 = %+v", call)
		}
		if string(call.Arguments) != `{"query": "ruvnet github"}` {
			t.Errorf("reassembled arguments = %s", call.Arguments)
		}
		res := got.OutputParts[1]
		if res.Type != "tool_call_response" || res.ID != "srvtoolu_01BBB" {
			t.Errorf("part1 = %+v", res)
		}
		if strings.Contains(res.Result, "OPAQUEBLOB") || !strings.Contains(res.Result, "https://example.com/ruv") {
			t.Errorf("result = %q, want url kept and encrypted_content stripped", res.Result)
		}
		if got.OutputParts[2].Type != "text" || got.OutputParts[2].Content != "summary" {
			t.Errorf("part2 = %+v", got.OutputParts[2])
		}
		if len(got.FinishReasons) != 1 || got.FinishReasons[0] != "pause_turn" {
			t.Errorf("finish_reasons = %v, want [pause_turn]", got.FinishReasons)
		}
	})

	// Any future *_tool_result block maps generically by suffix — no
	// per-tool allowlist. String content passes through as plain text.
	t.Run("unknown foo_tool_result maps generically", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`{"content":[` +
			`{"type":"server_tool_use","id":"srvtoolu_X","name":"foo","input":{"arg":1}},` +
			`{"type":"foo_tool_result","tool_use_id":"srvtoolu_X","content":"plain result text"}]}`)
		got := genaiattr.ExtractResponse("messages", raw)
		if len(got.OutputParts) != 2 {
			t.Fatalf("parts = %+v, want [tool_call, tool_call_response]", got.OutputParts)
		}
		if got.OutputParts[0].Type != "tool_call" || got.OutputParts[0].Name != "foo" {
			t.Errorf("part0 = %+v", got.OutputParts[0])
		}
		res := got.OutputParts[1]
		if res.Type != "tool_call_response" || res.ID != "srvtoolu_X" || res.Result != "plain result text" {
			t.Errorf("part1 = %+v", res)
		}
	})

	// Error results: content is an object, not an array — compacted as JSON.
	t.Run("tool_result error object", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`{"content":[{"type":"web_search_tool_result","tool_use_id":"srvtoolu_E","content":{"type":"web_search_tool_result_error","error_code":"max_uses_exceeded"}}]}`)
		got := genaiattr.ExtractResponse("messages", raw)
		if len(got.OutputParts) != 1 || got.OutputParts[0].Type != "tool_call_response" {
			t.Fatalf("parts = %+v", got.OutputParts)
		}
		if !strings.Contains(got.OutputParts[0].Result, "max_uses_exceeded") {
			t.Errorf("result = %q, want error_code preserved", got.OutputParts[0].Result)
		}
	})

	// mcp_tool_use is the server-executed MCP call — same shape as tool_use.
	t.Run("mcp_tool_use and mcp_tool_result", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`{"content":[` +
			`{"type":"mcp_tool_use","id":"mcptoolu_1","name":"list_issues","input":{"repo":"a/b"}},` +
			`{"type":"mcp_tool_result","tool_use_id":"mcptoolu_1","content":[{"type":"text","text":"3 open issues"}]}]}`)
		got := genaiattr.ExtractResponse("messages", raw)
		if len(got.OutputParts) != 2 {
			t.Fatalf("parts = %+v", got.OutputParts)
		}
		if got.OutputParts[0].Type != "tool_call" || got.OutputParts[0].ID != "mcptoolu_1" || got.OutputParts[0].Name != "list_issues" {
			t.Errorf("part0 = %+v", got.OutputParts[0])
		}
		if got.OutputParts[1].Type != "tool_call_response" || !strings.Contains(got.OutputParts[1].Result, "3 open issues") {
			t.Errorf("part1 = %+v", got.OutputParts[1])
		}
	})

	// An unrecognised future block type degrades to a bare typed part —
	// never a silent drop.
	t.Run("unknown block degrades to typed part", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`{"content":[{"type":"future_server_block","payload":"x"},{"type":"text","text":"hi"}]}`)
		got := genaiattr.ExtractResponse("messages", raw)
		if len(got.OutputParts) != 2 {
			t.Fatalf("parts = %+v, want [future_server_block, text]", got.OutputParts)
		}
		if got.OutputParts[0].Type != "future_server_block" {
			t.Errorf("part0 = %+v, want bare typed part", got.OutputParts[0])
		}
	})
}

func TestExtractResponse_OpenAIResponses_ServerTools(t *testing.T) {
	t.Parallel()

	// Non-streaming: a built-in tool mix interleaved with a function call and
	// the assistant message. Every *_call item maps to a tool_call part named
	// by the type minus "_call"; file_search carries inline (include-gated)
	// results, which pair as a tool_call_response with the same id.
	t.Run("non-stream mixed builtin items", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`{"id":"resp_1","model":"gpt-5.2","output":[` +
			`{"type":"web_search_call","id":"ws_1","status":"completed","action":{"type":"search","query":"positive news"}},` +
			`{"type":"file_search_call","id":"fs_1","status":"completed","queries":["deep research"],"results":[{"file_id":"file-1","filename":"blog.pdf","score":0.95}]},` +
			`{"type":"function_call","id":"fc_1","call_id":"call_1","name":"get_weather","arguments":"{\"loc\":\"Paris\"}"},` +
			`{"type":"message","content":[{"type":"output_text","text":"done"}]}]}`)
		got := genaiattr.ExtractResponse("responses", raw)
		if len(got.OutputParts) != 5 {
			t.Fatalf("parts = %+v, want [text, ws call, fs call, fs response, fn call]", got.OutputParts)
		}
		if got.OutputParts[0].Type != "text" || got.OutputParts[0].Content != "done" {
			t.Errorf("part0 = %+v, want the message text", got.OutputParts[0])
		}
		ws := got.OutputParts[1]
		if ws.Type != "tool_call" || ws.ID != "ws_1" || ws.Name != "web_search" {
			t.Errorf("web_search part = %+v", ws)
		}
		// web_search is provider-hosted: it must be flagged server even though
		// its result folds into the model output (no same-span response). This
		// is the executor signal the console relies on to bucket it correctly.
		if ws.Executor != "server" {
			t.Errorf("web_search executor = %q, want server", ws.Executor)
		}
		if string(ws.Arguments) != `{"action":{"type":"search","query":"positive news"}}` {
			t.Errorf("web_search arguments = %s", ws.Arguments)
		}
		fs := got.OutputParts[2]
		if fs.Type != "tool_call" || fs.ID != "fs_1" || fs.Name != "file_search" {
			t.Errorf("file_search part = %+v", fs)
		}
		if fs.Executor != "server" {
			t.Errorf("file_search executor = %q, want server", fs.Executor)
		}
		if string(fs.Arguments) != `{"queries":["deep research"]}` {
			t.Errorf("file_search arguments = %s", fs.Arguments)
		}
		fsRes := got.OutputParts[3]
		if fsRes.Type != "tool_call_response" || fsRes.ID != "fs_1" {
			t.Errorf("file_search response part = %+v", fsRes)
		}
		if !strings.Contains(fsRes.Result, "blog.pdf") || !strings.Contains(fsRes.Result, "file-1") {
			t.Errorf("file_search result = %q", fsRes.Result)
		}
		fn := got.OutputParts[4]
		if fn.Type != "tool_call" || fn.ID != "call_1" || fn.Name != "get_weather" || string(fn.Arguments) != `{"loc":"Paris"}` {
			t.Errorf("function_call part = %+v", fn)
		}
		// function_call is client-executed — the caller runs it and returns the
		// result on a later turn.
		if fn.Executor != "client" {
			t.Errorf("function_call executor = %q, want client", fn.Executor)
		}
	})

	// Streaming: the item arrives on output_item.added (in_progress, bare),
	// is completed on output_item.done, and response.completed repeats the
	// whole output — the merges must stay idempotent (one part, final args).
	t.Run("stream web_search_call idempotent across events", func(t *testing.T) {
		t.Parallel()
		sse := "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"web_search_call\",\"id\":\"ws_9\",\"status\":\"in_progress\"}}\n\n" +
			"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"web_search_call\",\"id\":\"ws_9\",\"status\":\"completed\",\"action\":{\"type\":\"open_page\",\"url\":\"https://example.com/a\"}}}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_9\",\"output\":[{\"type\":\"web_search_call\",\"id\":\"ws_9\",\"status\":\"completed\",\"action\":{\"type\":\"open_page\",\"url\":\"https://example.com/a\"}}]}}\n\n"
		got := genaiattr.ExtractResponse("responses", []byte(sse))
		tc := toolCalls(got.OutputParts)
		if len(tc) != 1 || tc[0].ID != "ws_9" || tc[0].Name != "web_search" {
			t.Fatalf("tool calls = %+v, want exactly one web_search", tc)
		}
		if string(tc[0].Arguments) != `{"action":{"type":"open_page","url":"https://example.com/a"}}` {
			t.Errorf("arguments = %s", tc[0].Arguments)
		}
	})

	// mcp_call: the part keeps the family name (type minus _call); the MCP
	// tool's own name, decoded arguments, and server label ride inside the
	// argument object; the output string pairs as a tool_call_response.
	t.Run("mcp_call with output", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`{"output":[{"type":"mcp_call","id":"mcp_1","name":"roll_dice","server_label":"dice","arguments":"{\"sides\":6}","output":"4"}]}`)
		got := genaiattr.ExtractResponse("responses", raw)
		if len(got.OutputParts) != 2 {
			t.Fatalf("parts = %+v, want [tool_call, tool_call_response]", got.OutputParts)
		}
		call := got.OutputParts[0]
		if call.Type != "tool_call" || call.ID != "mcp_1" || call.Name != "mcp" {
			t.Errorf("part0 = %+v", call)
		}
		if string(call.Arguments) != `{"name":"roll_dice","arguments":{"sides":6},"server_label":"dice"}` {
			t.Errorf("arguments = %s", call.Arguments)
		}
		if got.OutputParts[1].Type != "tool_call_response" || got.OutputParts[1].ID != "mcp_1" || got.OutputParts[1].Result != "4" {
			t.Errorf("part1 = %+v", got.OutputParts[1])
		}
	})

	// computer_call prefers call_id (the transcript-correlating id, matching
	// the function_call convention); code_interpreter_call carries code +
	// container_id as its arguments; an unknown future *_call still maps.
	t.Run("computer, code_interpreter, future calls", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`{"output":[` +
			`{"type":"computer_call","id":"cu_1","call_id":"call_z","status":"completed","action":{"type":"click","button":"left","x":156,"y":50}},` +
			`{"type":"code_interpreter_call","id":"ci_1","status":"completed","container_id":"cntr_1","code":"print(1)"},` +
			`{"type":"hologram_call","id":"holo_1","status":"completed"}]}`)
		got := genaiattr.ExtractResponse("responses", raw)
		tc := toolCalls(got.OutputParts)
		if len(tc) != 3 {
			t.Fatalf("tool calls = %+v, want 3", tc)
		}
		if tc[0].ID != "call_z" || tc[0].Name != "computer" || !strings.Contains(string(tc[0].Arguments), `"click"`) {
			t.Errorf("computer part = %+v", tc[0])
		}
		if tc[1].ID != "ci_1" || tc[1].Name != "code_interpreter" || string(tc[1].Arguments) != `{"code":"print(1)","container_id":"cntr_1"}` {
			t.Errorf("code_interpreter part = %+v", tc[1])
		}
		if tc[2].ID != "holo_1" || tc[2].Name != "hologram" || tc[2].Arguments != nil {
			t.Errorf("future *_call part = %+v", tc[2])
		}
	})

	// A failed call with no inline result surfaces the failure as a
	// status-marker tool_call_response so it doesn't vanish.
	t.Run("failed call emits status response", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`{"output":[{"type":"web_search_call","id":"ws_f","status":"failed","action":{"type":"search","query":"q"}}]}`)
		got := genaiattr.ExtractResponse("responses", raw)
		if len(got.OutputParts) != 2 {
			t.Fatalf("parts = %+v, want [tool_call, tool_call_response]", got.OutputParts)
		}
		if got.OutputParts[1].Type != "tool_call_response" || got.OutputParts[1].Result != `{"status":"failed"}` {
			t.Errorf("part1 = %+v", got.OutputParts[1])
		}
	})

	// mcp_list_tools / mcp_approval_request have no _call suffix but are
	// still server-tool activity; the type itself is the part name.
	t.Run("mcp_list_tools keeps full type as name", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`{"output":[{"type":"mcp_list_tools","id":"mcpl_1","server_label":"dice"}]}`)
		got := genaiattr.ExtractResponse("responses", raw)
		tc := toolCalls(got.OutputParts)
		if len(tc) != 1 || tc[0].Name != "mcp_list_tools" || string(tc[0].Arguments) != `{"server_label":"dice"}` {
			t.Fatalf("tool calls = %+v", tc)
		}
	})
}

// Chat Completions has no server-tool output items (search-preview models
// return plain text with annotations) — the extractor must keep emitting
// exactly [text, tool_call...] with no response parts.
func TestExtractResponse_OpenAIChat_NoServerToolParts(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"id":"c1","model":"gpt-4o-search-preview","choices":[{"message":{"content":"cited answer","annotations":[{"type":"url_citation","url_citation":{"url":"https://example.com","title":"t"}}],"tool_calls":[{"id":"call_1","function":{"name":"f","arguments":"{}"}}]},"finish_reason":"stop"}]}`)
	got := genaiattr.ExtractResponse("chat_completions", raw)
	if len(got.OutputParts) != 2 {
		t.Fatalf("parts = %+v, want [text, tool_call] only", got.OutputParts)
	}
	if got.OutputParts[0].Type != "text" || got.OutputParts[1].Type != "tool_call" {
		t.Errorf("parts = %+v", got.OutputParts)
	}
	for _, p := range got.OutputParts {
		if p.Type == "tool_call_response" {
			t.Errorf("unexpected tool_call_response part on chat: %+v", p)
		}
	}
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
