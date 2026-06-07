package translate

import (
	"encoding/json"
	"fmt"

	"github.com/andyjmorgan/sluice-gateway/protocols/anthropic/messages"
	"github.com/andyjmorgan/sluice-gateway/protocols/openai/chat"
)

// translateMessagesRequestToChat maps an Anthropic Messages request body into
// an OpenAI Chat Completions request body. It returns the translated bytes plus
// the list of source features that had no OpenAI equivalent and were dropped.
//
// Model is carried verbatim — model-name remapping is a separate changeModelName
// concern, not translation's. Unknown top-level Anthropic fields (the request's
// DynamicProperties) are intentionally not carried onto the OpenAI request:
// they are vendor-specific and meaningless to OpenAI, so they are neither
// forwarded nor counted as feature drops.
func translateMessagesRequestToChat(body []byte) ([]byte, []Drop, error) {
	var src messages.MessagesRequest
	if err := json.Unmarshal(body, &src); err != nil {
		return nil, nil, fmt.Errorf("translate messages->chat: decode request: %w", err)
	}

	var drops []Drop
	dst := chat.ChatCompletionRequest{
		Model:       src.Model,
		Temperature: src.Temperature,
		TopP:        src.TopP,
		Stream:      src.Stream,
		ServiceTier: src.ServiceTier,
	}

	// Anthropic requires max_tokens; OpenAI treats it as optional. Carry it
	// through as the (deprecated but universally accepted) max_tokens field.
	if src.MaxTokens > 0 {
		mt := src.MaxTokens
		dst.MaxTokens = &mt
	}

	if src.TopK != nil {
		drops = append(drops, Drop{Field: "top_k", Reason: reasonNoTargetEquivalent})
	}
	if src.Thinking != nil {
		drops = append(drops, Drop{Field: "thinking", Reason: reasonNoTargetEquivalent})
	}

	if len(src.StopSequences) > 0 {
		raw, err := json.Marshal(src.StopSequences)
		if err != nil {
			return nil, nil, fmt.Errorf("translate messages->chat: encode stop_sequences: %w", err)
		}
		dst.Stop = raw
	}

	// Anthropic's reasoning-effort hint (output_config.effort) maps cleanly to
	// OpenAI's reasoning_effort.
	if src.OutputConfig != nil && src.OutputConfig.Effort != "" {
		dst.ReasoningEffort = src.OutputConfig.Effort
	}

	if src.Metadata != nil {
		if src.Metadata.UserID != "" {
			dst.User = src.Metadata.UserID
		}
		if len(src.Metadata.Extra) > 0 {
			drops = append(drops, Drop{Field: "metadata", Reason: "only metadata.user_id maps to OpenAI user"})
		}
	}

	if len(src.Tools) > 0 {
		tools, td := translateTools(src.Tools)
		dst.Tools = tools
		drops = append(drops, td...)
	}

	if src.ToolChoice != nil {
		tc, parallel, err := translateToolChoice(*src.ToolChoice)
		if err != nil {
			return nil, nil, err
		}
		dst.ToolChoice = tc
		dst.ParallelToolCalls = parallel
	}

	msgs, md, err := translateMessages(src)
	if err != nil {
		return nil, nil, err
	}
	dst.Messages = msgs
	drops = append(drops, md...)

	out, err := json.Marshal(dst)
	if err != nil {
		return nil, nil, fmt.Errorf("translate messages->chat: encode request: %w", err)
	}
	return out, drops, nil
}

// translateTools maps Anthropic tool definitions to OpenAI function tools.
// CacheControl has no OpenAI equivalent and is dropped.
func translateTools(in []messages.Tool) ([]chat.Tool, []Drop) {
	out := make([]chat.Tool, 0, len(in))
	var drops []Drop
	for i, t := range in {
		fn := &chat.ToolFunction{
			Name:        t.Name,
			Description: t.Description,
		}
		if len(t.InputSchema) > 0 {
			fn.Parameters = t.InputSchema
		}
		out = append(out, chat.Tool{Type: "function", Function: fn})
		if t.CacheControl != nil {
			drops = append(drops, Drop{Field: fmt.Sprintf("tools[%d].cache_control", i), Reason: reasonNoTargetEquivalent})
		}
	}
	return out, drops
}

