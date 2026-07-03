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

	// PromptTokensDetails / CandidatesTokensDetails are Gemini's
	// per-modality breakdowns. Audio prompt tokens bill at a separate
	// (higher) rate on audio-capable models, so the AUDIO member of
	// each feeds the Snapshot's audio buckets; other modalities
	// (TEXT, IMAGE, VIDEO, DOCUMENT) stay folded in the gross counts.
	PromptTokensDetails     []geminiModalityTokenCount `json:"promptTokensDetails"`
	CandidatesTokensDetails []geminiModalityTokenCount `json:"candidatesTokensDetails"`
}

// geminiModalityTokenCount is one element of a *TokensDetails breakdown.
type geminiModalityTokenCount struct {
	Modality   string `json:"modality"`
	TokenCount int    `json:"tokenCount"`
}

// audioTokens returns the AUDIO member's count, 0 when absent.
func audioTokens(details []geminiModalityTokenCount) int {
	for _, d := range details {
		if d.Modality == "AUDIO" {
			return d.TokenCount
		}
	}
	return 0
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
		Input:       u.PromptTokenCount,
		Cached:      u.CachedContentTokenCount,
		InputAudio:  audioTokens(u.PromptTokensDetails),
		OutputAudio: audioTokens(u.CandidatesTokensDetails),
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

// geminiGroundingFrame is the minimal shape needed to count Google
// Search grounding queries: candidates[].groundingMetadata
// .webSearchQueries. Grounding bills per search query (a single prompt
// can issue several), reported nowhere in usageMetadata — the grounding
// block is the only wire evidence.
type geminiGroundingFrame struct {
	Candidates []struct {
		GroundingMetadata *struct {
			WebSearchQueries []string `json:"webSearchQueries"`
		} `json:"groundingMetadata"`
	} `json:"candidates"`
}

// extractGeminiServerToolCalls counts grounding web-search queries under
// the key "web_search_queries" (Google's own field name). Streaming
// emits groundingMetadata on the chunk(s) that carry grounded content;
// the cumulative final occurrence wins, matching the LastWins convention
// of the usage extractor. nil when the response was not grounded.
func extractGeminiServerToolCalls(frames [][]byte) map[string]int {
	n := 0
	for _, f := range frames {
		var fr geminiGroundingFrame
		if err := json.Unmarshal(f, &fr); err != nil {
			continue
		}
		for _, c := range fr.Candidates {
			if c.GroundingMetadata != nil && len(c.GroundingMetadata.WebSearchQueries) > 0 {
				n = len(c.GroundingMetadata.WebSearchQueries)
			}
		}
	}
	if n == 0 {
		return nil
	}
	return map[string]int{"web_search_queries": n}
}
