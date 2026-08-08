package translate

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/andyjmorgan/slipspace-gateway/protocols/anthropic/messages"
	"github.com/andyjmorgan/slipspace-gateway/protocols/openai/chat"
)

// defaultAnthropicMaxTokens is supplied when an OpenAI Chat request carries
// neither max_tokens nor max_completion_tokens: Anthropic requires max_tokens
// on every request, so the translator must synthesise one or the upstream
// rejects the call. The value is a conservative cap, not a feature drop.
const defaultAnthropicMaxTokens = 4096

// translateChatRequestToMessages maps an OpenAI Chat Completions request body
// into an Anthropic Messages request body. It returns the translated bytes plus
// the list of source features that had no Anthropic equivalent and were
// dropped.
//
// Model is carried verbatim — model-name remapping is a separate changeModelName
// concern, not translation's. Unknown top-level OpenAI fields (the request's
// DynamicProperties) are intentionally not carried onto the Anthropic request:
// they are vendor-specific and meaningless to Anthropic, so they are neither
// forwarded nor counted as feature drops.
func translateChatRequestToMessages(body []byte) ([]byte, []Drop, error) {
	var src chat.ChatCompletionRequest
	if err := json.Unmarshal(body, &src); err != nil {
		return nil, nil, fmt.Errorf("translate chat->messages: decode request: %w", err)
	}

	var drops []Drop
	addDropIf := func(cond bool, field string) {
		if cond {
			drops = append(drops, Drop{Field: field, Reason: reasonNoTargetEquivalent})
		}
	}

	dst := messages.MessagesRequest{
		Model:       src.Model,
		Temperature: src.Temperature,
		TopP:        src.TopP,
		Stream:      src.Stream,
		ServiceTier: src.ServiceTier,
	}

	// Anthropic requires max_tokens; prefer the reasoning-aware
	// max_completion_tokens, fall back to the legacy max_tokens, and synthesise
	// a default only when the client sent neither.
	switch {
	case src.MaxCompletionTokens != nil:
		dst.MaxTokens = *src.MaxCompletionTokens
	case src.MaxTokens != nil:
		dst.MaxTokens = *src.MaxTokens
	default:
		dst.MaxTokens = defaultAnthropicMaxTokens
	}

	if len(src.Stop) > 0 {
		seqs, err := stopToSequences(src.Stop)
		if err != nil {
			return nil, nil, err
		}
		dst.StopSequences = seqs
	}

	// OpenAI's reasoning_effort maps cleanly to Anthropic's output_config.effort.
	// The nested Ollama-compat reasoning.effort spelling maps to the same
	// control; the flat field wins when both are present (they express one
	// knob, so there is nothing to drop).
	switch {
	case src.ReasoningEffort != "":
		dst.OutputConfig = &messages.OutputConfig{Effort: src.ReasoningEffort}
	case src.Reasoning != nil && src.Reasoning.Effort != "":
		dst.OutputConfig = &messages.OutputConfig{Effort: src.Reasoning.Effort}
	}

	// OpenAI's end-user identifier maps to Anthropic metadata.user_id.
	if src.User != "" {
		dst.Metadata = &messages.Metadata{UserID: src.User}
	}

	// Sampling knobs and features Anthropic Messages does not express.
	addDropIf(src.N != nil, "n")
	addDropIf(src.StreamOptions != nil, "stream_options")
	addDropIf(src.PresencePenalty != nil, "presence_penalty")
	addDropIf(src.FrequencyPenalty != nil, "frequency_penalty")
	addDropIf(len(src.LogitBias) > 0, "logit_bias")
	addDropIf(src.LogProbs != nil, "logprobs")
	addDropIf(src.TopLogProbs != nil, "top_logprobs")
	addDropIf(src.ResponseFormat != nil, "response_format")
	addDropIf(src.Seed != nil, "seed")
	addDropIf(len(src.Modalities) > 0, "modalities")
	addDropIf(src.Audio != nil, "audio")
	addDropIf(len(src.Metadata) > 0, "metadata")
	addDropIf(src.Store != nil, "store")
	// Ollama/vLLM OpenAI-compat extensions with no Anthropic equivalent.
	// (reasoning.effort is not in this list — it maps to output_config.effort
	// above.)
	addDropIf(src.Think != nil, "think")
	addDropIf(len(src.ChatTemplateKwargs) > 0, "chat_template_kwargs")

	if len(src.Tools) > 0 {
		tools, td := translateToolsToMessages(src.Tools)
		dst.Tools = tools
		drops = append(drops, td...)
	}

	tc, tcd, err := translateToolChoiceToMessages(src.ToolChoice, src.ParallelToolCalls)
	if err != nil {
		return nil, nil, err
	}
	dst.ToolChoice = tc
	drops = append(drops, tcd...)

	system, msgs, md, err := translateChatMessages(src.Messages)
	if err != nil {
		return nil, nil, err
	}
	if system != "" {
		if err := dst.SetSystemString(system); err != nil {
			return nil, nil, fmt.Errorf("translate chat->messages: encode system: %w", err)
		}
	}
	dst.Messages = msgs
	drops = append(drops, md...)

	out, err := json.Marshal(dst)
	if err != nil {
		return nil, nil, fmt.Errorf("translate chat->messages: encode request: %w", err)
	}
	return out, drops, nil
}

