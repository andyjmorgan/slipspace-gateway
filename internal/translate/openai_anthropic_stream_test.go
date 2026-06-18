package translate

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/protocols/openai/chat"
)

// runMessagesStream feeds a sequence of Anthropic SSE event payloads through a
// fresh messagesToChatStream, then Close, and returns the resulting OpenAI SSE
// data payloads (one per `data:` frame, in order).
func runMessagesStream(t *testing.T, events []string) []string {
	t.Helper()
	st := &messagesToChatStream{}
	var buf strings.Builder
	for _, ev := range events {
		out, err := st.Translate([]byte(ev))
		if err != nil {
			t.Fatalf("Translate(%s): %v", ev, err)
		}
		buf.Write(out)
	}
	out, err := st.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	buf.Write(out)
	return dataFrames(buf.String())
}

// dataFrames extracts the payload after each `data: ` line in an SSE blob.
func dataFrames(s string) []string {
	var frames []string
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "data: ") {
			frames = append(frames, strings.TrimPrefix(line, "data: "))
		}
	}
	return frames
}

func parseChunk(t *testing.T, frame string) chat.ChatCompletionChunk {
	t.Helper()
	var c chat.ChatCompletionChunk
	if err := json.Unmarshal([]byte(frame), &c); err != nil {
		t.Fatalf("parse chunk %q: %v", frame, err)
	}
	return c
}

func TestMessagesStream_TextLifecycle(t *testing.T) {
	frames := runMessagesStream(t, []string{
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-x","content":[],"usage":{"input_tokens":5,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello "}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"world"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
		`{"type":"message_stop"}`,
	})

	if len(frames) < 5 {
		t.Fatalf("frames = %d (%v), want >=5", len(frames), frames)
	}
	if frames[len(frames)-1] != "[DONE]" {
		t.Errorf("last frame = %q, want [DONE]", frames[len(frames)-1])
	}

	// First chunk: leading role delta, stamped with id/model.
	first := parseChunk(t, frames[0])
	if first.ID != "msg_1" || first.Model != "claude-x" {
		t.Errorf("first chunk id/model = %q/%q, want msg_1/claude-x", first.ID, first.Model)
	}
	if first.Object != "chat.completion.chunk" {
		t.Errorf("object = %q, want chat.completion.chunk", first.Object)
	}
	if len(first.Choices) != 1 || first.Choices[0].Delta.Role != "assistant" {
		t.Errorf("first chunk = %+v, want role=assistant delta", first.Choices)
	}

	// Reassemble streamed content + find the finish + usage chunks.
	var text string
	var finish string
	var sawUsage bool
	for _, f := range frames {
		if f == "[DONE]" {
			continue
		}
		c := parseChunk(t, f)
		if len(c.Choices) == 1 {
			if dc := c.Choices[0].Delta.Content; len(dc) > 0 {
				var s string
				_ = json.Unmarshal(dc, &s)
				text += s
			}
			if c.Choices[0].FinishReason != nil {
				finish = *c.Choices[0].FinishReason
			}
		}
		if c.Usage != nil {
			sawUsage = true
			if c.Usage.PromptTokens != 5 || c.Usage.CompletionTokens != 2 || c.Usage.TotalTokens != 7 {
				t.Errorf("usage = %+v, want 5/2/7", c.Usage)
			}
			if len(c.Choices) != 0 {
				t.Errorf("usage chunk choices = %d, want 0", len(c.Choices))
			}
		}
	}
	if text != "hello world" {
		t.Errorf("streamed text = %q, want 'hello world'", text)
	}
	if finish != "stop" {
		t.Errorf("finish_reason = %q, want stop", finish)
	}
	if !sawUsage {
		t.Error("no usage chunk emitted")
	}
}

