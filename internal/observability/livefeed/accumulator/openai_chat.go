package accumulator

import (
	"encoding/json"
	"sort"
	"strings"

	openaichat "github.com/andyjmorgan/sluice-gateway/providers/openai/chat"
)

// accumulateOpenAIChat walks a stream of OpenAI ChatCompletionChunk
// SSE events and reassembles them into the JSON-encoded
// ChatCompletionResponse the non-streaming endpoint would have
// returned.
//
// Top-level fields (id, object, created, model, system_fingerprint,
// service_tier) are taken from any chunk that carries them — they are
// identical across chunks in practice. Choices are keyed by index;
// per-choice role is taken from the first delta that sets it, content
// is concatenated text from every delta, tool calls are accumulated
// by their delta Index, and finish_reason is the last non-empty value
// observed. Usage is taken from the last chunk that carries one
// (typically only the terminal chunk when StreamOptions.IncludeUsage
// was set on the request).
//
// The OpenAI-compat surfaces on Anthropic and Gemini emit the same
// chunk shape, so this function backs all three providers when
// dispatched against the `chat_completions` endpoint.
func accumulateOpenAIChat(raw []byte) Result {
	state := newOpenAIState()
	res := Result{}

	for _, ev := range parseSSE(raw) {
		if ev.Data == "" {
			continue
		}
		if strings.TrimSpace(ev.Data) == "[DONE]" {
			break
		}
		var chunk openaichat.ChatCompletionChunk
		if err := json.Unmarshal([]byte(ev.Data), &chunk); err != nil {
			res.Partial = true
			continue
		}
		state.absorb(&chunk)
	}

	out, err := json.Marshal(state.assemble())
	if err != nil {
		// Marshalling our own types should not fail — if it does, fall
		// back to Partial so the caller surfaces the raw bytes.
		res.Partial = true
		return res
	}
	res.Assembled = out
	return res
}

// openAIState tracks the in-flight reconstruction across chunks.
type openAIState struct {
	id                string
	object            string
	created           int64
	model             string
	systemFingerprint string
	serviceTier       string
	usage             *openaichat.Usage
	choices           map[int]*openAIChoiceState
}

type openAIChoiceState struct {
	index        int
	role         string
	content      strings.Builder
	contentSeen  bool
	refusal      *string
	finishReason *string
	tools        map[int]*openAIToolState
	toolOrder    []int
}

type openAIToolState struct {
	id      string
	kind    string
	name    string
	argsBuf strings.Builder
}

func newOpenAIState() *openAIState {
	return &openAIState{choices: map[int]*openAIChoiceState{}}
}

func (s *openAIState) absorb(chunk *openaichat.ChatCompletionChunk) {
	if chunk.ID != "" {
		s.id = chunk.ID
	}
	if chunk.Object != "" {
		// Non-streaming responses use "chat.completion"; the streaming
		// chunks use "chat.completion.chunk". Rewrite to the
		// non-streaming discriminator so the assembled object reads
		// like the non-streaming endpoint would have returned it.
		s.object = "chat.completion"
	}
	if chunk.Created != 0 {
		s.created = chunk.Created
	}
	if chunk.Model != "" {
		s.model = chunk.Model
	}
	if chunk.SystemFingerprint != "" {
		s.systemFingerprint = chunk.SystemFingerprint
	}
	if chunk.ServiceTier != "" {
		s.serviceTier = chunk.ServiceTier
	}
	if chunk.Usage != nil {
		s.usage = chunk.Usage
	}

	for _, cc := range chunk.Choices {
		ch, ok := s.choices[cc.Index]
		if !ok {
			ch = &openAIChoiceState{index: cc.Index, tools: map[int]*openAIToolState{}}
			s.choices[cc.Index] = ch
		}
		if cc.Delta.Role != "" {
			ch.role = cc.Delta.Role
		}
		if text := decodeContentString(cc.Delta.Content); text != "" {
			ch.content.WriteString(text)
			ch.contentSeen = true
		}
		if cc.Delta.Refusal != nil {
			ch.refusal = cc.Delta.Refusal
		}
		for _, td := range cc.Delta.ToolCalls {
			tool, ok := ch.tools[td.Index]
			if !ok {
				tool = &openAIToolState{}
				ch.tools[td.Index] = tool
				ch.toolOrder = append(ch.toolOrder, td.Index)
			}
			if td.ID != "" {
				tool.id = td.ID
			}
			if td.Type != "" {
				tool.kind = td.Type
			}
			if td.Function != nil {
				if td.Function.Name != "" {
					tool.name = td.Function.Name
				}
				tool.argsBuf.WriteString(td.Function.Arguments)
			}
		}
		if cc.FinishReason != nil {
			fr := *cc.FinishReason
			ch.finishReason = &fr
		}
	}
}

// assemble materialises the in-flight state into a ChatCompletionResponse
// matching what the non-streaming endpoint would have returned.
func (s *openAIState) assemble() openaichat.ChatCompletionResponse {
	indices := make([]int, 0, len(s.choices))
	for idx := range s.choices {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	choices := make([]openaichat.Choice, 0, len(indices))
	for _, idx := range indices {
		ch := s.choices[idx]
		msg := openaichat.ResponseMessage{Role: ch.role}
		if ch.contentSeen {
			b, _ := json.Marshal(ch.content.String())
			msg.Content = b
		}
		if ch.refusal != nil {
			r := *ch.refusal
			msg.Refusal = &r
		}
		if len(ch.toolOrder) > 0 {
			tools := make([]openaichat.ToolCall, 0, len(ch.toolOrder))
			for _, tIdx := range ch.toolOrder {
				t := ch.tools[tIdx]
				kind := t.kind
				if kind == "" {
					kind = "function"
				}
				tools = append(tools, openaichat.ToolCall{
					ID:   t.id,
					Type: kind,
					Function: openaichat.ToolCallFunction{
						Name:      t.name,
						Arguments: t.argsBuf.String(),
					},
				})
			}
			msg.ToolCalls = tools
		}
		choices = append(choices, openaichat.Choice{
			Index:        ch.index,
			Message:      msg,
			FinishReason: ch.finishReason,
		})
	}

	object := s.object
	if object == "" {
		object = "chat.completion"
	}
	return openaichat.ChatCompletionResponse{
		ID:                s.id,
		Object:            object,
		Created:           s.created,
		Model:             s.model,
		SystemFingerprint: s.systemFingerprint,
		ServiceTier:       s.serviceTier,
		Choices:           choices,
		Usage:             s.usage,
	}
}

// decodeContentString extracts the string payload from a Chat
// completion delta's content field. The field's wire shape is either
// a bare string ("hi") or an array of content-part objects (vision /
// audio inputs only appear on requests, but the type is shared). We
// concat any string-typed parts and ignore the rest — non-text
// content blocks on streaming responses are vanishingly rare today.
func decodeContentString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Fast path: most chunks are a bare string.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Array-of-parts path.
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}
