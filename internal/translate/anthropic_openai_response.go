package translate

import (
	"encoding/json"
	"fmt"

	"github.com/andyjmorgan/slipspace-gateway/protocols/anthropic/messages"
	"github.com/andyjmorgan/slipspace-gateway/protocols/openai/chat"
)

// translateChatResponseToMessages maps a non-streaming OpenAI Chat Completions
// response body back into an Anthropic Messages response body. It returns the
// translated bytes plus the list of target features that had no Anthropic
// equivalent.
//
// OpenAI can return N choices; Anthropic models a single assistant message, so
// only the first choice is mapped and any extras are reported as drops.
func translateChatResponseToMessages(body []byte) ([]byte, []Drop, error) {
	var src chat.ChatCompletionResponse
	if err := json.Unmarshal(body, &src); err != nil {
		return nil, nil, fmt.Errorf("translate chat->messages: decode response: %w", err)
	}

	var drops []Drop
	dst := messages.MessagesResponse{
		ID:    src.ID,
		Type:  "message",
		Role:  "assistant",
		Model: src.Model,
		Usage: translateUsage(src.Usage, src.ServiceTier),
	}

	if src.SystemFingerprint != "" {
		drops = append(drops, Drop{Field: "system_fingerprint", Reason: reasonNoTargetEquivalent})
	}

	if len(src.Choices) > 1 {
		drops = append(drops, Drop{Field: "choices[1:]", Reason: "Anthropic returns a single message; extra OpenAI choices dropped"})
	}

	if len(src.Choices) > 0 {
		choice := src.Choices[0]

		stop, sd := mapFinishReason(choice.FinishReason)
		dst.StopReason = stop
		drops = append(drops, sd...)

		if len(choice.LogProbs) > 0 {
			drops = append(drops, Drop{Field: "choices[0].logprobs", Reason: reasonNoTargetEquivalent})
		}

		blocks, bd, err := contentBlocksFromMessage(choice.Message)
		if err != nil {
			return nil, nil, err
		}
		dst.Content = blocks
		drops = append(drops, bd...)
	}

	out, err := json.Marshal(dst)
	if err != nil {
		return nil, nil, fmt.Errorf("translate chat->messages: encode response: %w", err)
	}
	return out, drops, nil
}

// mapFinishReason maps an OpenAI finish_reason to an Anthropic stop_reason.
// A nil finish_reason (in-progress) maps to a nil stop_reason. Reasons OpenAI
// has but Anthropic does not (content_filter, and any future value) map to the
// closest Anthropic reason and are reported as drops so the nuance is visible.
func mapFinishReason(fr *string) (*string, []Drop) {
	if fr == nil {
		return nil, nil
	}
	endTurn := "end_turn"
	switch *fr {
	case "stop":
		return &endTurn, nil
	case "length":
		s := "max_tokens"
		return &s, nil
	case "tool_calls", "function_call":
		s := "tool_use"
		return &s, nil
	case "content_filter":
		return &endTurn, []Drop{{Field: "choices[0].finish_reason=content_filter", Reason: "mapped to end_turn; Anthropic has no content_filter stop_reason"}}
	default:
		return &endTurn, []Drop{{Field: "choices[0].finish_reason=" + *fr, Reason: "unknown finish_reason mapped to end_turn"}}
	}
}

// contentBlocksFromMessage builds the Anthropic content-block array from an
// OpenAI assistant ResponseMessage: text (string or content parts), a refusal,
// then tool_use blocks for each tool call. Audio replies have no Anthropic
// representation and are dropped.
func contentBlocksFromMessage(msg chat.ResponseMessage) ([]messages.ContentBlock, []Drop, error) {
	var blocks []messages.ContentBlock
	var drops []Drop

	if len(msg.Content) > 0 && string(msg.Content) != "null" {
		var s string
		if err := json.Unmarshal(msg.Content, &s); err == nil {
			if s != "" {
				blocks = append(blocks, &messages.TextBlock{Type: "text", Text: s})
			}
		} else {
			parts, perr := chat.UnmarshalContentParts(msg.Content)
			if perr != nil {
				return nil, nil, fmt.Errorf("translate chat->messages: decode message content: %w", perr)
			}
			for i, p := range parts {
				switch part := p.(type) {
				case *chat.TextContentPart:
					blocks = append(blocks, &messages.TextBlock{Type: "text", Text: part.Text})
				case *chat.RefusalContentPart:
					blocks = append(blocks, &messages.TextBlock{Type: "text", Text: part.Refusal})
					drops = append(drops, Drop{Field: fmt.Sprintf("choices[0].message.content[%d].refusal", i), Reason: "mapped to text; Anthropic has no refusal block"})
				default:
					drops = append(drops, Drop{Field: fmt.Sprintf("choices[0].message.content[%d].%s", i, p.PartType()), Reason: reasonNoTargetEquivalent})
				}
			}
		}
	}

	if msg.Refusal != nil && *msg.Refusal != "" {
		blocks = append(blocks, &messages.TextBlock{Type: "text", Text: *msg.Refusal})
		drops = append(drops, Drop{Field: "choices[0].message.refusal", Reason: "mapped to text; Anthropic has no refusal block"})
	}

	if msg.Audio != nil {
		drops = append(drops, Drop{Field: "choices[0].message.audio", Reason: reasonNoTargetEquivalent})
	}

	for _, tc := range msg.ToolCalls {
		args := tc.Function.Arguments
		if args == "" {
			args = "{}"
		}
		blocks = append(blocks, &messages.ToolUseBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: json.RawMessage(args),
		})
	}

	return blocks, drops, nil
}

// translateUsage maps OpenAI token accounting to Anthropic's. A nil OpenAI
// usage block yields a zero Anthropic usage. OpenAI's top-level service_tier is
// folded into Anthropic's usage.service_tier.
func translateUsage(u *chat.Usage, serviceTier string) messages.Usage {
	out := messages.Usage{ServiceTier: serviceTier}
	if u == nil {
		return out
	}
	out.InputTokens = u.PromptTokens
	out.OutputTokens = u.CompletionTokens
	if u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens > 0 {
		cached := u.PromptTokensDetails.CachedTokens
		out.CacheReadInputTokens = &cached
	}
	return out
}
