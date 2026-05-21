package tokens

import "encoding/json"

// anthropicUsage is the `usage` shape on both the non-streaming /v1/
// messages response body and the `message_start.message.usage` /
// `message_delta.usage` SSE events. Anthropic's docs are explicit that
// message_delta carries cumulative running totals, and a verified prod
// capture shows message_start emits the initial values (input,
// cache_creation, cache_read, plus a small initial output) — both
// satisfy StrategyLastWins.
//
// CacheCreation has both the legacy flat counter
// (cache_creation_input_tokens) and a newer nested per-TTL breakdown
// (cache_creation.{ephemeral_5m,ephemeral_1h}_input_tokens). We read
// the flat field; the nested struct is informational and round-trips
// untouched via the providers package's DynamicProperties.
type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// anthropicNonStream is the top-level response body when stream=false.
type anthropicNonStream struct {
	Usage *anthropicUsage `json:"usage,omitempty"`
}

// anthropicStreamFrame covers both event types we care about:
//
//   - message_start: usage lives at `message.usage`
//   - message_delta: usage lives at the top-level `usage`
//
// One struct can decode either by having both nested paths present;
// json.Unmarshal silently leaves the absent path nil.
type anthropicStreamFrame struct {
	Type    string                  `json:"type"`
	Usage   *anthropicUsage         `json:"usage,omitempty"`
	Message *anthropicStreamMessage `json:"message,omitempty"`
}

type anthropicStreamMessage struct {
	Usage *anthropicUsage `json:"usage,omitempty"`
}

// extractAnthropicMessages walks raw under StrategyLastWins. The
// message_delta event is the authoritative carrier of final totals;
// message_start contributes initial values that LastWins correctly
// supersedes. message_stop carries no usage. Other event types
// (content_block_start/delta/stop, ping, error) are skipped silently.
//
// The server-tool-use case from Anthropic's docs — input_tokens jumps
// from a small initial value at message_start to a larger total at
// message_delta — works correctly under LastWins because both
// emissions are observed and the last one wins on every field.
func extractAnthropicMessages(raw []byte) Snapshot {
	var agg Aggregator

	if looksLikeJSON(raw) {
		var body anthropicNonStream
		if err := json.Unmarshal(raw, &body); err == nil && body.Usage != nil {
			agg.Handle(StrategyLastWins, anthropicUsageToObservation(body.Usage))
		}
		return agg.Snapshot()
	}

	for _, ev := range parseSSE(raw) {
		if ev.Data == "" {
			continue
		}
		var frame anthropicStreamFrame
		if err := json.Unmarshal([]byte(ev.Data), &frame); err != nil {
			continue
		}
		var u *anthropicUsage
		switch {
		case frame.Type == "message_start" && frame.Message != nil:
			u = frame.Message.Usage
		case frame.Type == "message_delta":
			u = frame.Usage
		}
		if u == nil {
			continue
		}
		agg.Handle(StrategyLastWins, anthropicUsageToObservation(u))
	}
	return agg.Snapshot()
}

func anthropicUsageToObservation(u *anthropicUsage) Usage {
	// Anthropic's input_tokens is the *uncached* prompt size — cache
	// reads and writes are reported alongside as separate counts that
	// each carry their own price. For the gateway's gross-input
	// accounting we sum them so TokensIn reflects total prompt
	// content the customer paid for, matching the OpenAI/Gemini
	// convention where input_tokens already includes the cached
	// portion.
	return Usage{
		Input:         u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens,
		Output:        u.OutputTokens,
		Cached:        u.CacheReadInputTokens,
		CacheCreation: u.CacheCreationInputTokens,
	}
}
