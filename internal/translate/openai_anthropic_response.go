package translate

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/andyjmorgan/sluice-gateway/protocols/anthropic/messages"
	"github.com/andyjmorgan/sluice-gateway/protocols/openai/chat"
)

// translateMessagesResponseToChat maps a non-streaming Anthropic Messages
// response body back into an OpenAI Chat Completions response body. It returns
// the translated bytes plus the list of target features that had no OpenAI
// equivalent.
//
// Anthropic models a single assistant message; OpenAI wraps it in a single
// Choice. Text blocks concatenate into the choice's content, tool_use blocks
// become tool_calls, and the stop_reason maps to a finish_reason.
func translateMessagesResponseToChat(body []byte) ([]byte, []Drop, error) {
	var src messages.MessagesResponse
	if err := json.Unmarshal(body, &src); err != nil {
		return nil, nil, fmt.Errorf("translate messages->chat: decode response: %w", err)
	}

	var drops []Drop

	text, toolCalls, cd := messageContentFromBlocks(src.Content)
	drops = append(drops, cd...)

	msg := chat.ResponseMessage{Role: "assistant", ToolCalls: toolCalls}
	if text != "" {
		raw, _ := json.Marshal(text) // marshaling a Go string cannot fail
		msg.Content = raw
	}

	finish, fd := mapStopReasonToFinish(src.StopReason)
	drops = append(drops, fd...)

	dst := chat.ChatCompletionResponse{
		ID:          src.ID,
		Object:      "chat.completion",
		Model:       src.Model,
		ServiceTier: src.Usage.ServiceTier,
		Choices: []chat.Choice{{
			Index:        0,
			Message:      msg,
			FinishReason: finish,
		}},
		Usage: usageToChat(src.Usage),
	}

	if src.StopSequence != nil {
		drops = append(drops, Drop{Field: "stop_sequence", Reason: reasonNoTargetEquivalent})
	}
	if src.Container != nil {
		drops = append(drops, Drop{Field: "container", Reason: reasonNoTargetEquivalent})
	}
	if len(src.StopDetails) > 0 && string(src.StopDetails) != "null" {
		drops = append(drops, Drop{Field: "stop_details", Reason: reasonNoTargetEquivalent})
	}
	if src.ContextManagement != nil {
		drops = append(drops, Drop{Field: "context_management", Reason: reasonNoTargetEquivalent})
	}

	out, err := json.Marshal(dst)
	if err != nil {
		return nil, nil, fmt.Errorf("translate messages->chat: encode response: %w", err)
	}
	return out, drops, nil
}

// messageContentFromBlocks projects an Anthropic content-block array into an
// OpenAI assistant message: text blocks concatenate into the returned string,
// tool_use blocks become tool_calls. thinking / redacted_thinking blocks have
// no OpenAI Chat representation and are dropped; any other block kind (an image
// or stray tool_result in a response) is likewise dropped.
func messageContentFromBlocks(blocks []messages.ContentBlock) (string, []chat.ToolCall, []Drop) {
	var sb strings.Builder
	var toolCalls []chat.ToolCall
	var drops []Drop

	for i, b := range blocks {
		switch blk := b.(type) {
		case *messages.TextBlock:
			sb.WriteString(blk.Text)
		case *messages.ToolUseBlock:
			args := "{}"
			if len(blk.Input) > 0 {
				args = string(blk.Input)
			}
			toolCalls = append(toolCalls, chat.ToolCall{
				ID:       blk.ID,
				Type:     "function",
				Function: chat.ToolCallFunction{Name: blk.Name, Arguments: args},
			})
		case *messages.ThinkingBlock:
			drops = append(drops, Drop{Field: fmt.Sprintf("content[%d].thinking", i), Reason: reasonNoTargetEquivalent})
		case *messages.RedactedThinkingBlock:
			drops = append(drops, Drop{Field: fmt.Sprintf("content[%d].redacted_thinking", i), Reason: reasonNoTargetEquivalent})
		default:
			drops = append(drops, Drop{Field: fmt.Sprintf("content[%d].%s", i, b.BlockType()), Reason: reasonNoTargetEquivalent})
		}
	}

	return sb.String(), toolCalls, drops
}

// mapStopReasonToFinish maps an Anthropic stop_reason to an OpenAI
// finish_reason. A nil stop_reason (in-progress) maps to a nil finish_reason.
// stop_sequence folds into "stop" (the matched sequence string is reported as a
// separate dropped field); an unknown reason maps to "stop" and is reported.
func mapStopReasonToFinish(sr *string) (*string, []Drop) {
	if sr == nil {
		return nil, nil
	}
	stop := "stop"
	switch *sr {
	case "end_turn", "stop_sequence":
		return &stop, nil
	case "max_tokens":
		s := "length"
		return &s, nil
	case "tool_use":
		s := "tool_calls"
		return &s, nil
	default:
		return &stop, []Drop{{Field: "stop_reason=" + *sr, Reason: "unknown stop_reason mapped to stop"}}
	}
}

// usageToChat maps Anthropic token accounting to OpenAI's. Anthropic's
// cache_read_input_tokens folds into OpenAI prompt_tokens_details.cached_tokens;
// total_tokens is synthesised as input + output. Always returns a non-nil Usage
// because OpenAI responses always carry a usage block.
func usageToChat(u messages.Usage) *chat.Usage {
	out := &chat.Usage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.InputTokens + u.OutputTokens,
	}
	if u.CacheReadInputTokens != nil {
		out.PromptTokensDetails = &chat.PromptTokensDetails{CachedTokens: *u.CacheReadInputTokens}
	}
	return out
}
