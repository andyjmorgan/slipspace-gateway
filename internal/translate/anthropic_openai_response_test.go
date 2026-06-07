package translate

import (
	"encoding/json"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/protocols/anthropic/messages"
)

// decodeMessages translates an OpenAI Chat response body and decodes the
// resulting Anthropic Messages response for assertions.
func decodeMessages(t *testing.T, body string) (messages.MessagesResponse, []Drop) {
	t.Helper()
	out, drops, err := translateChatResponseToMessages([]byte(body))
	if err != nil {
		t.Fatalf("translateChatResponseToMessages: %v", err)
	}
	var resp messages.MessagesResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode translated response: %v\nbody: %s", err, out)
	}
	return resp, drops
}

func TestTranslateResponse_TextAndUsage(t *testing.T) {
	resp, drops := decodeMessages(t, `{
		"id":"chatcmpl-1","model":"gpt-4o","service_tier":"default",
		"choices":[{"index":0,"message":{"role":"assistant","content":"hi there"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":11,"completion_tokens":4,"total_tokens":15}
	}`)

	if resp.ID != "chatcmpl-1" || resp.Type != "message" || resp.Role != "assistant" {
		t.Errorf("envelope = %q/%q/%q, want chatcmpl-1/message/assistant", resp.ID, resp.Type, resp.Role)
	}
	if resp.Model != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o", resp.Model)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("content blocks = %d, want 1", len(resp.Content))
	}
	tb, ok := resp.Content[0].(*messages.TextBlock)
	if !ok || tb.Text != "hi there" {
		t.Errorf("content[0] = %+v, want TextBlock 'hi there'", resp.Content[0])
	}
	if resp.StopReason == nil || *resp.StopReason != "end_turn" {
		t.Errorf("stop_reason = %v, want end_turn", resp.StopReason)
	}
	if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 4 {
		t.Errorf("usage = %d/%d, want 11/4", resp.Usage.InputTokens, resp.Usage.OutputTokens)
	}
	if resp.Usage.ServiceTier != "default" {
		t.Errorf("usage.service_tier = %q, want default", resp.Usage.ServiceTier)
	}
	if len(drops) != 0 {
		t.Errorf("unexpected drops: %+v", drops)
	}
}

func TestTranslateResponse_FinishReasonMapping(t *testing.T) {
	tests := []struct {
		openai   string
		want     string
		wantDrop bool
	}{
		{`"stop"`, "end_turn", false},
		{`"length"`, "max_tokens", false},
		{`"tool_calls"`, "tool_use", false},
		{`"function_call"`, "tool_use", false},
		{`"content_filter"`, "end_turn", true},
		{`"some_future_reason"`, "end_turn", true},
	}
	for _, tt := range tests {
		t.Run(tt.openai, func(t *testing.T) {
			resp, drops := decodeMessages(t, `{"id":"x","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"y"},"finish_reason":`+tt.openai+`}]}`)
			if resp.StopReason == nil || *resp.StopReason != tt.want {
				t.Errorf("stop_reason = %v, want %s", resp.StopReason, tt.want)
			}
			gotDrop := len(drops) > 0
			if gotDrop != tt.wantDrop {
				t.Errorf("drops = %+v, wantDrop = %v", drops, tt.wantDrop)
			}
		})
	}
}

func TestTranslateResponse_NilFinishReason(t *testing.T) {
	resp, _ := decodeMessages(t, `{"id":"x","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"y"},"finish_reason":null}]}`)
	if resp.StopReason != nil {
		t.Errorf("stop_reason = %v, want nil for null finish_reason", *resp.StopReason)
	}
}

func TestTranslateResponse_ToolCalls(t *testing.T) {
	resp, _ := decodeMessages(t, `{
		"id":"x","model":"m",
		"choices":[{"index":0,"message":{"role":"assistant","content":"calling","tool_calls":[
			{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}
		]},"finish_reason":"tool_calls"}]
	}`)
	if len(resp.Content) != 2 {
		t.Fatalf("content blocks = %d, want 2 (text + tool_use)", len(resp.Content))
	}
	if _, ok := resp.Content[0].(*messages.TextBlock); !ok {
		t.Errorf("content[0] = %T, want TextBlock", resp.Content[0])
	}
	tu, ok := resp.Content[1].(*messages.ToolUseBlock)
	if !ok {
		t.Fatalf("content[1] = %T, want ToolUseBlock", resp.Content[1])
	}
	if tu.ID != "call_1" || tu.Name != "get_weather" {
		t.Errorf("tool_use = %q/%q, want call_1/get_weather", tu.ID, tu.Name)
	}
	var input map[string]string
	if err := json.Unmarshal(tu.Input, &input); err != nil || input["city"] != "SF" {
		t.Errorf("tool_use input = %s (err %v), want {city:SF} object", tu.Input, err)
	}
	if resp.StopReason == nil || *resp.StopReason != "tool_use" {
		t.Errorf("stop_reason = %v, want tool_use", resp.StopReason)
	}
}

func TestTranslateResponse_ToolCallEmptyArgs(t *testing.T) {
	resp, _ := decodeMessages(t, `{"id":"x","model":"m","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":""}}]},"finish_reason":"tool_calls"}]}`)
	tu := resp.Content[0].(*messages.ToolUseBlock)
	if string(tu.Input) != "{}" {
		t.Errorf("empty args input = %s, want {}", tu.Input)
	}
}

