package pricing

import (
	"math"
	"testing"
	"time"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
)

func mustCost(t *testing.T, tbl *Table, in Input) Cost {
	t.Helper()
	c, ok := tbl.Cost(in)
	if !ok {
		t.Fatalf("Cost(%+v): no match", in)
	}
	return c
}

func approx(t *testing.T, got, want float64, what string) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

var july = time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

// TestCost_DefaultsSimple prices a plain request against the embedded
// card: gpt-5.4 at $2.50/M in, $15/M out.
func TestCost_DefaultsSimple(t *testing.T) {
	t.Parallel()
	tbl := New(&contractsconfig.Pricing{})
	c := mustCost(t, tbl, Input{Provider: "openai", Model: "gpt-5.4", At: july, In: 1_000_000, Out: 100_000})
	approx(t, c.ByCategory[CategoryInput], 2.5, "input")
	approx(t, c.ByCategory[CategoryOutput], 1.5, "output")
	approx(t, c.Total, 4.0, "total")
	if c.Version != DefaultsVersion {
		t.Errorf("Version = %q, want %q", c.Version, DefaultsVersion)
	}
}

// TestCost_LongestPrefixWins proves gpt-5.4-mini matches its own entry,
// not the shorter gpt-5.4* one.
func TestCost_LongestPrefixWins(t *testing.T) {
	t.Parallel()
	tbl := New(&contractsconfig.Pricing{})
	c := mustCost(t, tbl, Input{Model: "gpt-5.4-mini-2026-01", At: july, In: 1_000_000})
	approx(t, c.ByCategory[CategoryInput], 0.75, "input (mini rate)")
}

// TestCost_AnthropicCacheSplit prices the full Anthropic cache surface:
// uncached input, cache read, and the 5m/1h write split at their own
// rates (fable-5: in 10, read 1, w5m 12.5, w1h 20).
func TestCost_AnthropicCacheSplit(t *testing.T) {
	t.Parallel()
	tbl := New(&contractsconfig.Pricing{})
	c := mustCost(t, tbl, Input{
		Provider: "anthropic", Model: "claude-fable-5", At: july,
		In: 2_000_000, Cached: 1_000_000, CacheCreation: 500_000,
		CacheCreation5m: 200_000, CacheCreation1h: 300_000,
		Out: 100_000,
	})
	approx(t, c.ByCategory[CategoryInput], 5.0, "input (500k uncached @ 10)")
	approx(t, c.ByCategory[CategoryCacheRead], 1.0, "cache_read (1M @ 1)")
	approx(t, c.ByCategory[CategoryCacheWrite], 0.2*12.5+0.3*20, "cache_write (5m/1h split)")
	approx(t, c.ByCategory[CategoryOutput], 5.0, "output (100k @ 50)")
}

// TestCost_FlatCacheCreationPricedAt5m: a response reporting only the
// flat cache-creation total bills entirely at the 5m (default TTL) rate.
func TestCost_FlatCacheCreationPricedAt5m(t *testing.T) {
	t.Parallel()
	tbl := New(&contractsconfig.Pricing{})
	c := mustCost(t, tbl, Input{
		Model: "claude-haiku-4-5", At: july,
		In: 400_000, CacheCreation: 400_000,
	})
	approx(t, c.ByCategory[CategoryCacheWrite], 0.4*1.25, "cache_write flat @ 5m rate")
	if _, has := c.ByCategory[CategoryInput]; has {
		t.Errorf("input category present, want absent (all input was cache write)")
	}
}

// TestCost_TierAndGeoMultipliers: Anthropic batch (0.5×) and us geo
// (1.1×) compose on token categories; tool calls stay flat.
func TestCost_TierAndGeoMultipliers(t *testing.T) {
	t.Parallel()
	tbl := New(&contractsconfig.Pricing{})
	c := mustCost(t, tbl, Input{
		Model: "claude-opus-4-8", At: july,
		In: 1_000_000, Out: 0,
		ServiceTier: "batch", InferenceGeo: "us",
		ServerToolUse: map[string]int{"web_search_requests": 2000},
	})
	approx(t, c.ByCategory[CategoryInput], 5.0*0.5*1.1, "input with batch+us multipliers")
	approx(t, c.ByCategory[CategoryToolCalls], 20.0, "tool_calls (2000 @ $10/1k, unmultiplied)")
}

// TestCost_UnknownTierMultipliesByOne: standard/default/auto and other
// unlisted tiers do not change the price.
func TestCost_UnknownTierMultipliesByOne(t *testing.T) {
	t.Parallel()
	tbl := New(&contractsconfig.Pricing{})
	c := mustCost(t, tbl, Input{Model: "gpt-5.4", At: july, In: 1_000_000, ServiceTier: "default"})
	approx(t, c.ByCategory[CategoryInput], 2.5, "input at standard tier")
}