// translateToolChoice maps Anthropic's tool_choice policy to OpenAI's
// tool_choice plus the parallel_tool_calls flag. Anthropic encodes
// "no parallel tool use" inside tool_choice; OpenAI carries it as a separate
// top-level field, so it is returned alongside.
func translateToolChoice(tc messages.ToolChoice) (json.RawMessage, *bool, error) {
	var parallel *bool
	if tc.DisableParallelToolUse != nil {
		v := !*tc.DisableParallelToolUse
		parallel = &v
	}

	var raw json.RawMessage
	switch tc.Type {
	case "auto":
		raw = json.RawMessage(`"auto"`)
	case "any":
		raw = json.RawMessage(`"required"`)
	case "none":
		raw = json.RawMessage(`"none"`)
	case "tool":
		obj := map[string]any{
			"type":     "function",
			"function": map[string]string{"name": tc.Name},
		}
		b, err := json.Marshal(obj)
		if err != nil {
			return nil, nil, fmt.Errorf("translate messages->chat: encode tool_choice: %w", err)
		}
		raw = b
	default:
		// Unknown policy: omit tool_choice and let OpenAI apply its default
		// rather than emit a value the upstream may reject.
		raw = nil
	}
	return raw, parallel, nil
}

// translateMessages maps the Anthropic system prompt + conversation into the
// OpenAI message array. A system prompt becomes a leading SystemMessage. An
// Anthropic user turn carrying tool_result blocks is split: each tool_result
// becomes its own OpenAI ToolMessage (OpenAI models tool results as a distinct
// role) emitted before any remaining user content.
func translateMessages(src messages.MessagesRequest) (chat.RequestMessages, []Drop, error) {
	var out chat.RequestMessages
	var drops []Drop

	if sysMsg, sd, ok := translateSystem(src); ok {
		out = append(out, sysMsg)
		drops = append(drops, sd...)
	}

	for i, m := range src.Messages {
		msgs, md, err := translateMessage(i, m)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, msgs...)
		drops = append(drops, md...)
	}
	return out, drops, nil
}

// translateSystem projects the Anthropic system prompt (string or block array)
// into a single OpenAI SystemMessage. ok is false when there is no system
// prompt. Block-level cache_control is dropped.
func translateSystem(src messages.MessagesRequest) (chat.RequestMessage, []Drop, bool) {
	if s, ok := src.SystemAsString(); ok {
		return &chat.SystemMessage{RoleField: "system", Content: s}, nil, true
	}
	if blocks, ok := src.SystemAsBlocks(); ok {
		var drops []Drop
		text := ""
		for i, b := range blocks {
			if i > 0 {
				text += "\n\n"
			}
			text += b.Text
			if b.CacheControl != nil {
				drops = append(drops, Drop{Field: fmt.Sprintf("system[%d].cache_control", i), Reason: reasonNoTargetEquivalent})
			}
		}
		return &chat.SystemMessage{RoleField: "system", Content: text}, drops, true
	}
	return nil, nil, false
}

// translateMessage maps one Anthropic message into one or more OpenAI messages.
// idx is the source index, used to label drops.
func translateMessage(idx int, m messages.Message) ([]chat.RequestMessage, []Drop, error) {
	// String content is the simple case for both user and assistant turns.
	if s, ok := m.ContentAsString(); ok {
		switch m.Role {
		case "assistant":
			return []chat.RequestMessage{&chat.AssistantMessage{RoleField: "assistant", Content: chat.NewStringContent(s)}}, nil, nil
		default:
			return []chat.RequestMessage{&chat.UserMessage{RoleField: "user", Content: chat.NewStringContent(s)}}, nil, nil
		}
	}

	blocks, ok := m.ContentAsBlocks()
	if !ok {
		return nil, nil, fmt.Errorf("translate messages->chat: message[%d]: content is neither string nor block array", idx)
	}

	if m.Role == "assistant" {
		return translateAssistantBlocks(idx, blocks)
	}
	return translateUserBlocks(idx, blocks)
}

// translateAssistantBlocks builds a single OpenAI AssistantMessage from an
// assistant turn's blocks: text blocks concatenate into Content, tool_use
// blocks become tool_calls. thinking / redacted_thinking blocks have no OpenAI
// request representation and are dropped.
func translateAssistantBlocks(idx int, blocks []messages.ContentBlock) ([]chat.RequestMessage, []Drop, error) {
	var drops []Drop
	asst := &chat.AssistantMessage{RoleField: "assistant"}
	text := ""
	for bi, b := range blocks {
		switch blk := b.(type) {
		case *messages.TextBlock:
			text += blk.Text
		case *messages.ToolUseBlock:
			args := "{}"
			if len(blk.Input) > 0 {
				args = string(blk.Input)
			}
			asst.ToolCalls = append(asst.ToolCalls, chat.ToolCall{
				ID:       blk.ID,
				Type:     "function",
				Function: chat.ToolCallFunction{Name: blk.Name, Arguments: args},
			})
		case *messages.ThinkingBlock:
			drops = append(drops, Drop{Field: fmt.Sprintf("messages[%d].content[%d].thinking", idx, bi), Reason: reasonNoTargetEquivalent})
		case *messages.RedactedThinkingBlock:
			drops = append(drops, Drop{Field: fmt.Sprintf("messages[%d].content[%d].redacted_thinking", idx, bi), Reason: reasonNoTargetEquivalent})
		default:
			drops = append(drops, Drop{Field: fmt.Sprintf("messages[%d].content[%d].%s", idx, bi, b.BlockType()), Reason: reasonNoTargetEquivalent})
		}
	}
	if text != "" {
		asst.Content = chat.NewStringContent(text)
	}
	return []chat.RequestMessage{asst}, drops, nil
}

