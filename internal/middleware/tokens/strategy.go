package tokens

// Strategy is how an Aggregator combines a Usage observation with prior
// state. Per-provider extractors tag each emission with a Strategy so the
// aggregator does not need to know the upstream's wire convention; new
// vendors plug in by extending only the extractor.
type Strategy int

const (
	// StrategyLastWins overwrites the snapshot with the new Usage. The
	// right choice for providers whose terminal emission carries the
	// authoritative final total: OpenAI (usage on the terminal stream
	// chunk + every non-streaming body), Anthropic (message_delta
	// cumulative), Gemini (usageMetadata on the response, or per-chunk
	// running totals where last value wins).
	StrategyLastWins Strategy = iota

	// StrategyAddition sums the Usage into the snapshot. Reserved for
	// vendors that emit per-chunk deltas — Perplexity is the canonical
	// case. Unused by every v1.0 extractor.
	StrategyAddition

	// StrategyIncremental treats the first emission as the initial state
	// (input + cache + initial output) and subsequent emissions as
	// output-only updates. Matches airia-platform's historical
	// "Incremental" pattern for vendors that quote input once up front
	// and only emit growing output thereafter. Unused by every v1.0
	// extractor — kept for forward compatibility.
	StrategyIncremental
)

// Usage is one observation of upstream token accounting. Zero fields are
// "not reported" and are ignored by every strategy. The buckets align
// with the contracts/events/Request.Tokens* fields so the reporter can
// copy straight through without a translation step.
type Usage struct {
	// Input is the gross prompt-token total billed for the request.
	// Always includes any tokens served from cache (Cached is a sub-
	// bucket, not a separate deduction) — matches OpenAI's
	// prompt_tokens, Anthropic's input_tokens, Gemini's
	// promptTokenCount.
	Input int

	// Output is the count of tokens the model generated. For providers
	// that bill reasoning tokens separately (OpenAI reasoning models,
	// Gemini thoughts), they are folded in here — what the customer
	// pays for, not just user-visible characters.
	Output int

	// Cached is the share of Input the provider served from its prompt
	// cache and billed at the cached-read price. Informational —
	// already part of Input, do not add it again.
	Cached int

	// CacheCreation is the share of Input billed at the cache-write
	// premium. Anthropic-only on today's surface (their cache writes
	// cost more than uncached input); OpenAI and Gemini do implicit
	// caching with no separate write charge, so extractors there leave
	// this zero.
	CacheCreation int

	// CacheCreation5m / CacheCreation1h split CacheCreation by cache
	// TTL (Anthropic usage.cache_creation.ephemeral_5m_input_tokens /
	// ephemeral_1h_input_tokens). The tiers carry different write
	// premiums (1.25× vs 2× base input), so costing needs the split;
	// older responses report only the flat total, in which case both
	// stay zero and CacheCreation alone is authoritative. When
	// present, the tiers sum to CacheCreation — they are a breakdown,
	// not an addition.
	CacheCreation5m int
	CacheCreation1h int

	// InputAudio / OutputAudio are the audio-modality shares of Input /
	// Output, billed at audio rates where the provider prices audio
	// separately (OpenAI prompt/completion_tokens_details.audio_tokens,
	// Gemini per-modality *TokensDetails). Sub-buckets — already
	// counted in Input/Output.
	InputAudio  int
	OutputAudio int
}

// IsZero reports whether u carries no observation. Strategies skip
// zero-valued observations so an extractor can call Handle defensively
// without first checking each field.
func (u Usage) IsZero() bool {
	return u == Usage{}
}

// Snapshot is the aggregator's current accumulated view. Recognised is
// true iff at least one non-zero Usage was Handle'd — separates "no
// usage data was reported by the provider" from "the provider reported
// zero tokens" (the latter is virtually impossible but the type system
// shouldn't conflate them).
type Snapshot struct {
	Input           int
	Output          int
	Cached          int
	CacheCreation   int
	CacheCreation5m int
	CacheCreation1h int
	InputAudio      int
	OutputAudio     int
	Recognised      bool
}