// TestCost_LongContextTier: gemini-3.1-pro above 200k input switches to
// the long-context rates for input, output, and cache read.
func TestCost_LongContextTier(t *testing.T) {
	t.Parallel()
	tbl := New(&contractsconfig.Pricing{})
	c := mustCost(t, tbl, Input{
		Model: "gemini-3.1-pro-preview", At: july,
		In: 400_000, Cached: 100_000, Out: 50_000,
	})
	approx(t, c.ByCategory[CategoryInput], 0.3*4.0, "input @ LC rate")
	approx(t, c.ByCategory[CategoryCacheRead], 0.1*0.40, "cache read @ LC rate")
	approx(t, c.ByCategory[CategoryOutput], 0.05*18, "output @ LC rate")

	small := mustCost(t, tbl, Input{Model: "gemini-3.1-pro-preview", At: july, In: 100_000})
	approx(t, small.ByCategory[CategoryInput], 0.1*2.0, "input below threshold @ base rate")
}

// TestCost_AudioRates: audio token shares bill at the audio rates, the
// text remainder at base rates (gemini-2.5-flash audio in $1 vs $0.30).
func TestCost_AudioRates(t *testing.T) {
	t.Parallel()
	tbl := New(&contractsconfig.Pricing{})
	c := mustCost(t, tbl, Input{
		Model: "gemini-2.5-flash", At: july,
		In: 1_000_000, InputAudio: 600_000,
	})
	approx(t, c.ByCategory[CategoryInput], 0.4*0.30+0.6*1.0, "input text+audio split")
}

// TestCost_AudioWithoutRateBillsAsText: a model with no audio rate folds
// audio tokens into the plain input price.
func TestCost_AudioWithoutRateBillsAsText(t *testing.T) {
	t.Parallel()
	tbl := New(&contractsconfig.Pricing{})
	c := mustCost(t, tbl, Input{Model: "gpt-5.4", At: july, In: 1_000_000, InputAudio: 500_000})
	approx(t, c.ByCategory[CategoryInput], 2.5, "input, audio at base rate")
}

// TestCost_EffectiveDatedEntry: Sonnet 5 intro pricing applies before
// 2026-09-01, the dated entry after.
func TestCost_EffectiveDatedEntry(t *testing.T) {
	t.Parallel()
	tbl := New(&contractsconfig.Pricing{})
	intro := mustCost(t, tbl, Input{Model: "claude-sonnet-5", At: july, In: 1_000_000})
	approx(t, intro.ByCategory[CategoryInput], 2.0, "intro price")

	oct := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	full := mustCost(t, tbl, Input{Model: "claude-sonnet-5", At: oct, In: 1_000_000})
	approx(t, full.ByCategory[CategoryInput], 3.0, "post-intro price")
}

// TestCost_OperatorOverrideWins: an operator entry with the same pattern
// as an embedded one takes precedence; a zero-rate card prices to
// nothing but still matches.
func TestCost_OperatorOverrideWins(t *testing.T) {
	t.Parallel()
	tbl := New(&contractsconfig.Pricing{Models: []contractsconfig.ModelPricing{
		{Match: "gpt-5.4*", PerMTok: contractsconfig.Rates{Input: 1, Output: 2}},
		{Match: "qwen*"}, // self-hosted: zero-rate, matched not unpriced
	}})
	c := mustCost(t, tbl, Input{Model: "gpt-5.4", At: july, In: 1_000_000})
	approx(t, c.ByCategory[CategoryInput], 1.0, "operator override rate")
	if c.Version != DefaultsVersion+"+overrides" {
		t.Errorf("Version = %q, want %q", c.Version, DefaultsVersion+"+overrides")
	}

	q := mustCost(t, tbl, Input{Model: "qwen3-coder", At: july, In: 1_000_000, Out: 50})
	approx(t, q.Total, 0, "zero-rate total")
}

// TestCost_ProviderScopedEntry only applies to the named provider.
func TestCost_ProviderScopedEntry(t *testing.T) {
	t.Parallel()
	tbl := New(&contractsconfig.Pricing{Models: []contractsconfig.ModelPricing{
		{Match: "llama*", Provider: "groq", PerMTok: contractsconfig.Rates{Input: 0.1}},
	}})
	if _, ok := tbl.Cost(Input{Provider: "ollama", Model: "llama-4", At: july, In: 10}); ok {
		t.Errorf("provider-scoped entry matched the wrong provider")
	}
	c := mustCost(t, tbl, Input{Provider: "groq", Model: "llama-4", At: july, In: 1_000_000})
	approx(t, c.ByCategory[CategoryInput], 0.1, "provider-scoped rate")
}

// TestCost_UnmatchedModel yields ok=false — unpriced, never guessed.
func TestCost_UnmatchedModel(t *testing.T) {
	t.Parallel()
	tbl := New(&contractsconfig.Pricing{})
	if _, ok := tbl.Cost(Input{Model: "nomatch-internal", At: july, In: 10}); ok {
		t.Errorf("unmatched model returned a cost")
	}
}

