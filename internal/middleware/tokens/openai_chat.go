package tokens

import "encoding/json"

// openaiChatUsage is the wire shape OpenAI emits in the `usage` field of
// both ChatCompletionResponse (non-stream body) and the terminal
// streaming chunk when stream_options.include_usage is true. Anthropic's
// and Gemini's OpenAI-compat surfaces emit the same shape under the
// same `chat_completions` endpoint key.
//
// Defined locally rather than borrowed from providers/openai/chat.Usage
// because the providers package owns wire-faithful round-tripping
// (DynamicProperties, MarshalJSON, etc.) and is appropriately strict;
// here we want a forgiving, omitempty-tolerant subset that survives any
// future field additions without code change.
type openaiChatUsage struct {
	PromptTokens        int                           `json:"prompt_tokens"`
	CompletionTokens    int                           `json:"completion_tokens"`
	PromptTokensDetails *openaiChatPromptTokensDetail `json:"prompt_tokens_details,omitempty"`
}

type openaiChatPromptTokensDetail struct {
	CachedTokens int `json:"cached_tokens"`
}

// openaiChatChunk is the minimum we need to read off a streaming
// ChatCompletionChunk frame to spot the terminal usage block. We
// deliberately ignore `choices`, `delta`, `tool_calls`, and every other
// field — the accumulator package already handles content reassembly.
type openaiChatChunk struct {
	Usage *openaiChatUsage `json:"usage,omitempty"`
}

// extractOpenAIChat walks raw — either a single ChatCompletionResponse
// JSON body or an SSE stream of ChatCompletionChunk frames — and
// returns a Snapshot under StrategyLastWins.
//
// Empirically verified against a real OpenAI prod streaming response
// captured during design: chunks 1..N-1 carry "usage":null and a
// streaming choice; chunk N carries "choices":[] and the full usage
// block, followed by the "[DONE]" sentinel. LastWins is trivially
// correct because at most one chunk in a successful stream carries a
// non-zero usage. A client-cancelled stream may not emit the terminal
// chunk at all — in that case Recognised stays false and the reporter
// leaves the counter unbumped.
func extractOpenAIChat(frames [][]byte) Snapshot {
	var agg Aggregator
	for _, f := range frames {
		var chunk openaiChatChunk
		if err := json.Unmarshal(f, &chunk); err != nil || chunk.Usage == nil {
			continue
		}
		agg.Handle(StrategyLastWins, openaiChatUsageToObservation(chunk.Usage))
	}
	return agg.Snapshot()
}

func openaiChatUsageToObservation(u *openaiChatUsage) Usage {
	out := Usage{
		Input:  u.PromptTokens,
		Output: u.CompletionTokens,
	}
	if u.PromptTokensDetails != nil {
		out.Cached = u.PromptTokensDetails.CachedTokens
	}
	return out
}
