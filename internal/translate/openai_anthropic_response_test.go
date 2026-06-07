package translate

import (
	"encoding/json"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/protocols/openai/chat"
)

// decodeChatResp translates an Anthropic Messages response body and decodes the
// resulting OpenAI Chat response for assertions.
func decodeChatResp(t *testing.T, body string) (chat.ChatCompletionResponse, []Drop) {
	t.Helper()
	out, drops, err := translateMessagesResponseToChat([]byte(body))
	if err != nil {
		t.Fatalf("translateMessagesResponseToChat: %v", err)
	}
	var resp chat.ChatCompletionResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode translated response: %v\nbody: %s", err, out)
	}
	return resp, drops
}

func TestMessagesToChat_TextResponse(t *testing.T) {
	resp, _ := decodeChatResp(t, `{
		"id":"msg_1","type":"message","role":"assistant","model":"claude-x",
		"content":[{"type":"text","text":"hello world"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":5,"output_tokens":3,"service_tier":"standard"}
	}`)

	if resp.ID != "msg_1" {
		t.Errorf("ID = %q, want msg_1", resp.ID)
	}
	if resp.Object != "chat.completion" {
		t.Errorf("Object = %q, want chat.completion", resp.Object)
	}
	if resp.Model != "claude-x" {
		t.Errorf("Model = %q, want claude-x", resp.Model)
	}
	if resp.ServiceTier != "standard" {
		t.Errorf("ServiceTier = %q, want standard", resp.ServiceTier)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	c := resp.Choices[0]
	if c.Message.Role != "assistant" {
		t.Errorf("choice role = %q, want assistant", c.Message.Role)
	}
	var content string
	if err := json.Unmarshal(c.Message.Content, &content); err != nil || content != "hello world" {
		t.Errorf("content = %s (err %v), want 'hello world'", c.Message.Content, err)
	}
	if c.FinishReason == nil || *c.FinishReason != "stop" {
		t.Errorf("finish_reason = %v, want stop", c.FinishReason)
	}
	if resp.Usage == nil || resp.Usage.PromptTokens != 5 || resp.Usage.CompletionTokens != 3 || resp.Usage.TotalTokens != 8 {
		t.Errorf("usage = %+v, want 5/3/8", resp.Usage)
	}
}

func TestMessagesToChat_ToolUse(t *testing.T) {
	resp, _ := decodeChatResp(t, `{
		"id":"msg_2","type":"message","role":"assistant","model":"claude-x",
		"content":[
			{"type":"text","text":"calling"},
			{"type":"tool_use","id":"tu_1","name":"get_weather","input":{"city":"SF"}},
			{"type":"tool_use","id":"tu_2","name":"noargs"}
		],
		"stop_reason":"tool_use",
		"usage":{"input_tokens":1,"output_tokens":1}
	}`)

	c := resp.Choices[0]
	if c.FinishReason == nil || *c.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %v, want tool_calls", c.FinishReason)
	}
	if len(c.Message.ToolCalls) != 2 {
		t.Fatalf("tool_calls = %d, want 2", len(c.Message.ToolCalls))
	}
	if c.Message.ToolCalls[0].ID != "tu_1" || c.Message.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("tool_calls[0] = %+v, want tu_1/get_weather", c.Message.ToolCalls[0])
	}
	if c.Message.ToolCalls[0].Function.Arguments != `{"city":"SF"}` {
		t.Errorf("tool_calls[0] args = %q, want {\"city\":\"SF\"}", c.Message.ToolCalls[0].Function.Arguments)
	}
	if c.Message.ToolCalls[1].Function.Arguments != "{}" {
		t.Errorf("tool_calls[1] args = %q, want {} (empty input)", c.Message.ToolCalls[1].Function.Arguments)
	}
}

func TestMessagesToChat_FinishReasonMapping(t *testing.T) {
	cases := []struct {
		stop string
		want string
		drop bool
	}{
		{"end_turn", "stop", false},
		{"stop_sequence", "stop", false},
		{"max_tokens", "length", false},
		{"tool_use", "tool_calls", false},
		{"odd_reason", "stop", true},
	}
	for _, tc := range cases {
		t.Run(tc.stop, func(t *testing.T) {
			resp, drops := decodeChatResp(t, `{"id":"i","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"x"}],"stop_reason":"`+tc.stop+`","usage":{"input_tokens":1,"output_tokens":1}}`)
			fr := resp.Choices[0].FinishReason
			if fr == nil || *fr != tc.want {
				t.Errorf("finish_reason = %v, want %q", fr, tc.want)
			}
			if tc.drop && !hasDrop(drops, "stop_reason="+tc.stop) {
				t.Errorf("missing unknown stop_reason drop; drops=%+v", drops)
			}
		})
	}
}

