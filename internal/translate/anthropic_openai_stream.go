package translate

import (
	"bytes"
	"encoding/json"

	"github.com/andyjmorgan/sluice-gateway/protocols/anthropic/messages"
	"github.com/andyjmorgan/sluice-gateway/protocols/openai/chat"
)

// chatToMessagesStream is the stateful translator that turns an OpenAI Chat
// Completions SSE stream into an Anthropic Messages SSE stream. One instance
// handles one response; it is not safe for concurrent use.
//
// OpenAI streams a flat sequence of choice deltas (role, then content
// fragments, then tool-call fragments keyed by index, then a terminal
// finish_reason and optional usage). Anthropic needs an explicitly framed
// lifecycle: message_start, then opened/closed indexed content blocks
// (text or tool_use) carrying deltas, then message_delta + message_stop.
//
// This translator OWNS the Anthropic block indices (nextIndex), so the
// concatenated-tool-call / colliding-index corruption an untyped passthrough is
// prone to is structurally impossible on the emit side.
//
// Stream events are fixed-shape, so marshalling them cannot fail in practice;
// the (impossible) error is accumulated on err and surfaced once per Translate/
// Close rather than checked at every emit site.
type chatToMessagesStream struct {
	started  bool   // message_start emitted
	id       string // message id (from the first chunk that carries one)
	model    string // model (from the first chunk that carries one)
	usageIn  int    // prompt tokens (from a usage-bearing chunk)
	usageOut int    // completion tokens (from a usage-bearing chunk)
	stopRaw  *string
	err      error // first marshal error, surfaced by Translate/Close

	// Open-block tracking. Exactly one block is open at a time.
	curKind   blockKind // none | text | tool
	curIndex  int       // Anthropic index of the open block
	nextIndex int       // next Anthropic block index to assign
	curToolOA int       // OpenAI tool-call index the open tool block maps to
}

type blockKind int

const (
	blockNone blockKind = iota
	blockText
	blockTool
)

