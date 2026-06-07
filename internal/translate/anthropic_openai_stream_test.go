package translate

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

// errReader fails every Read, to exercise the streaming reader's error path.
type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

func TestStream_ReaderPropagatesUpstreamError(t *testing.T) {
	boom := errors.New("upstream boom")
	tr := anthropicOpenAIChat{}.NewStreamTranslator()
	rc := NewStreamingReader(io.NopCloser(errReader{err: boom}), tr)
	if _, err := io.ReadAll(rc); !errors.Is(err, boom) {
		t.Fatalf("read err = %v, want upstream boom", err)
	}
	_ = rc.Close()
}

type sseEvent struct {
	typ  string
	data []byte
}

// runStream feeds an OpenAI SSE body through the (messages,chat) streaming
// translator and parses the resulting Anthropic SSE events.
func runStream(t *testing.T, openaiSSE string) []sseEvent {
	t.Helper()
	tr := anthropicOpenAIChat{}.NewStreamTranslator()
	rc := NewStreamingReader(io.NopCloser(strings.NewReader(openaiSSE)), tr)
	out, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read translated stream: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return parseSSE(t, string(out))
}

func parseSSE(t *testing.T, raw string) []sseEvent {
	t.Helper()
	var events []sseEvent
	for _, block := range strings.Split(raw, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		var ev sseEvent
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				ev.typ = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.data = []byte(strings.TrimPrefix(line, "data: "))
			}
		}
		events = append(events, ev)
	}
	return events
}

func types(events []sseEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.typ
	}
	return out
}

