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
func extractOpenAIResponses(frames [][]byte) Snapshot {
	var agg Aggregator
	for _, f := range frames {
		var frame openaiResponsesBody
		if err := json.Unmarshal(f, &frame); err != nil {
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

// responsesToolItem is the minimal shape of a Responses output item (or
// streamed output_item envelope) needed to count server-tool invocations.
type responsesToolItem struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// responsesToolFrame covers every place output items appear across the
// Responses surfaces: the non-streaming body's `output`, the streamed
// `response.output_item.added/done` events' `item`, and the terminal
// `response.completed` event's nested `response.output`.
type responsesToolFrame struct {
	Type     string              `json:"type"`
	Output   []responsesToolItem `json:"output"`
	Item     *responsesToolItem  `json:"item"`
	Response *struct {
		Output []responsesToolItem `json:"output"`
	} `json:"response"`
}

// extractResponsesServerToolCalls counts server-executed tool invocations
// by output-item type (web_search_call, file_search_call,
// code_interpreter_call, image_generation_call, mcp_call, future
// built-ins). OpenAI bills these per call / per session *outside* the
// usage block, so the usage extractor can't see them — the output items
// are the only wire evidence. Keys are the verbatim item types; the
// pricing layer decides which carry a rate, so counting a non-billable
// type is harmless.
//
// The same item appears up to three times on a stream (output_item.added,
// output_item.done, response.completed's output array) — items are
// deduplicated by id. function_call is client-executed and the *_output
// echo types are inputs, never tool activity; both are excluded.
func extractResponsesServerToolCalls(frames [][]byte) map[string]int {
	seen := map[string]bool{}
	counts := map[string]int{}
	add := func(it responsesToolItem) {
		if !isResponsesServerToolCall(it.Type) {
			return
		}
		if it.ID != "" {
			key := it.Type + "\x00" + it.ID
			if seen[key] {
				return
			}
			seen[key] = true
		}
		counts[it.Type]++
	}
	for _, f := range frames {
		var fr responsesToolFrame
		if err := json.Unmarshal(f, &fr); err != nil {
			continue
		}
		for _, it := range fr.Output {
			add(it)
		}
		if fr.Item != nil {
			add(*fr.Item)
		}
		if fr.Response != nil {
			for _, it := range fr.Response.Output {
				add(it)
			}
		}
	}
	if len(counts) == 0 {
		return nil
	}
	return counts
}

// isResponsesServerToolCall reports whether a Responses output-item type
// represents a server-executed tool invocation: any "*_call" item except
// the client-executed function_call. The *_output input-item echoes
// (function_call_output, computer_call_output) don't match the suffix and
// message/reasoning items are not calls at all.
func isResponsesServerToolCall(itemType string) bool {
	if itemType == "function_call" {
		return false
	}
	return strings.HasSuffix(itemType, "_call")
}