// Translate consumes one upstream SSE data payload (an OpenAI
// ChatCompletionChunk JSON object) and returns the Anthropic SSE frames it
// produces (possibly empty). Non-JSON or empty payloads yield no output.
func (s *chatToMessagesStream) Translate(frame []byte) ([]byte, error) {
	frame = bytes.TrimSpace(frame)
	if len(frame) == 0 {
		return nil, nil
	}
	// An OpenAI error delivered mid-stream (a chunk carrying a top-level
	// "error") becomes an Anthropic SSE error event.
	if msg, ok := errorChunk(frame); ok {
		var out bytes.Buffer
		s.emit(&out, messages.ErrorEvent{Type: "error", Error: messages.StreamError{Type: "api_error", Message: msg}})
		if s.err != nil {
			return nil, s.err
		}
		return out.Bytes(), nil
	}
	var chunk chat.ChatCompletionChunk
	if err := json.Unmarshal(frame, &chunk); err != nil {
		// A chunk we cannot decode is skipped rather than aborting the stream;
		// the alternative is dropping the whole response on one bad frame.
		return nil, nil //nolint:nilerr // skip undecodable chunk, keep streaming
	}

	if chunk.ID != "" {
		s.id = chunk.ID
	}
	if chunk.Model != "" {
		s.model = chunk.Model
	}
	if chunk.Usage != nil {
		s.usageIn = chunk.Usage.PromptTokens
		s.usageOut = chunk.Usage.CompletionTokens
	}

	var out bytes.Buffer
	s.ensureStarted(&out)
	if len(chunk.Choices) > 0 {
		choice := chunk.Choices[0]
		s.handleContent(&out, choice.Delta.Content)
		s.handleToolCalls(&out, choice.Delta.ToolCalls)
		if choice.FinishReason != nil {
			stop, _ := mapFinishReason(choice.FinishReason)
			s.stopRaw = stop
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	return out.Bytes(), nil
}

// Close flushes the terminal Anthropic events: it closes any open content
// block, then emits message_delta (stop_reason + output usage) and
// message_stop. Safe to call once at end of stream.
func (s *chatToMessagesStream) Close() ([]byte, error) {
	var out bytes.Buffer
	s.ensureStarted(&out) // frame an empty message if the stream produced nothing
	s.closeCurrent(&out)
	s.emit(&out, messages.MessageDeltaEvent{
		Type:  "message_delta",
		Delta: messages.MessageDelta{StopReason: s.stopRaw},
		Usage: messages.Usage{InputTokens: s.usageIn, OutputTokens: s.usageOut},
	})
	s.emit(&out, messages.MessageStopEvent{Type: "message_stop"})
	if s.err != nil {
		return nil, s.err
	}
	return out.Bytes(), nil
}

// ensureStarted emits message_start exactly once.
func (s *chatToMessagesStream) ensureStarted(out *bytes.Buffer) {
	if s.started {
		return
	}
	s.started = true
	id := s.id
	if id == "" {
		id = "msg_translated"
	}
	s.emit(out, messages.MessageStartEvent{
		Type: "message_start",
		Message: messages.MessagesResponse{
			ID:      id,
			Type:    "message",
			Role:    "assistant",
			Model:   s.model,
			Content: []messages.ContentBlock{},
			Usage:   messages.Usage{InputTokens: s.usageIn},
		},
	})
}

// handleContent emits a text-block delta for an OpenAI content fragment,
// opening a text block first if one is not already open. raw is the delta
// content field — a JSON string fragment in the common case.
func (s *chatToMessagesStream) handleContent(out *bytes.Buffer, raw json.RawMessage) {
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil || text == "" {
		return // non-string or empty content delta: nothing to emit
	}
	if s.curKind != blockText {
		s.closeCurrent(out)
		s.openText(out)
	}
	s.emit(out, messages.ContentBlockDeltaEvent{
		Type:  "content_block_delta",
		Index: s.curIndex,
		Delta: messages.TextDelta{Type: "text_delta", Text: text},
	})
}

// handleToolCalls opens/extends tool_use blocks from OpenAI tool-call deltas. A
// delta carrying an ID (or name) starts a new tool block; subsequent deltas for
// the same call carry argument fragments mapped to input_json_delta.
func (s *chatToMessagesStream) handleToolCalls(out *bytes.Buffer, calls []chat.ToolCallDelta) {
	for _, tc := range calls {
		if tc.ID != "" || tc.Function.Name != "" {
			s.closeCurrent(out)
			s.openTool(out, tc.Index, tc.ID, tc.Function.Name)
		}
		if tc.Function.Arguments != "" && s.curKind == blockTool {
			s.emit(out, messages.ContentBlockDeltaEvent{
				Type:  "content_block_delta",
				Index: s.curIndex,
				Delta: messages.InputJSONDelta{Type: "input_json_delta", PartialJSON: tc.Function.Arguments},
			})
		}
	}
}

func (s *chatToMessagesStream) openText(out *bytes.Buffer) {
	s.curKind = blockText
	s.curIndex = s.nextIndex
	s.nextIndex++
	s.emit(out, messages.ContentBlockStartEvent{
		Type:         "content_block_start",
		Index:        s.curIndex,
		ContentBlock: &messages.TextBlock{Type: "text", Text: ""},
	})
}

func (s *chatToMessagesStream) openTool(out *bytes.Buffer, oaIndex int, id, name string) {
	s.curKind = blockTool
	s.curIndex = s.nextIndex
	s.curToolOA = oaIndex
	s.nextIndex++
	s.emit(out, messages.ContentBlockStartEvent{
		Type:         "content_block_start",
		Index:        s.curIndex,
		ContentBlock: &messages.ToolUseBlock{Type: "tool_use", ID: id, Name: name, Input: json.RawMessage("{}")},
	})
}

// closeCurrent emits content_block_stop for the open block, if any.
func (s *chatToMessagesStream) closeCurrent(out *bytes.Buffer) {
	if s.curKind == blockNone {
		return
	}
	s.emit(out, messages.ContentBlockStopEvent{Type: "content_block_stop", Index: s.curIndex})
	s.curKind = blockNone
}

// emit marshals an Anthropic stream event and writes it as an SSE frame
// (`event: <type>\ndata: <json>\n\n`). A marshal failure (not reachable for the
// fixed-shape event types) is recorded on s.err and surfaced once by
// Translate/Close.
func (s *chatToMessagesStream) emit(out *bytes.Buffer, ev messages.StreamEvent) {
	if s.err != nil {
		return
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		s.err = err
		return
	}
	out.WriteString("event: ")
	out.WriteString(ev.EventType())
	out.WriteString("\ndata: ")
	out.Write(payload)
	out.WriteString("\n\n")
}
