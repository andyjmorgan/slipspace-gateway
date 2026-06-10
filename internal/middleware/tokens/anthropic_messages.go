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

	// ServerToolUse is the per-tool request-counter block Anthropic adds
	// when the model invoked server-executed tools (web_search_requests,
	// web_fetch_requests, and whatever counter the next server tool family
	// ships). Decoded generically — keys are NOT hardcoded — and as raw
	// values so a future non-integer member can never fail the whole usage
	// unmarshal and cost us the token counts.
	ServerToolUse map[string]json.RawMessage `json:"server_tool_use"`
}

// counters filters u.ServerToolUse down to its integer members. nil when
// the block was absent or carried no integer counters.
func (u *anthropicUsage) counters() map[string]int {
	if len(u.ServerToolUse) == 0 {
		return nil
	}
	m := make(map[string]int, len(u.ServerToolUse))
	for k, v := range u.ServerToolUse {
		var n int
		if json.Unmarshal(v, &n) == nil {
			m[k] = n
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
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
func extractAnthropicMessages(frames [][]byte) Snapshot {
	var agg Aggregator
	for _, f := range frames {
		var frame anthropicStreamFrame
		if err := json.Unmarshal(f, &frame); err != nil {
			continue
		}
		// usageForType: message_start nests usage under message.usage;
		// message_delta and the non-streaming body (type "message") carry it
		// at the top level — no other streaming frame does.
		u := frame.usageForType()
		if u == nil {
			continue
		}
		agg.Handle(StrategyLastWins, anthropicUsageToObservation(u))
	}
	return agg.Snapshot()
}

// extractAnthropicServerToolUse walks the same frames as
// extractAnthropicMessages and returns the usage.server_tool_use counters as
// a generic map (web_search_requests, web_fetch_requests, future keys). It
// rides outside Snapshot so Snapshot stays a comparable value type; the same
// last-wins logic applies — the cumulative message_delta emission is
// authoritative. nil when the response reported no server-tool counters.
func extractAnthropicServerToolUse(frames [][]byte) map[string]int {
	var out map[string]int
	for _, f := range frames {
		var frame anthropicStreamFrame
		if err := json.Unmarshal(f, &frame); err != nil {
			continue
		}
		u := frame.usageForType()
		if u == nil {
			continue
		}
		if m := u.counters(); m != nil {
			out = m
		}
	}
	return out
}

// usageForType resolves which usage block the frame carries, mirroring the
// event-type dispatch in extractAnthropicMessages: message_start nests it
// under message.usage, message_delta and the non-streaming body carry it at
// the top level.
func (f *anthropicStreamFrame) usageForType() *anthropicUsage {
	if f.Type == "message_start" && f.Message != nil {
		return f.Message.Usage
	}
	return f.Usage
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