func TestMessagesStream_ToolCallLifecycle(t *testing.T) {
	frames := runMessagesStream(t, []string{
		`{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"x","content":[],"usage":{"input_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tu_1","name":"get_weather","input":{}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":":\"SF\"}"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":4}}`,
		`{"type":"message_stop"}`,
	})

	var startName, args, finish string
	var toolIndex = -1
	for _, f := range frames {
		if f == "[DONE]" {
			continue
		}
		c := parseChunk(t, f)
		if len(c.Choices) == 0 {
			continue
		}
		for _, tc := range c.Choices[0].Delta.ToolCalls {
			if tc.Function != nil && tc.Function.Name != "" {
				startName = tc.Function.Name
				toolIndex = tc.Index
			}
			if tc.Function != nil && tc.Function.Arguments != "" {
				args += tc.Function.Arguments
			}
		}
		if c.Choices[0].FinishReason != nil {
			finish = *c.Choices[0].FinishReason
		}
	}
	if startName != "get_weather" || toolIndex != 0 {
		t.Errorf("tool start name/index = %q/%d, want get_weather/0", startName, toolIndex)
	}
	if args != `{"city":"SF"}` {
		t.Errorf("assembled args = %q, want {\"city\":\"SF\"}", args)
	}
	if finish != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", finish)
	}
}

func TestMessagesStream_ToolIndexRemap(t *testing.T) {
	// A text block (Anthropic index 0) precedes two tool blocks (Anthropic
	// indices 1, 2). OpenAI tool indices must be 0, 1 — counted over tools only.
	frames := runMessagesStream(t, []string{
		`{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"x","content":[]}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"a","name":"f1","input":{}}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"b","name":"f2","input":{}}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":1}}`,
		`{"type":"message_stop"}`,
	})

	var indices []int
	for _, f := range frames {
		if f == "[DONE]" {
			continue
		}
		c := parseChunk(t, f)
		if len(c.Choices) == 0 {
			continue
		}
		for _, tc := range c.Choices[0].Delta.ToolCalls {
			if tc.Function != nil && tc.Function.Name != "" {
				indices = append(indices, tc.Index)
			}
		}
	}
	if len(indices) != 2 || indices[0] != 0 || indices[1] != 1 {
		t.Errorf("tool indices = %v, want [0 1]", indices)
	}
}

func TestMessagesStream_ThinkingDeltasDropped(t *testing.T) {
	frames := runMessagesStream(t, []string{
		`{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"x","content":[]}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"secret"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
		`{"type":"message_stop"}`,
	})
	for _, f := range frames {
		if f == "[DONE]" {
			continue
		}
		c := parseChunk(t, f)
		if len(c.Choices) == 1 && len(c.Choices[0].Delta.Content) > 0 {
			var s string
			_ = json.Unmarshal(c.Choices[0].Delta.Content, &s)
			if strings.Contains(s, "secret") {
				t.Errorf("thinking text leaked into content: %q", s)
			}
		}
	}
}

func TestMessagesStream_InitialBlockTextAndDeltaInput(t *testing.T) {
	// A content_block_start carrying initial text emits it immediately, and a
	// message_delta carrying input_tokens updates the prompt count.
	frames := runMessagesStream(t, []string{
		`{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"x","content":[]}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"seed"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":9,"output_tokens":1}}`,
		`{"type":"message_stop"}`,
	})
	var text string
	var prompt int
	for _, f := range frames {
		if f == "[DONE]" {
			continue
		}
		c := parseChunk(t, f)
		if len(c.Choices) == 1 && len(c.Choices[0].Delta.Content) > 0 {
			var s string
			_ = json.Unmarshal(c.Choices[0].Delta.Content, &s)
			text += s
		}
		if c.Usage != nil {
			prompt = c.Usage.PromptTokens
		}
	}
	if text != "seed" {
		t.Errorf("text = %q, want 'seed' (initial block text)", text)
	}
	if prompt != 9 {
		t.Errorf("prompt tokens = %d, want 9 (from message_delta input_tokens)", prompt)
	}
}

