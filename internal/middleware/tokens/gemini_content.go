package tokens

import "encoding/json"

// geminiUsageMetadata is the `usageMetadata` shape on a Gemini
// generateContent response (and on each chunk of an SSE
// streamGenerateContent stream). Field names are camelCase per
// Google's REST convention.
//
// We don't read CandidatesTokenCount directly — empirically Gemini may
// omit it when the model spent its entire output budget on hidden
// thoughts (verified with a real prod call against gemini-2.5-flash:
// promptTokenCount=6, thoughtsTokenCount=17, totalTokenCount=23, no
// candidatesTokenCount field). Output is therefore derived as
// totalTokenCount - promptTokenCount, which folds candidates +
// thoughts + any future generated-token sub-bucket together — exactly
// what the customer pays for.
type geminiUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
}

// geminiResponse is the smallest payload shape we need to decode for
// usage extraction. Both non-stream bodies and per-chunk stream frames
// share this top-level layout (GenerateContentResponse).
type geminiResponse struct {
	UsageMetadata *geminiUsageMetadata `json:"usageMetadata,omitempty"`
}

// extractGeminiContent walks raw under StrategyLastWins. Non-stream
// bodies carry usageMetadata directly on the response; streaming
// responses (alt=sse) carry one GenerateContentResponse per chunk, and
// per Google's docs the final chunk carries the authoritative
// usageMetadata. Some models emit usageMetadata on intermediate
// chunks too with running totals — LastWins handles either shape.
//
// Known model-specific bug: gemini-2.5-pro-preview has been reported
// to return nil usageMetadata in the final stream chunk. In that case
// Snapshot.Recognised stays false and the reporter leaves the counter
// unbumped — preferable to inventing a count.
func extractGeminiContent(frames [][]byte) Snapshot {
	var agg Aggregator
	for _, f := range frames {
		var frame geminiResponse
		if err := json.Unmarshal(f, &frame); err != nil || frame.UsageMetadata == nil {
			continue
		}
		agg.Handle(StrategyLastWins, geminiUsageToObservation(frame.UsageMetadata))
	}
	return agg.Snapshot()
}

func geminiUsageToObservation(u *geminiUsageMetadata) Usage {
	out := Usage{
		Input:  u.PromptTokenCount,
		Cached: u.CachedContentTokenCount,
	}
	// Derive output from the gross delta. Avoids depending on
	// candidatesTokenCount being populated (it is omitted when the
	// model produced no candidate, e.g. MAX_TOKENS hit during the
	// thinking phase) and naturally includes reasoning/thoughts
	// tokens that the customer is billed for.
	if u.TotalTokenCount > u.PromptTokenCount {
		out.Output = u.TotalTokenCount - u.PromptTokenCount
	}
	return out
}
