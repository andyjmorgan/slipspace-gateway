package translate

import (
	"bytes"
	"encoding/json"

	"github.com/andyjmorgan/slipspace-gateway/protocols/anthropic/messages"
	"github.com/andyjmorgan/slipspace-gateway/protocols/openai/chat"
)

// messagesToChatStream is the stateful translator that turns an Anthropic
// Messages SSE stream into an OpenAI Chat Completions SSE stream. One instance
// handles one response; it is not safe for concurrent use.
//
// Anthropic frames an explicit lifecycle: message_start, then opened/closed
// indexed content blocks (text or tool_use) carrying deltas, then
// message_delta + message_stop. OpenAI streams a flat sequence of choice deltas
// (a leading role delta, then content fragments, then tool-call fragments keyed
// by a tool index, then a terminal finish_reason and optional usage), closed by
// a `[DONE]` sentinel.
//
// Anthropic block indices increment across BOTH text and tool blocks; OpenAI
// tool_calls carry a separate index over tool calls only. This translator owns
// that remapping (nextToolIndex), so a tool call always lands at the right
// OpenAI tool index regardless of interleaved text blocks.
//
// Every OpenAI frame this translator emits is a fixed-shape chunk (or an error
// envelope) whose only dynamic content is text/arguments already validated as a
// JSON string — so marshalling cannot fail and the emit path carries no error
// return.
type messagesToChatStream struct {
	id           string  // completion id (from message_start)
	model        string  // model (from message_start)
	roleEmitted  bool    // leading role delta emitted
	inputTokens  int     // prompt tokens (from message_start / message_delta usage)
	outputTokens int     // completion tokens (from message_delta usage)
	stopReason   *string // anthropic stop_reason (from message_delta)

	// Open-block tracking. Anthropic streams one block at a time
	// (start..deltas..stop), so a single current-block record suffices.
	curKind       blockKind // none | text | tool
	curToolIndex  int       // OpenAI tool index of the open tool block
	nextToolIndex int       // next OpenAI tool index to assign
}

// Translate consumes one upstream Anthropic SSE data payload (a single stream
// event JSON object) and returns the OpenAI SSE frames it produces (possibly
// empty). Non-JSON or empty payloads yield no output.
func (s *messagesToChatStream) Translate(frame []byte) ([]byte, error) {
	frame = bytes.TrimSpace(frame)
	if len(frame) == 0 {
		return nil, nil
	}
	ev, err := messages.UnmarshalStreamEvent(frame)
	if err != nil {
		// An event we cannot decode is skipped rather than aborting the stream.
		return nil, nil //nolint:nilerr // skip undecodable event, keep streaming
	}

	var out bytes.Buffer
	switch e := ev.(type) {
	case *messages.MessageStartEvent:
		if e.Message.ID != "" {
			s.id = e.Message.ID
		}
		if e.Message.Model != "" {
			s.model = e.Message.Model
		}
		if e.Message.Usage.InputTokens > 0 {
			s.inputTokens = e.Message.Usage.InputTokens
		}
		s.ensureRole(&out)
	case *messages.ContentBlockStartEvent:
		s.handleBlockStart(&out, e)
	case *messages.ContentBlockDeltaEvent:
		s.handleBlockDelta(&out, e)
	case *messages.ContentBlockStopEvent:
		s.curKind = blockNone
	case *messages.MessageDeltaEvent:
		if e.Delta.StopReason != nil {
			s.stopReason = e.Delta.StopReason
		}
		if e.Usage.OutputTokens > 0 {
			s.outputTokens = e.Usage.OutputTokens
		}
		if e.Usage.InputTokens > 0 {
			s.inputTokens = e.Usage.InputTokens
		}
	case *messages.ErrorEvent:
		s.emitError(&out, e.Error)
	default:
		// message_stop is handled in Close; ping / unknown events are ignored.
	}

	return out.Bytes(), nil
}

// Close flushes the terminal OpenAI frames: the finish-reason chunk, a
// usage-only chunk (empty choices), and the `[DONE]` sentinel. Safe to call
// once at end of stream.
func (s *messagesToChatStream) Close() ([]byte, error) {
	var out bytes.Buffer
	s.ensureRole(&out) // frame an empty stream with a leading role delta

	finish := s.finishReason()
	s.emitChunk(&out, s.chunk([]chat.ChunkChoice{{
		Index:        0,
		Delta:        chat.DeltaMessage{},
		FinishReason: &finish,
	}}, nil))

	usage := &chat.Usage{
		PromptTokens:     s.inputTokens,
		CompletionTokens: s.outputTokens,
		TotalTokens:      s.inputTokens + s.outputTokens,
	}
	s.emitChunk(&out, s.chunk([]chat.ChunkChoice{}, usage))

	out.WriteString("data: [DONE]\n\n")
	return out.Bytes(), nil
}