func eqTypes(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestStream_TextOnly(t *testing.T) {
	in := `data: {"id":"chatcmpl-1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant"}}]}

data: {"choices":[{"index":0,"delta":{"content":"Hello"}}]}

data: {"choices":[{"index":0,"delta":{"content":" world"}}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`
	ev := runStream(t, in)
	want := []string{"message_start", "content_block_start", "content_block_delta", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if !eqTypes(types(ev), want) {
		t.Fatalf("event types = %v\nwant %v", types(ev), want)
	}

	// message_start carries id + model.
	var ms struct {
		Message struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Role  string `json:"role"`
		} `json:"message"`
	}
	mustJSON(t, ev[0].data, &ms)
	if ms.Message.ID != "chatcmpl-1" || ms.Message.Model != "gpt-4o" || ms.Message.Role != "assistant" {
		t.Errorf("message_start = %+v, want chatcmpl-1/gpt-4o/assistant", ms.Message)
	}

	// content_block_start is a text block at index 0.
	var cbs struct {
		Index        int `json:"index"`
		ContentBlock struct {
			Type string `json:"type"`
		} `json:"content_block"`
	}
	mustJSON(t, ev[1].data, &cbs)
	if cbs.Index != 0 || cbs.ContentBlock.Type != "text" {
		t.Errorf("content_block_start = %+v, want index 0 text", cbs)
	}

	if got := deltaText(t, ev[2].data); got != "Hello" {
		t.Errorf("delta[0] text = %q, want Hello", got)
	}
	if got := deltaText(t, ev[3].data); got != " world" {
		t.Errorf("delta[1] text = %q, want ' world'", got)
	}

	var md struct {
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
	}
	mustJSON(t, ev[5].data, &md)
	if md.Delta.StopReason != "end_turn" {
		t.Errorf("message_delta stop_reason = %q, want end_turn", md.Delta.StopReason)
	}
}

func TestStream_ToolCallWithUsage(t *testing.T) {
	in := `data: {"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant"}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"SF\"}"}}]}}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19}}

data: [DONE]

`
	ev := runStream(t, in)
	want := []string{"message_start", "content_block_start", "content_block_delta", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if !eqTypes(types(ev), want) {
		t.Fatalf("event types = %v\nwant %v", types(ev), want)
	}

	// tool_use block start at index 0 with id + name.
	var cbs struct {
		Index        int `json:"index"`
		ContentBlock struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content_block"`
	}
	mustJSON(t, ev[1].data, &cbs)
	if cbs.ContentBlock.Type != "tool_use" || cbs.ContentBlock.ID != "call_1" || cbs.ContentBlock.Name != "get_weather" {
		t.Errorf("tool_use start = %+v, want tool_use/call_1/get_weather", cbs.ContentBlock)
	}

	// argument fragments arrive as input_json_delta and concatenate to valid JSON.
	args := deltaPartialJSON(t, ev[2].data) + deltaPartialJSON(t, ev[3].data)
	if args != `{"city":"SF"}` {
		t.Errorf("concatenated args = %q, want {\"city\":\"SF\"}", args)
	}

	var md struct {
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	mustJSON(t, ev[5].data, &md)
	if md.Delta.StopReason != "tool_use" {
		t.Errorf("stop_reason = %q, want tool_use", md.Delta.StopReason)
	}
	if md.Usage.InputTokens != 12 || md.Usage.OutputTokens != 7 {
		t.Errorf("usage = %d/%d, want 12/7", md.Usage.InputTokens, md.Usage.OutputTokens)
	}
}

func TestStream_EmptyUpstreamStillFramesMessage(t *testing.T) {
	ev := runStream(t, ``)
	want := []string{"message_start", "message_delta", "message_stop"}
	if !eqTypes(types(ev), want) {
		t.Errorf("empty stream event types = %v, want %v", types(ev), want)
	}
}

func TestStream_TrailingEventWithoutBlankLine(t *testing.T) {
	// No trailing blank line after the last data line — the reader must still
	// translate it.
	in := "data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}"
	ev := runStream(t, in)
	if len(ev) == 0 || ev[0].typ != "message_start" {
		t.Fatalf("expected message_start, got %v", types(ev))
	}
	if got := types(ev); got[len(got)-1] != "message_stop" {
		t.Errorf("last event = %q, want message_stop", got[len(got)-1])
	}
}

func TestStream_SkipsUndecodableAndEmptyDeltas(t *testing.T) {
	in := `data: not-json-at-all

data: {"id":"x","model":"m","choices":[{"index":0,"delta":{"role":"assistant"}}]}

data: {"choices":[{"index":0,"delta":{"content":""}}]}

data: {"choices":[{"index":0,"delta":{"content":123}}]}

data: {"choices":[{"index":0,"delta":{"content":"real"}}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

`
	ev := runStream(t, in)
	want := []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if !eqTypes(types(ev), want) {
		t.Fatalf("event types = %v\nwant %v", types(ev), want)
	}
	if got := deltaText(t, ev[2].data); got != "real" {
		t.Errorf("delta text = %q, want real (empty/non-string/undecodable skipped)", got)
	}
}

func TestStream_TextThenToolCall(t *testing.T) {
	in := `data: {"id":"x","model":"m","choices":[{"index":0,"delta":{"content":"thinking"}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"t1","type":"function","function":{"name":"f","arguments":"{}"}}]}}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

`
	ev := runStream(t, in)
	want := []string{
		"message_start",
		"content_block_start", "content_block_delta", "content_block_stop", // text block 0
		"content_block_start", "content_block_delta", "content_block_stop", // tool block 1
		"message_delta", "message_stop",
	}
	if !eqTypes(types(ev), want) {
		t.Fatalf("event types = %v\nwant %v", types(ev), want)
	}
	// The text block is index 0 and the tool block is index 1 (the translator
	// owns indices, so they never collide).
	idx := func(data []byte) int {
		var d struct {
			Index int `json:"index"`
		}
		mustJSON(t, data, &d)
		return d.Index
	}
	if idx(ev[1].data) != 0 || idx(ev[4].data) != 1 {
		t.Errorf("block indices = %d,%d, want 0,1", idx(ev[1].data), idx(ev[4].data))
	}
}

func TestStream_MultipleToolCalls(t *testing.T) {
	in := `data: {"id":"x","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"t1","type":"function","function":{"name":"a","arguments":"{}"}}]}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"t2","type":"function","function":{"name":"b","arguments":"{}"}}]}}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

`
	ev := runStream(t, in)
	want := []string{
		"message_start",
		"content_block_start", "content_block_delta", "content_block_stop",
		"content_block_start", "content_block_delta", "content_block_stop",
		"message_delta", "message_stop",
	}
	if !eqTypes(types(ev), want) {
		t.Fatalf("event types = %v\nwant %v", types(ev), want)
	}
}

func TestStream_IgnoresNonDataLines(t *testing.T) {
	in := ": a comment line\nevent: ping\ndata: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"
	ev := runStream(t, in)
	if len(ev) == 0 || ev[0].typ != "message_start" {
		t.Fatalf("expected message_start first, got %v", types(ev))
	}
	if got := types(ev); got[len(got)-1] != "message_stop" {
		t.Errorf("last = %q, want message_stop", got[len(got)-1])
	}
}

func mustJSON(t *testing.T, data []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal %s: %v", data, err)
	}
}

func deltaText(t *testing.T, data []byte) string {
	t.Helper()
	var d struct {
		Delta struct {
			Text string `json:"text"`
		} `json:"delta"`
	}
	mustJSON(t, data, &d)
	return d.Delta.Text
}

func deltaPartialJSON(t *testing.T, data []byte) string {
	t.Helper()
	var d struct {
		Delta struct {
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
	}
	mustJSON(t, data, &d)
	return d.Delta.PartialJSON
}
