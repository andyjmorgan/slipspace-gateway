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