// TestCost_NoDefaults: use_defaults: false hides the embedded card.
func TestCost_NoDefaults(t *testing.T) {
	t.Parallel()
	f := false
	tbl := New(&contractsconfig.Pricing{UseDefaults: &f, Models: []contractsconfig.ModelPricing{
		{Match: "gpt-5.4*", PerMTok: contractsconfig.Rates{Input: 1}},
	}})
	if _, ok := tbl.Cost(Input{Model: "claude-fable-5", At: july, In: 10}); ok {
		t.Errorf("embedded entry matched with use_defaults=false")
	}
	if tbl.Version() != "custom" {
		t.Errorf("Version = %q, want custom", tbl.Version())
	}
}

// TestCost_ClampsInconsistentCounts: cached exceeding gross input can't
// produce negative input spend.
func TestCost_ClampsInconsistentCounts(t *testing.T) {
	t.Parallel()
	tbl := New(&contractsconfig.Pricing{})
	c := mustCost(t, tbl, Input{Model: "gpt-5.4", At: july, In: 100, Cached: 500})
	if v := c.ByCategory[CategoryInput]; v < 0 {
		t.Errorf("input = %v, want >= 0", v)
	}
}

// TestDefaults_Validate keeps the embedded card structurally legal under
// the same validation operator YAML gets.
func TestDefaults_Validate(t *testing.T) {
	t.Parallel()
	p := &contractsconfig.Pricing{Models: defaults()}
	if err := p.Validate(); err != nil {
		t.Fatalf("embedded defaults fail validation: %v", err)
	}
}

// TestTable_NilAndEdgeBranches covers the nil-receiver and edge paths:
// nil table, empty model, exact-match entries, and invalid
// effective_from strings surviving compile (validation rejects them
// upstream; compile just leaves the entry undated).
func TestTable_NilAndEdgeBranches(t *testing.T) {
	t.Parallel()
	var nilTable *Table
	if v := nilTable.Version(); v != "" {
		t.Errorf("nil Version = %q, want empty", v)
	}
	if _, ok := nilTable.Cost(Input{Model: "gpt-5.4", At: july, In: 10}); ok {
		t.Errorf("nil table matched")
	}

	tbl := New(&contractsconfig.Pricing{Models: []contractsconfig.ModelPricing{
		{Match: "exact-model", PerMTok: contractsconfig.Rates{Input: 7}},
		{Match: "dated*", EffectiveFrom: "not-a-date", PerMTok: contractsconfig.Rates{Input: 9}},
	}})
	if _, ok := tbl.Cost(Input{Model: "", At: july, In: 10}); ok {
		t.Errorf("empty model matched")
	}
	if _, ok := tbl.Cost(Input{Model: "exact-model-suffixed", At: july, In: 10}); ok {
		t.Errorf("exact entry matched a prefixed model")
	}
	c := mustCost(t, tbl, Input{Model: "exact-model", At: july, In: 1_000_000})
	approx(t, c.ByCategory[CategoryInput], 7, "exact entry rate")
	d := mustCost(t, tbl, Input{Model: "dated-x", At: july, In: 1_000_000})
	approx(t, d.ByCategory[CategoryInput], 9, "undated (invalid date) entry applies")
}

// TestCost_OutputAudioAndLCWriteFallback covers the remaining rate
// branches: output-audio pricing and long-context entries falling back
// to base cache-write/audio rates they leave zero.
func TestCost_OutputAudioAndLCWriteFallback(t *testing.T) {
	t.Parallel()
	tbl := New(&contractsconfig.Pricing{Models: []contractsconfig.ModelPricing{{
		Match:       "av*",
		PerMTok:     contractsconfig.Rates{Input: 1, Output: 2, CacheWrite5m: 5, CacheWrite1h: 8, AudioOutput: 20},
		LongContext: &contractsconfig.LongContextPricing{Threshold: 100, PerMTok: contractsconfig.Rates{Input: 3}},
	}}})
	c := mustCost(t, tbl, Input{
		Model: "av-1", At: july,
		In: 1_000_000, Out: 1_000_000, OutputAudio: 250_000,
		CacheCreation: 0, CacheCreation5m: 0, CacheCreation1h: 0,
	})
	// Above threshold: input at LC rate 3; output keeps base 2 with the
	// audio share at 20.
	approx(t, c.ByCategory[CategoryInput], 3, "LC input")
	approx(t, c.ByCategory[CategoryOutput], 0.75*2+0.25*20, "output text+audio")

	w := mustCost(t, tbl, Input{Model: "av-1", At: july, In: 1_000_000, CacheCreation5m: 200_000, CacheCreation1h: 100_000})
	approx(t, w.ByCategory[CategoryCacheWrite], 0.2*5+0.1*8, "LC write fallback to base rates")
}