func TestTranslateResponse_Drops(t *testing.T) {
	_, drops := decodeMessages(t, `{
		"id":"x","model":"m","system_fingerprint":"fp_1",
		"choices":[
			{"index":0,"message":{"role":"assistant","content":"a","refusal":"I cannot","audio":{"id":"au1"}},"finish_reason":"stop","logprobs":{"content":[]}},
			{"index":1,"message":{"role":"assistant","content":"b"},"finish_reason":"stop"}
		]
	}`)
	for _, want := range []string{"system_fingerprint", "choices[1:]", "choices[0].logprobs", "choices[0].message.refusal", "choices[0].message.audio"} {
		if !hasDrop(drops, want) {
			t.Errorf("expected drop %q, got %+v", want, drops)
		}
	}
}

func TestTranslateResponse_ContentParts(t *testing.T) {
	resp, drops := decodeMessages(t, `{
		"id":"x","model":"m",
		"choices":[{"index":0,"message":{"role":"assistant","content":[
			{"type":"text","text":"hello"},
			{"type":"refusal","refusal":"no"}
		]},"finish_reason":"stop"}]
	}`)
	if len(resp.Content) != 2 {
		t.Fatalf("content = %d, want 2", len(resp.Content))
	}
	if tb, ok := resp.Content[0].(*messages.TextBlock); !ok || tb.Text != "hello" {
		t.Errorf("content[0] = %+v, want TextBlock hello", resp.Content[0])
	}
	if tb, ok := resp.Content[1].(*messages.TextBlock); !ok || tb.Text != "no" {
		t.Errorf("content[1] = %+v, want TextBlock from refusal", resp.Content[1])
	}
	if !hasDrop(drops, "choices[0].message.content[1].refusal") {
		t.Errorf("expected refusal-part drop, got %+v", drops)
	}
}

func TestTranslateResponse_CachedTokens(t *testing.T) {
	resp, _ := decodeMessages(t, `{"id":"x","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"y"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":80}}}`)
	if resp.Usage.CacheReadInputTokens == nil || *resp.Usage.CacheReadInputTokens != 80 {
		t.Errorf("cache_read_input_tokens = %v, want 80", resp.Usage.CacheReadInputTokens)
	}
}

func TestTranslateResponse_NoChoicesNoUsage(t *testing.T) {
	resp, drops := decodeMessages(t, `{"id":"x","model":"m","choices":[]}`)
	if len(resp.Content) != 0 {
		t.Errorf("content = %d, want 0 for no choices", len(resp.Content))
	}
	if resp.StopReason != nil {
		t.Errorf("stop_reason = %v, want nil", *resp.StopReason)
	}
	if resp.Usage.InputTokens != 0 || resp.Usage.OutputTokens != 0 {
		t.Errorf("usage = %d/%d, want 0/0 for absent usage", resp.Usage.InputTokens, resp.Usage.OutputTokens)
	}
	if len(drops) != 0 {
		t.Errorf("unexpected drops: %+v", drops)
	}
}

func TestTranslateResponse_InvalidBody(t *testing.T) {
	if _, _, err := translateChatResponseToMessages([]byte(`{nope`)); err == nil {
		t.Error("expected error on invalid JSON response")
	}
}

func TestTranslateResponse_UnknownContentPartDropped(t *testing.T) {
	_, drops := decodeMessages(t, `{"id":"x","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":[{"type":"text","text":"a"},{"type":"image_url","image_url":{"url":"u"}}]},"finish_reason":"stop"}]}`)
	if !hasDrop(drops, "choices[0].message.content[1].image_url") {
		t.Errorf("expected unknown-part drop, got %+v", drops)
	}
}

func TestTranslateResponse_MalformedContentParts(t *testing.T) {
	// content that is a JSON array but not of content-part objects is a hard
	// error, not a silent drop.
	if _, _, err := translateChatResponseToMessages([]byte(`{"id":"x","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":[1,2,3]},"finish_reason":"stop"}]}`)); err == nil {
		t.Error("expected error for malformed content parts array")
	}
}

func TestAnthropicOpenAIChat_Registered(t *testing.T) {
	tr, ok := Lookup("messages", "chat")
	if !ok {
		t.Fatal("Lookup(messages, chat) miss; the translator must register itself at init")
	}
	if tr.Source() != "messages" || tr.Target() != "chat" {
		t.Errorf("translator pair = %q->%q, want messages->chat", tr.Source(), tr.Target())
	}

	reqOut, reqDrops, err := tr.TranslateRequest([]byte(`{"model":"m","max_tokens":1,"top_k":5,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("TranslateRequest via interface: %v", err)
	}
	if len(reqOut) == 0 || !hasDrop(reqDrops, "top_k") {
		t.Errorf("TranslateRequest did not surface bytes + drops: out=%d drops=%+v", len(reqOut), reqDrops)
	}

	respOut, _, err := tr.TranslateResponse([]byte(`{"id":"x","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`))
	if err != nil {
		t.Fatalf("TranslateResponse via interface: %v", err)
	}
	if len(respOut) == 0 {
		t.Error("TranslateResponse returned no bytes")
	}
}
