package tokens

import (
	"encoding/json"
	"strings"
)

// openaiResponsesUsage is the `usage` shape on a /v1/responses non-
// stream body and on the terminal `response.completed` SSE event.
// Field names differ from chat completions: input_tokens /
// output_tokens / input_tokens_details — kept separate from
// openaiChatUsage rather than smushed into one because a regression
// where the wrong field name is read would silently zero out the
// counter for a whole endpoint family.
type openaiResponsesUsage struct {
	InputTokens        int                               `json:"input_tokens"`
	OutputTokens       int                               `json:"output_tokens"`
	InputTokensDetails *openaiResponsesInputTokensDetail `json:"input_tokens_details,omitempty"`
}

type openaiResponsesInputTokensDetail struct {
	CachedTokens int `json:"cached_tokens"`
}

// openaiResponsesBody covers both shapes of payload we need to read:
// the top-level non-streaming response (usage nested directly) and the
// SSE event envelope where the same response object is nested under
// `response` (`response.completed`, `response.in_progress`, etc.).
type openaiResponsesBody struct {
	Usage    *openaiResponsesUsage          `json:"usage,omitempty"`
	Response *openaiResponsesNestedResponse `json:"response,omitempty"`
}

type openaiResponsesNestedResponse struct {
	Usage *openaiResponsesUsage `json:"usage,omitempty"`
}

// extractOpenAIResponses walks raw under StrategyLastWins. On a non-
// streaming body, `usage` is a top-level field. On the streaming
// surface, `usage` arrives inside the `response.completed` event's
// nested `response` object; intermediate `response.in_progress` events
// carry the same shape but with usage either absent or partial — last
// non-nil wins.
func extractOpenAIResponses(raw []byte) Snapshot {
	var agg Aggregator

	if looksLikeJSON(raw) {
		var body openaiResponsesBody
		if err := json.Unmarshal(raw, &body); err == nil {
			if u := pickResponsesUsage(&body); u != nil {
				agg.Handle(StrategyLastWins, openaiResponsesUsageToObservation(u))
			}
		}
		return agg.Snapshot()
	}

	for _, ev := range parseSSE(raw) {
		if ev.Data == "" || strings.TrimSpace(ev.Data) == "[DONE]" {
			continue
		}
		var frame openaiResponsesBody
		if err := json.Unmarshal([]byte(ev.Data), &frame); err != nil {
			continue
		}
		u := pickResponsesUsage(&frame)
		if u == nil {
			continue
		}
		agg.Handle(StrategyLastWins, openaiResponsesUsageToObservation(u))
	}
	return agg.Snapshot()
}

func pickResponsesUsage(b *openaiResponsesBody) *openaiResponsesUsage {
	if b.Usage != nil {
		return b.Usage
	}
	if b.Response != nil && b.Response.Usage != nil {
		return b.Response.Usage
	}
	return nil
}

func openaiResponsesUsageToObservation(u *openaiResponsesUsage) Usage {
	out := Usage{
		Input:  u.InputTokens,
		Output: u.OutputTokens,
	}
	if u.InputTokensDetails != nil {
		out.Cached = u.InputTokensDetails.CachedTokens
	}
	return out
}