func TestMessagesToChat_NilStopReason(t *testing.T) {
	resp, _ := decodeChatResp(t, `{"id":"i","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"x"}],"stop_reason":null,"usage":{"input_tokens":1,"output_tokens":1}}`)
	if resp.Choices[0].FinishReason != nil {
		t.Errorf("finish_reason = %v, want nil for in-progress", resp.Choices[0].FinishReason)
	}
}

func TestMessagesToChat_CachedTokens(t *testing.T) {
	resp, _ := decodeChatResp(t, `{"id":"i","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"x"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":2,"cache_read_input_tokens":7}}`)
	if resp.Usage.PromptTokensDetails == nil || resp.Usage.PromptTokensDetails.CachedTokens != 7 {
		t.Errorf("cached tokens = %+v, want 7", resp.Usage.PromptTokensDetails)
	}
}

func TestMessagesToChat_DroppedBlocksAndFields(t *testing.T) {
	resp, drops := decodeChatResp(t, `{
		"id":"i","type":"message","role":"assistant","model":"m",
		"content":[
			{"type":"text","text":"visible"},
			{"type":"thinking","thinking":"hmm","signature":"sig"},
			{"type":"redacted_thinking","data":"enc"},
			{"type":"image","source":{"type":"url","url":"https://x"}}
		],
		"stop_reason":"end_turn",
		"stop_sequence":"STOP",
		"container":{"id":"c1"},
		"stop_details":{"type":"x"},
		"context_management":{"applied_edits":[]},
		"usage":{"input_tokens":1,"output_tokens":1}
	}`)

	var content string
	_ = json.Unmarshal(resp.Choices[0].Message.Content, &content)
	if content != "visible" {
		t.Errorf("content = %q, want 'visible' (thinking/image excluded)", content)
	}
	for _, f := range []string{
		"content[1].thinking", "content[2].redacted_thinking", "content[3].image",
		"stop_sequence", "container", "stop_details", "context_management",
	} {
		if !hasDrop(drops, f) {
			t.Errorf("missing expected drop %q; drops=%+v", f, drops)
		}
	}
}

func TestMessagesToChat_NoContentNoText(t *testing.T) {
	// A response with only tool_use blocks must omit message content, not emit "".
	out, _, err := translateMessagesResponseToChat([]byte(`{"id":"i","type":"message","role":"assistant","model":"m","content":[{"type":"tool_use","id":"t","name":"f","input":{}}],"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`))
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	var probe struct {
		Choices []struct {
			Message struct {
				Content *string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if probe.Choices[0].Message.Content != nil {
		t.Errorf("content = %v, want omitted/null for tool-only reply", *probe.Choices[0].Message.Content)
	}
}

func TestMessagesToChat_BadResponseBody(t *testing.T) {
	if _, _, err := translateMessagesResponseToChat([]byte(`{nope`)); err == nil {
		t.Error("expected decode error for malformed body")
	}
}

func TestOpenAIAnthropicChat_Registered(t *testing.T) {
	tr, ok := Lookup("chat", "messages")
	if !ok {
		t.Fatal("Lookup(chat, messages) miss; the reverse translator must register itself at init")
	}
	if tr.Source() != "chat" || tr.Target() != "messages" {
		t.Errorf("translator pair = %q->%q, want chat->messages", tr.Source(), tr.Target())
	}

	reqOut, reqDrops, err := tr.TranslateRequest([]byte(`{"model":"m","seed":1,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("TranslateRequest via interface: %v", err)
	}
	if len(reqOut) == 0 || !hasDrop(reqDrops, "seed") {
		t.Errorf("TranslateRequest did not surface bytes + drops: out=%d drops=%+v", len(reqOut), reqDrops)
	}

	respOut, _, err := tr.TranslateResponse([]byte(`{"id":"x","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	if err != nil {
		t.Fatalf("TranslateResponse via interface: %v", err)
	}
	if len(respOut) == 0 {
		t.Error("TranslateResponse returned no bytes")
	}

	sc, ok := tr.(StreamCapable)
	if !ok {
		t.Fatal("reverse translator must implement StreamCapable")
	}
	if sc.NewStreamTranslator() == nil {
		t.Error("NewStreamTranslator returned nil")
	}

	et, ok := tr.(ErrorTranslator)
	if !ok {
		t.Fatal("reverse translator must implement ErrorTranslator")
	}
	errOut, err := et.TranslateError(429, []byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow"}}`))
	if err != nil || len(errOut) == 0 {
		t.Fatalf("TranslateError via interface: out=%d err=%v", len(errOut), err)
	}
}

func TestMessagesToChat_MalformedAssistantContentInResponse(t *testing.T) {
	// content blocks array that is not block objects is a hard error.
	if _, _, err := translateMessagesResponseToChat([]byte(`{"id":"x","type":"message","role":"assistant","model":"m","content":[1,2,3],"usage":{"input_tokens":1,"output_tokens":1}}`)); err == nil {
		t.Error("expected error for malformed content blocks array")
	}
}
