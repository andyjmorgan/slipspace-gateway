package accumulator

import (
	"encoding/json"
	"strings"

	openaichat "github.com/andyjmorgan/sluice-gateway/providers/openai/chat"
)

// accumulateOpenAIChat walks a stream of OpenAI ChatCompletionChunk
// SSE events and reassembles the assistant's textual reply plus any
// tool/function-call invocations.
//
// One chunk per frame: `data: {"choices":[{"delta":{"content":"…"}}]}`.
// The terminal `data: [DONE]` sentinel is recognised and stops the
// walk. Tool-call deltas arrive piecewise keyed by Index; we
// accumulate by index across frames into the final ToolCall list.
//
// The OpenAI-compat surfaces on Anthropic and Gemini emit the same
// chunk shape, so this function backs all three providers when
// dispatched against the `chat_completions` endpoint.
func accumulateOpenAIChat(raw []byte) Result {
	var text strings.Builder
	tools := map[int]*ToolCall{}
	indices := []int{} // preserve insertion order for stable output

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
		for _, choice := range chunk.Choices {
			if content := decodeContentString(choice.Delta.Content); content != "" {
				text.WriteString(content)
			}
			for _, td := range choice.Delta.ToolCalls {
				existing, ok := tools[td.Index]
				if !ok {
					existing = &ToolCall{ID: td.ID}
					tools[td.Index] = existing
					indices = append(indices, td.Index)
				} else if existing.ID == "" && td.ID != "" {
					existing.ID = td.ID
				}
				if td.Function != nil {
					if existing.Name == "" && td.Function.Name != "" {
						existing.Name = td.Function.Name
					}
					existing.Arguments += td.Function.Arguments
				}
			}
		}
	}

	res.Text = text.String()
	if len(indices) > 0 {
		out := make([]ToolCall, 0, len(indices))
		for _, idx := range indices {
			if tc := tools[idx]; tc != nil {
				out = append(out, *tc)
			}
		}
		res.ToolCalls = out
	}
	return res
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