// stopToSequences normalises OpenAI's polymorphic stop field (a single string
// or an array of strings) into Anthropic's stop_sequences array.
func stopToSequences(raw json.RawMessage) ([]string, error) {
	var seqs []string
	if err := json.Unmarshal(raw, &seqs); err == nil {
		return seqs, nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err != nil {
		return nil, fmt.Errorf("translate chat->messages: decode stop: %w", err)
	}
	return []string{single}, nil
}

// translateToolsToMessages maps OpenAI function tools to Anthropic tool
// definitions. Non-function tool kinds and the function-level strict flag have
// no Anthropic equivalent and are dropped.
func translateToolsToMessages(in []chat.Tool) ([]messages.Tool, []Drop) {
	out := make([]messages.Tool, 0, len(in))
	var drops []Drop
	for i, t := range in {
		if t.Type != "function" || t.Function == nil {
			drops = append(drops, Drop{Field: fmt.Sprintf("tools[%d]", i), Reason: reasonNoTargetEquivalent})
			continue
		}
		out = append(out, messages.Tool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
		if t.Function.Strict != nil {
			drops = append(drops, Drop{Field: fmt.Sprintf("tools[%d].function.strict", i), Reason: reasonNoTargetEquivalent})
		}
	}
	return out, drops
}

// translateToolChoiceToMessages maps OpenAI's tool_choice (a string or an
// object) plus the top-level parallel_tool_calls flag into a single Anthropic
// ToolChoice. Anthropic carries "no parallel tool use" inside tool_choice, so a
// parallel_tool_calls=false with no explicit tool_choice still produces an
// "auto" choice to hang disable_parallel_tool_use on.
func translateToolChoiceToMessages(raw json.RawMessage, parallel *bool) (*messages.ToolChoice, []Drop, error) {
	var tc *messages.ToolChoice
	var drops []Drop

	if len(raw) > 0 {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			switch s {
			case "auto":
				tc = &messages.ToolChoice{Type: "auto"}
			case "none":
				tc = &messages.ToolChoice{Type: "none"}
			case "required":
				tc = &messages.ToolChoice{Type: "any"}
			default:
				drops = append(drops, Drop{Field: "tool_choice", Reason: "unknown tool_choice value, omitted"})
			}
		} else {
			var obj struct {
				Type     string `json:"type"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			}
			if err := json.Unmarshal(raw, &obj); err != nil {
				return nil, nil, fmt.Errorf("translate chat->messages: decode tool_choice: %w", err)
			}
			if obj.Type == "function" && obj.Function.Name != "" {
				tc = &messages.ToolChoice{Type: "tool", Name: obj.Function.Name}
			} else {
				drops = append(drops, Drop{Field: "tool_choice", Reason: "unsupported tool_choice object, omitted"})
			}
		}
	}

	if parallel != nil {
		if tc == nil {
			tc = &messages.ToolChoice{Type: "auto"}
		}
		disable := !*parallel
		tc.DisableParallelToolUse = &disable
	}

	return tc, drops, nil
}

// translateChatMessages projects the flat OpenAI message array into Anthropic's
// top-level system prompt plus the user/assistant conversation. System and
// developer messages fold into the system string. A run of user + tool messages
// accumulates into a single Anthropic user turn (tool results become
// tool_result blocks), which is flushed when an assistant turn is reached — the
// inverse of the forward translator's per-turn split.
func translateChatMessages(in chat.RequestMessages) (system string, msgs []messages.Message, drops []Drop, err error) {
	var sysParts []string
	var pendingUser []messages.ContentBlock

	flush := func() error {
		if len(pendingUser) == 0 {
			return nil
		}
		m := messages.Message{Role: "user"}
		if serr := m.SetContentBlocks(pendingUser); serr != nil {
			return fmt.Errorf("translate chat->messages: encode user content: %w", serr)
		}
		msgs = append(msgs, m)
		pendingUser = nil
		return nil
	}

	for i, rm := range in {
		switch msg := rm.(type) {
		case *chat.SystemMessage:
			sysParts = append(sysParts, msg.Content)
		case *chat.DeveloperMessage:
			sysParts = append(sysParts, msg.Content)
		case *chat.UserMessage:
			blocks, d, uerr := userContentToBlocks(i, msg.Content)
			if uerr != nil {
				return "", nil, nil, uerr
			}
			pendingUser = append(pendingUser, blocks...)
			drops = append(drops, d...)
		case *chat.ToolMessage:
			blk, d, terr := toolMessageToResult(i, msg)
			if terr != nil {
				return "", nil, nil, terr
			}
			pendingUser = append(pendingUser, blk)
			drops = append(drops, d...)
		case *chat.AssistantMessage:
			if ferr := flush(); ferr != nil {
				return "", nil, nil, ferr
			}
			am, d, aerr := assistantToMessage(i, msg)
			if aerr != nil {
				return "", nil, nil, aerr
			}
			msgs = append(msgs, am)
			drops = append(drops, d...)
		case *chat.FunctionMessage:
			drops = append(drops, Drop{Field: fmt.Sprintf("messages[%d].function", i), Reason: "legacy function role has no Anthropic equivalent"})
		default:
			drops = append(drops, Drop{Field: fmt.Sprintf("messages[%d].%s", i, rm.Role()), Reason: reasonNoTargetEquivalent})
		}
	}

	if ferr := flush(); ferr != nil {
		return "", nil, nil, ferr
	}

	return strings.Join(sysParts, "\n\n"), msgs, drops, nil
}

// userContentToBlocks maps an OpenAI user message's content into Anthropic
// content blocks. String content becomes a single text block; content parts map
// text -> text and image_url -> image. Audio/file/refusal/unknown parts have no
// Anthropic user-content equivalent and are dropped.
func userContentToBlocks(idx int, c chat.MessageContent) ([]messages.ContentBlock, []Drop, error) {
	if c.IsNull() {
		return nil, nil, nil
	}
	if c.IsString() {
		s, err := c.AsString()
		if err != nil {
			return nil, nil, fmt.Errorf("translate chat->messages: message[%d]: decode user content: %w", idx, err)
		}
		return []messages.ContentBlock{&messages.TextBlock{Type: "text", Text: s}}, nil, nil
	}
	parts, err := c.AsParts()
	if err != nil {
		return nil, nil, fmt.Errorf("translate chat->messages: message[%d]: decode user content parts: %w", idx, err)
	}
	blocks, drops := contentPartsToBlocks(idx, -1, parts)
	return blocks, drops, nil
}

// contentPartsToBlocks maps OpenAI content parts (text, image_url) into Anthropic
// content blocks, dropping the parts Anthropic cannot express. When tr >= 0 the
// drop labels mark a tool_result's nested content; otherwise they mark a
// message's content.
func contentPartsToBlocks(idx, tr int, parts []chat.ContentPart) ([]messages.ContentBlock, []Drop) {
	var blocks []messages.ContentBlock
	var drops []Drop
	for pi, p := range parts {
		switch part := p.(type) {
		case *chat.TextContentPart:
			blocks = append(blocks, &messages.TextBlock{Type: "text", Text: part.Text})
		case *chat.ImageURLContentPart:
			blocks = append(blocks, &messages.ImageBlock{Type: "image", Source: imageURLToSource(part.ImageURL.URL)})
		default:
			drops = append(drops, Drop{Field: contentPartPath(idx, tr, pi, p.PartType()), Reason: reasonNoTargetEquivalent})
		}
	}
	return blocks, drops
}

// contentPartPath builds the drop-label path for a content part, distinguishing
// a message's own content from a tool_result's nested content.
func contentPartPath(idx, tr, pi int, kind string) string {
	if tr >= 0 {
		return fmt.Sprintf("messages[%d].tool_result.content[%d].%s", idx, pi, kind)
	}
	return fmt.Sprintf("messages[%d].content[%d].%s", idx, pi, kind)
}

// assistantToMessage maps an OpenAI assistant message into an Anthropic
// assistant turn: text content concatenates into a text block, tool_calls become
// tool_use blocks. A refusal maps to a text block (Anthropic has no refusal
// block) and audio replies are dropped.
func assistantToMessage(idx int, msg *chat.AssistantMessage) (messages.Message, []Drop, error) {
	var blocks []messages.ContentBlock
	var drops []Drop

	if !msg.Content.IsNull() {
		if msg.Content.IsString() {
			s, err := msg.Content.AsString()
			if err != nil {
				return messages.Message{}, nil, fmt.Errorf("translate chat->messages: message[%d]: decode assistant content: %w", idx, err)
			}
			if s != "" {
				blocks = append(blocks, &messages.TextBlock{Type: "text", Text: s})
			}
		} else {
			parts, err := msg.Content.AsParts()
			if err != nil {
				return messages.Message{}, nil, fmt.Errorf("translate chat->messages: message[%d]: decode assistant content parts: %w", idx, err)
			}
			for pi, p := range parts {
				switch part := p.(type) {
				case *chat.TextContentPart:
					blocks = append(blocks, &messages.TextBlock{Type: "text", Text: part.Text})
				case *chat.RefusalContentPart:
					blocks = append(blocks, &messages.TextBlock{Type: "text", Text: part.Refusal})
					drops = append(drops, Drop{Field: fmt.Sprintf("messages[%d].content[%d].refusal", idx, pi), Reason: "mapped to text; Anthropic has no refusal block"})
				default:
					drops = append(drops, Drop{Field: fmt.Sprintf("messages[%d].content[%d].%s", idx, pi, p.PartType()), Reason: reasonNoTargetEquivalent})
				}
			}
		}
	}

	if msg.Refusal != nil && *msg.Refusal != "" {
		blocks = append(blocks, &messages.TextBlock{Type: "text", Text: *msg.Refusal})
		drops = append(drops, Drop{Field: fmt.Sprintf("messages[%d].refusal", idx), Reason: "mapped to text; Anthropic has no refusal block"})
	}
	if msg.Audio != nil {
		drops = append(drops, Drop{Field: fmt.Sprintf("messages[%d].audio", idx), Reason: reasonNoTargetEquivalent})
	}

	for _, tc := range msg.ToolCalls {
		input := json.RawMessage("{}")
		if strings.TrimSpace(tc.Function.Arguments) != "" {
			input = json.RawMessage(tc.Function.Arguments)
		}
		blocks = append(blocks, &messages.ToolUseBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}

	m := messages.Message{Role: "assistant"}
	if err := m.SetContentBlocks(blocks); err != nil {
		return messages.Message{}, nil, fmt.Errorf("translate chat->messages: message[%d]: encode assistant content: %w", idx, err)
	}
	return m, drops, nil
}

// toolMessageToResult maps an OpenAI tool-result message into an Anthropic
// tool_result block. String content maps to a string; content parts map to a
// nested text/image block array.
func toolMessageToResult(idx int, msg *chat.ToolMessage) (messages.ContentBlock, []Drop, error) {
	blk := &messages.ToolResultBlock{Type: "tool_result", ToolUseID: msg.ToolCallID}

	if msg.Content.IsNull() {
		return blk, nil, nil
	}

	if msg.Content.IsString() {
		s, err := msg.Content.AsString()
		if err != nil {
			return nil, nil, fmt.Errorf("translate chat->messages: message[%d]: decode tool content: %w", idx, err)
		}
		raw, _ := json.Marshal(s) // marshaling a Go string cannot fail
		blk.Content = raw
		return blk, nil, nil
	}

	parts, err := msg.Content.AsParts()
	if err != nil {
		return nil, nil, fmt.Errorf("translate chat->messages: message[%d]: decode tool content parts: %w", idx, err)
	}
	rblocks, drops := contentPartsToBlocks(idx, idx, parts)
	raw, err := json.Marshal(rblocks)
	if err != nil {
		return nil, nil, fmt.Errorf("translate chat->messages: message[%d]: encode tool content: %w", idx, err)
	}
	blk.Content = raw
	return blk, drops, nil
}

// imageURLToSource renders an OpenAI image_url as an Anthropic image source: a
// "data:" URL is split back into a base64 source (media type + payload), and an
// http(s) URL passes through as a url source.
func imageURLToSource(url string) messages.ImageSource {
	if strings.HasPrefix(url, "data:") {
		rest := url[len("data:"):]
		if comma := strings.IndexByte(rest, ','); comma >= 0 {
			meta, data := rest[:comma], rest[comma+1:]
			mediaType := meta
			if semi := strings.IndexByte(meta, ';'); semi >= 0 {
				mediaType = meta[:semi]
			}
			return messages.ImageSource{Type: "base64", MediaType: mediaType, Data: data}
		}
	}
	return messages.ImageSource{Type: "url", URL: url}
}
