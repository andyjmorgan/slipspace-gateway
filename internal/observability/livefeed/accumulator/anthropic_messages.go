package accumulator

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/andyjmorgan/sluice-gateway/providers/anthropic/messages"
)

// accumulateAnthropicMessages walks an Anthropic /v1/messages SSE
// stream and reassembles it into the JSON-encoded MessagesResponse
// the non-streaming endpoint would have returned.
//
// The Anthropic stream uses named SSE events:
//
//   - message_start            (initial Message envelope — id, role,
//     model, empty Content, partial Usage)
//   - content_block_start      (block of kind text|tool_use|thinking)
//   - content_block_delta      (text_delta | input_json_delta |
//     thinking_delta | signature_delta)
//   - content_block_stop
//   - message_delta            (terminal stop_reason / stop_sequence /
//     final Usage)
//   - message_stop
//   - ping                     (heartbeat — ignored)
//
// Content blocks are kept in index order. TextBlock.Text accumulates
// across TextDelta events; ToolUseBlock.Input accumulates across
// InputJSONDelta events as a JSON string that is parsed once at
// assembly time. ThinkingBlock.Thinking accumulates across
// ThinkingDelta events, with the terminal SignatureDelta setting its
// Signature, so the reconstructed block matches the non-streaming
// shape. message_delta overlays the terminal stop_reason,
// stop_sequence, and merges Usage so the assembled response carries
// the final accounting.
func accumulateAnthropicMessages(raw []byte) Result {
	state := newAnthropicState()
	res := Result{}

	for _, ev := range parseSSE(raw) {
		if ev.Data == "" {
			continue
		}
		decoded, err := messages.UnmarshalStreamEvent([]byte(ev.Data))
		if err != nil {
			res.Partial = true
			continue
		}
		switch e := decoded.(type) {
		case *messages.MessageStartEvent:
			state.absorbStart(&e.Message)
		case *messages.ContentBlockStartEvent:
			state.absorbBlockStart(e.Index, e.ContentBlock)
		case *messages.ContentBlockDeltaEvent:
			state.absorbBlockDelta(e.Index, e.Delta)
		case *messages.MessageDeltaEvent:
			state.absorbMessageDelta(e)
		}
	}

	out, err := json.Marshal(state.assemble())
	if err != nil {
		res.Partial = true
		return res
	}
	res.Assembled = out
	return res
}

type anthropicState struct {
	base       messages.MessagesResponse
	gotStart   bool
	blocks     map[int]*anthropicBlockState
	blockOrder []int
}

// anthropicBlockState carries the in-flight reconstruction of one
// content block. Only the buffers matching kind are populated per
// block — the kind is fixed at block_start time.
type anthropicBlockState struct {
	kind     string
	textBuf  strings.Builder
	toolID   string
	toolName string
	toolArgs strings.Builder
	// thinkingBuf accumulates thinking_delta fragments; signature is set
	// once by the terminal signature_delta. Both only populate when
	// kind == "thinking".
	thinkingBuf strings.Builder
	signature   string
	// raw holds the original block as emitted by content_block_start;
	// kept so unknown block kinds (and any DynamicProperties that
	// rode in on the start event) survive assembly.
	raw messages.ContentBlock
}

func newAnthropicState() *anthropicState {
	return &anthropicState{blocks: map[int]*anthropicBlockState{}}
}

func (s *anthropicState) absorbStart(msg *messages.MessagesResponse) {
	if msg == nil {
		return
	}
	s.base = *msg
	s.gotStart = true
}

func (s *anthropicState) absorbBlockStart(index int, block messages.ContentBlock) {
	st, ok := s.blocks[index]
	if !ok {
		st = &anthropicBlockState{}
		s.blocks[index] = st
		s.blockOrder = append(s.blockOrder, index)
	}
	st.raw = block
	switch b := block.(type) {
	case *messages.TextBlock:
		st.kind = "text"
		if b != nil && b.Text != "" {
			st.textBuf.WriteString(b.Text)
		}
	case *messages.ToolUseBlock:
		st.kind = "tool_use"
		if b != nil {
			st.toolID = b.ID
			st.toolName = b.Name
		}
	case *messages.ThinkingBlock:
		st.kind = "thinking"
		if b != nil {
			if b.Thinking != "" {
				st.thinkingBuf.WriteString(b.Thinking)
			}
			st.signature = b.Signature
		}
	default:
		// RedactedThinkingBlock / unknown — keep the raw block as-is;
		// it carries no deltas to accumulate.
	}
}

