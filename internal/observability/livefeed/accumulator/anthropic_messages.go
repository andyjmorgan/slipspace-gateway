package accumulator

import (
	"strings"

	"github.com/andyjmorgan/sluice-gateway/providers/anthropic/messages"
)

// accumulateAnthropicMessages walks an Anthropic /v1/messages SSE
// stream and reassembles the assistant's textual reply plus any
// tool-use invocations.
//
// The Anthropic stream uses named SSE events:
//
//   - message_start            (initial Message envelope)
//   - content_block_start      (block of kind text|tool_use|thinking)
//   - content_block_delta      (text_delta | input_json_delta | …)
//   - content_block_stop
//   - message_delta            (usage / stop_reason updates)
//   - message_stop
//   - ping                     (heartbeat — ignored)
//
// Text is collected from TextDelta payloads, indexed by content block
// so multi-block responses concatenate in block order. Tool calls are
// initialised from the `content_block_start` event (ToolUseBlock
// carries id + name) and the InputJSONDelta payloads on subsequent
// content_block_delta events feed the arguments string.
func accumulateAnthropicMessages(raw []byte) Result {
	// blockText[i] is the cumulative text for content block at index i.
	blockText := map[int]*strings.Builder{}
	// blockOrder preserves insertion order so the final text matches
	// the stream's block ordering.
	var blockOrder []int

	tools := map[int]*ToolCall{}
	var toolOrder []int

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
		case *messages.ContentBlockStartEvent:
			if e.ContentBlock == nil {
				continue
			}
			switch block := e.ContentBlock.(type) {
			case *messages.TextBlock:
				if _, ok := blockText[e.Index]; !ok {
					b := &strings.Builder{}
					if block.Text != "" {
						b.WriteString(block.Text)
					}
					blockText[e.Index] = b
					blockOrder = append(blockOrder, e.Index)
				}
			case *messages.ToolUseBlock:
				if _, ok := tools[e.Index]; !ok {
					tools[e.Index] = &ToolCall{ID: block.ID, Name: block.Name}
					toolOrder = append(toolOrder, e.Index)
				}
			}
		case *messages.ContentBlockDeltaEvent:
			switch d := e.Delta.(type) {
			case *messages.TextDelta:
				b, ok := blockText[e.Index]
				if !ok {
					b = &strings.Builder{}
					blockText[e.Index] = b
					blockOrder = append(blockOrder, e.Index)
				}
				b.WriteString(d.Text)
			case *messages.InputJSONDelta:
				if tc, ok := tools[e.Index]; ok {
					tc.Arguments += d.PartialJSON
				}
			}
		}
	}

	if len(blockOrder) > 0 {
		var b strings.Builder
		for _, idx := range blockOrder {
			if sb, ok := blockText[idx]; ok {
				b.WriteString(sb.String())
			}
		}
		res.Text = b.String()
	}
	if len(toolOrder) > 0 {
		out := make([]ToolCall, 0, len(toolOrder))
		for _, idx := range toolOrder {
			if tc, ok := tools[idx]; ok {
				out = append(out, *tc)
			}
		}
		res.ToolCalls = out
	}
	return res
}
