package accumulator

import (
	"encoding/json"
	"strings"

	geminicontent "github.com/andyjmorgan/sluice-gateway/providers/gemini/content"
)

// accumulateGeminiContent walks a Gemini streamGenerateContent SSE
// stream and reassembles the assistant's textual reply plus any
// function-call invocations.
//
// Gemini emits one GenerateContentResponse per SSE event; the
// candidates[0].content.parts list grows across events with text
// chunks and (rarely) functionCall parts. We concatenate TextParts in
// arrival order and treat each FunctionCallPart as a distinct tool
// call — Gemini does not stream function arguments delta-by-delta the
// way OpenAI / Anthropic do.
func accumulateGeminiContent(raw []byte) Result {
	var text strings.Builder
	var tools []ToolCall

	res := Result{}
	for _, ev := range parseSSE(raw) {
		if ev.Data == "" {
			continue
		}
		var chunk geminicontent.GenerateContentResponse
		if err := json.Unmarshal([]byte(ev.Data), &chunk); err != nil {
			res.Partial = true
			continue
		}
		if len(chunk.Candidates) == 0 {
			continue
		}
		// Use the first candidate; multi-candidate streaming is
		// extremely rare and the live tail does not need to render
		// every variant.
		cand := chunk.Candidates[0]
		if cand.Content == nil {
			continue
		}
		for _, p := range cand.Content.Parts {
			switch part := p.(type) {
			case *geminicontent.TextPart:
				text.WriteString(part.Text)
			case *geminicontent.FunctionCallPart:
				tools = append(tools, ToolCall{
					ID:        part.FunctionCall.ID,
					Name:      part.FunctionCall.Name,
					Arguments: string(part.FunctionCall.Args),
				})
			}
		}
	}

	res.Text = text.String()
	if len(tools) > 0 {
		res.ToolCalls = tools
	}
	return res
}