func (s *anthropicState) absorbBlockDelta(index int, delta messages.ContentBlockDelta) {
	st, ok := s.blocks[index]
	if !ok {
		// Defensive: some streams emit a delta before the block_start
		// was observed (truncated capture, in particular). Initialise
		// as text so the bytes aren't dropped.
		st = &anthropicBlockState{kind: "text"}
		s.blocks[index] = st
		s.blockOrder = append(s.blockOrder, index)
	}
	switch d := delta.(type) {
	case *messages.TextDelta:
		st.textBuf.WriteString(d.Text)
	case *messages.InputJSONDelta:
		st.toolArgs.WriteString(d.PartialJSON)
	case *messages.ThinkingDelta:
		st.thinkingBuf.WriteString(d.Thinking)
	case *messages.SignatureDelta:
		st.signature = d.Signature
	}
}

func (s *anthropicState) absorbMessageDelta(e *messages.MessageDeltaEvent) {
	if e == nil {
		return
	}
	if e.Delta.StopReason != nil {
		v := *e.Delta.StopReason
		s.base.StopReason = &v
	}
	if e.Delta.StopSequence != nil {
		v := *e.Delta.StopSequence
		s.base.StopSequence = &v
	}
	// MessageDeltaEvent.Usage carries deltas for the final accounting;
	// Anthropic's convention is that the final OutputTokens overrides
	// the placeholder set on message_start.
	if e.Usage.OutputTokens != 0 {
		s.base.Usage.OutputTokens = e.Usage.OutputTokens
	}
	if e.Usage.InputTokens != 0 {
		s.base.Usage.InputTokens = e.Usage.InputTokens
	}
	if e.Usage.CacheCreationInputTokens != nil {
		v := *e.Usage.CacheCreationInputTokens
		s.base.Usage.CacheCreationInputTokens = &v
	}
	if e.Usage.CacheReadInputTokens != nil {
		v := *e.Usage.CacheReadInputTokens
		s.base.Usage.CacheReadInputTokens = &v
	}
}

func (s *anthropicState) assemble() messages.MessagesResponse {
	resp := s.base
	if !s.gotStart {
		// Stream truncated before message_start — emit minimal shell so
		// downstream can still render whatever blocks were parsed.
		resp.Type = "message"
		resp.Role = "assistant"
	}

	sort.Ints(s.blockOrder)
	content := make([]messages.ContentBlock, 0, len(s.blockOrder))
	for _, idx := range s.blockOrder {
		st := s.blocks[idx]
		switch st.kind {
		case "text":
			block := &messages.TextBlock{Type: "text", Text: st.textBuf.String()}
			if existing, ok := st.raw.(*messages.TextBlock); ok && existing != nil {
				block.DynamicProperties = existing.DynamicProperties
			}
			content = append(content, block)
		case "tool_use":
			input := json.RawMessage("{}")
			if s := st.toolArgs.String(); s != "" {
				input = json.RawMessage(s)
			}
			block := &messages.ToolUseBlock{
				Type:  "tool_use",
				ID:    st.toolID,
				Name:  st.toolName,
				Input: input,
			}
			if existing, ok := st.raw.(*messages.ToolUseBlock); ok && existing != nil {
				block.DynamicProperties = existing.DynamicProperties
			}
			content = append(content, block)
		case "thinking":
			block := &messages.ThinkingBlock{
				Type:      "thinking",
				Thinking:  st.thinkingBuf.String(),
				Signature: st.signature,
			}
			if existing, ok := st.raw.(*messages.ThinkingBlock); ok && existing != nil {
				block.DynamicProperties = existing.DynamicProperties
			}
			content = append(content, block)
		default:
			if st.raw != nil {
				content = append(content, st.raw)
			}
		}
	}
	resp.Content = content
	return resp
}