// translateUserBlocks builds OpenAI messages from a user turn's blocks.
// tool_result blocks become ToolMessages (emitted first, to immediately follow
// the assistant tool_calls they answer); text/image blocks become a UserMessage
// with content parts.
func translateUserBlocks(idx int, blocks []messages.ContentBlock) ([]chat.RequestMessage, []Drop, error) {
	var out []chat.RequestMessage
	var drops []Drop
	var parts []chat.ContentPart

	for bi, b := range blocks {
		switch blk := b.(type) {
		case *messages.TextBlock:
			parts = append(parts, &chat.TextContentPart{Type: "text", Text: blk.Text})
		case *messages.ImageBlock:
			parts = append(parts, &chat.ImageURLContentPart{Type: "image_url", ImageURL: chat.ImageURL{URL: imageSourceToURL(blk.Source)}})
		case *messages.ToolResultBlock:
			tm, td, err := translateToolResult(idx, bi, blk)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, tm)
			drops = append(drops, td...)
		default:
			drops = append(drops, Drop{Field: fmt.Sprintf("messages[%d].content[%d].%s", idx, bi, b.BlockType()), Reason: reasonNoTargetEquivalent})
		}
	}

	if len(parts) > 0 {
		content, err := chat.NewPartsContent(parts)
		if err != nil {
			return nil, nil, fmt.Errorf("translate messages->chat: message[%d]: encode user content: %w", idx, err)
		}
		out = append(out, &chat.UserMessage{RoleField: "user", Content: content})
	}
	return out, drops, nil
}

// translateToolResult maps an Anthropic tool_result block to an OpenAI
// ToolMessage. The result content is string-or-blocks on the Anthropic side;
// string content maps to a string, block content maps to text/image parts.
func translateToolResult(idx, bi int, blk *messages.ToolResultBlock) (chat.RequestMessage, []Drop, error) {
	tm := &chat.ToolMessage{RoleField: "tool", ToolCallID: blk.ToolUseID}

	if len(blk.Content) == 0 {
		tm.Content = chat.NewStringContent("")
		return tm, nil, nil
	}

	var s string
	if err := json.Unmarshal(blk.Content, &s); err == nil {
		tm.Content = chat.NewStringContent(s)
		return tm, nil, nil
	}

	resultBlocks, err := messages.UnmarshalContentBlocks(blk.Content)
	if err != nil {
		return nil, nil, fmt.Errorf("translate messages->chat: message[%d].content[%d]: decode tool_result: %w", idx, bi, err)
	}
	var parts []chat.ContentPart
	var drops []Drop
	for ri, rb := range resultBlocks {
		switch rblk := rb.(type) {
		case *messages.TextBlock:
			parts = append(parts, &chat.TextContentPart{Type: "text", Text: rblk.Text})
		case *messages.ImageBlock:
			parts = append(parts, &chat.ImageURLContentPart{Type: "image_url", ImageURL: chat.ImageURL{URL: imageSourceToURL(rblk.Source)}})
		default:
			drops = append(drops, Drop{Field: fmt.Sprintf("messages[%d].content[%d].tool_result[%d].%s", idx, bi, ri, rb.BlockType()), Reason: reasonNoTargetEquivalent})
		}
	}
	content, err := chat.NewPartsContent(parts)
	if err != nil {
		return nil, nil, fmt.Errorf("translate messages->chat: message[%d].content[%d]: encode tool_result: %w", idx, bi, err)
	}
	tm.Content = content
	return tm, drops, nil
}

// imageSourceToURL renders an Anthropic image source as an OpenAI image_url
// string: a base64 source becomes a data: URL, a url source passes through.
func imageSourceToURL(src messages.ImageSource) string {
	if src.Type == "url" {
		return src.URL
	}
	return fmt.Sprintf("data:%s;base64,%s", src.MediaType, src.Data)
}