func TestMessagesStream_ErrorEvent(t *testing.T) {
	st := &messagesToChatStream{}
	out, err := st.Translate([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"too busy"}}`))
	if err != nil {
		t.Fatalf("Translate error event: %v", err)
	}
	frames := dataFrames(string(out))
	if len(frames) != 1 {
		t.Fatalf("frames = %v, want 1 error frame", frames)
	}
	var env openAIErrorEnvelope
	if err := json.Unmarshal([]byte(frames[0]), &env); err != nil {
		t.Fatalf("error frame not an OpenAI envelope: %v\n%s", err, frames[0])
	}
	if env.Error.Message != "too busy" || env.Error.Type != "overloaded_error" {
		t.Errorf("error = %+v, want 'too busy'/overloaded_error", env.Error)
	}
}

func TestMessagesStream_SkipsAndEmpties(t *testing.T) {
	st := &messagesToChatStream{}
	// Empty frame: no output.
	if out, err := st.Translate([]byte("   ")); err != nil || len(out) != 0 {
		t.Errorf("empty frame: out=%q err=%v, want empty/nil", out, err)
	}
	// Undecodable frame: skipped, no error.
	if out, err := st.Translate([]byte(`{not json`)); err != nil || len(out) != 0 {
		t.Errorf("bad frame: out=%q err=%v, want empty/nil", out, err)
	}
	// Ping event: ignored.
	if out, err := st.Translate([]byte(`{"type":"ping"}`)); err != nil || len(out) != 0 {
		t.Errorf("ping: out=%q err=%v, want empty/nil", out, err)
	}
}

func TestMessagesStream_EmptyStreamClose(t *testing.T) {
	// Close with no events still frames a valid OpenAI stream.
	frames := runMessagesStream(t, nil)
	if len(frames) < 3 {
		t.Fatalf("frames = %v, want role + finish + usage + [DONE]", frames)
	}
	if frames[len(frames)-1] != "[DONE]" {
		t.Errorf("last frame = %q, want [DONE]", frames[len(frames)-1])
	}
	first := parseChunk(t, frames[0])
	if first.ID != "chatcmpl_translated" {
		t.Errorf("synthesised id = %q, want chatcmpl_translated", first.ID)
	}
	if first.Choices[0].Delta.Role != "assistant" {
		t.Error("first frame missing role delta")
	}
	// Default finish reason is stop when no message_delta arrived.
	var finish string
	for _, f := range frames {
		if f == "[DONE]" {
			continue
		}
		c := parseChunk(t, f)
		if len(c.Choices) == 1 && c.Choices[0].FinishReason != nil {
			finish = *c.Choices[0].FinishReason
		}
	}
	if finish != "stop" {
		t.Errorf("finish_reason = %q, want stop", finish)
	}
}

func TestMessagesStream_ThroughStreamingReader(t *testing.T) {
	// Integration: an Anthropic SSE body (no [DONE] sentinel — it ends on EOF)
	// driven through the pull-based reader yields a terminated OpenAI stream.
	upstream := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"x","content":[],"usage":{"input_tokens":3}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"yo"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	r := NewStreamingReader(io.NopCloser(strings.NewReader(upstream)), &messagesToChatStream{})
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if cerr := r.Close(); cerr != nil {
		t.Fatalf("Close: %v", cerr)
	}
	out := string(got)
	if !strings.Contains(out, "data: [DONE]") {
		t.Errorf("translated stream missing [DONE]:\n%s", out)
	}
	var text string
	for _, f := range dataFrames(out) {
		if f == "[DONE]" {
			continue
		}
		c := parseChunk(t, f)
		if len(c.Choices) == 1 && len(c.Choices[0].Delta.Content) > 0 {
			var s string
			_ = json.Unmarshal(c.Choices[0].Delta.Content, &s)
			text += s
		}
	}
	if text != "yo" {
		t.Errorf("streamed text = %q, want 'yo'", text)
	}
}