// handleBlockStart opens a content block. A text block just sets the current
// kind (OpenAI needs no block-open marker); a tool_use block assigns the next
// OpenAI tool index and emits the tool-call start delta. thinking / redacted /
// unknown blocks set the current kind to none so their deltas are dropped.
func (s *messagesToChatStream) handleBlockStart(out *bytes.Buffer, e *messages.ContentBlockStartEvent) {
	s.ensureRole(out)
	switch blk := e.ContentBlock.(type) {
	case *messages.TextBlock:
		s.curKind = blockText
		if blk.Text != "" {
			s.emitContent(out, blk.Text)
		}
	case *messages.ToolUseBlock:
		s.curKind = blockTool
		s.curToolIndex = s.nextToolIndex
		s.nextToolIndex++
		s.emitToolStart(out, s.curToolIndex, blk.ID, blk.Name)
	default:
		s.curKind = blockNone
	}
}

// handleBlockDelta routes an Anthropic delta to its OpenAI equivalent: a
// text_delta on a text block becomes a content delta; an input_json_delta on a
// tool block becomes a tool-call argument delta. thinking / signature / unknown
// deltas have no OpenAI Chat representation and are dropped.
func (s *messagesToChatStream) handleBlockDelta(out *bytes.Buffer, e *messages.ContentBlockDeltaEvent) {
	switch d := e.Delta.(type) {
	case *messages.TextDelta:
		if s.curKind == blockText && d.Text != "" {
			s.emitContent(out, d.Text)
		}
	case *messages.InputJSONDelta:
		if s.curKind == blockTool {
			s.emitToolArgs(out, s.curToolIndex, d.PartialJSON)
		}
	default:
		// thinking_delta / signature_delta / unknown: dropped.
	}
}

// finishReason maps the captured Anthropic stop_reason to an OpenAI
// finish_reason, defaulting to "stop" for a terminal stream that never carried
// one.
func (s *messagesToChatStream) finishReason() string {
	fr, _ := mapStopReasonToFinish(s.stopReason)
	if fr == nil {
		return "stop"
	}
	return *fr
}

// ensureRole emits the leading `delta:{role:"assistant"}` chunk exactly once.
func (s *messagesToChatStream) ensureRole(out *bytes.Buffer) {
	if s.roleEmitted {
		return
	}
	s.roleEmitted = true
	s.emitChunk(out, s.chunk([]chat.ChunkChoice{{
		Index: 0,
		Delta: chat.DeltaMessage{Role: "assistant"},
	}}, nil))
}

// emitContent emits a content-fragment chunk for a text delta.
func (s *messagesToChatStream) emitContent(out *bytes.Buffer, text string) {
	raw, _ := json.Marshal(text) // marshaling a Go string cannot fail
	s.emitChunk(out, s.chunk([]chat.ChunkChoice{{
		Index: 0,
		Delta: chat.DeltaMessage{Content: raw},
	}}, nil))
}

// emitToolStart emits the opening tool-call delta (id + name + empty argument
// marker) at the given OpenAI tool index.
func (s *messagesToChatStream) emitToolStart(out *bytes.Buffer, idx int, id, name string) {
	s.emitChunk(out, s.chunk([]chat.ChunkChoice{{
		Index: 0,
		Delta: chat.DeltaMessage{ToolCalls: []chat.ToolCallDelta{{
			Index:    idx,
			ID:       id,
			Type:     "function",
			Function: &chat.ToolCallFunctionDelta{Name: name, Arguments: ""},
		}}},
	}}, nil))
}

// emitToolArgs emits an argument-fragment delta for the tool call at idx.
func (s *messagesToChatStream) emitToolArgs(out *bytes.Buffer, idx int, args string) {
	s.emitChunk(out, s.chunk([]chat.ChunkChoice{{
		Index: 0,
		Delta: chat.DeltaMessage{ToolCalls: []chat.ToolCallDelta{{
			Index:    idx,
			Function: &chat.ToolCallFunctionDelta{Arguments: args},
		}}},
	}}, nil))
}

// emitError emits an OpenAI mid-stream error frame (`data: {"error":{...}}`)
// from an Anthropic stream error event.
func (s *messagesToChatStream) emitError(out *bytes.Buffer, e messages.StreamError) {
	env := openAIErrorEnvelope{Error: openAIErrorBody{Message: e.Message, Type: e.Type}}
	payload, _ := json.Marshal(env) // fixed-shape struct cannot fail to marshal
	out.WriteString("data: ")
	out.Write(payload)
	out.WriteString("\n\n")
}

// chunk builds a ChatCompletionChunk carrying the supplied choices and optional
// usage, stamped with the stream's id (synthesised if the upstream gave none)
// and model.
func (s *messagesToChatStream) chunk(choices []chat.ChunkChoice, usage *chat.Usage) chat.ChatCompletionChunk {
	id := s.id
	if id == "" {
		id = "chatcmpl_translated"
	}
	return chat.ChatCompletionChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Model:   s.model,
		Choices: choices,
		Usage:   usage,
	}
}

// emitChunk marshals an OpenAI chunk and writes it as an SSE frame
// (`data: <json>\n\n`). The chunk types are fixed-shape and their only dynamic
// content is an already-validated JSON string, so marshalling cannot fail.
func (s *messagesToChatStream) emitChunk(out *bytes.Buffer, c chat.ChatCompletionChunk) {
	payload, _ := json.Marshal(c) // fixed-shape chunk cannot fail to marshal
	out.WriteString("data: ")
	out.Write(payload)
	out.WriteString("\n\n")
}
