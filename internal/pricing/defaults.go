package pricing

import contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"

// DefaultsVersion names the embedded rate card. Bump whenever the table
// below changes; it rides every cost estimate (Record cost.table_version)
// so an auditor can tell which card priced a request.
const DefaultsVersion = "2026-07"

// defaults returns the embedded rate card: the current frontier models
// of the three supported vendors, transcribed from the official pricing
// pages as fetched 2026-07-03 (see the Token Costing design note for the
// sourced snapshot). USD per million tokens; tool calls USD per 1k.
//
// Deliberate omissions, priced $0 rather than guessed:
//   - OpenAI long-context tiers (the page shows the columns but not the
//     token threshold — unverifiable, so base rates apply throughout).
//   - Gemini 2.5 search grounding ($35/1k grounded *prompts* — our
//     counter counts *queries*, a different unit, so no rate is safe).
//   - Per-hour charges invisible to a response (Gemini cache storage,
//     OpenAI container sessions).
//
// Operator `pricing.models` entries override any of this by matching the
// same pattern (or a longer one).
func defaults() []contractsconfig.ModelPricing {
	openaiTiers := map[string]float64{"batch": 0.5, "flex": 0.5, "priority": 2.0}
	anthTiers := map[string]float64{"batch": 0.5}
	anthGeos := map[string]float64{"us": 1.1}
	gemTiers := map[string]float64{"batch": 0.5, "priority": 1.8}

	return []contractsconfig.ModelPricing{
		// ---- OpenAI (developers.openai.com/api/docs/pricing) ----
		{Match: "gpt-5.5-pro*", PerMTok: contractsconfig.Rates{Input: 30, Output: 180},
			Tiers: map[string]float64{"batch": 0.5, "flex": 0.5}},
		{Match: "gpt-5.5*", PerMTok: contractsconfig.Rates{Input: 5, Output: 30, CacheRead: 0.50},
			Tiers:     map[string]float64{"batch": 0.5, "flex": 0.5, "priority": 2.5},
			ToolCalls: map[string]float64{"web_search_call": 10}},
		{Match: "gpt-5.4-mini*", PerMTok: contractsconfig.Rates{Input: 0.75, Output: 4.5, CacheRead: 0.075},
			Tiers: openaiTiers, ToolCalls: map[string]float64{"web_search_call": 10}},
		{Match: "gpt-5.4-nano*", PerMTok: contractsconfig.Rates{Input: 0.20, Output: 1.25, CacheRead: 0.02},
			Tiers: map[string]float64{"batch": 0.5, "flex": 0.5}},
		{Match: "gpt-5.4*", PerMTok: contractsconfig.Rates{Input: 2.5, Output: 15, CacheRead: 0.25},
			Tiers: openaiTiers, ToolCalls: map[string]float64{"web_search_call": 10}},
		{Match: "gpt-5.3-codex*", PerMTok: contractsconfig.Rates{Input: 1.75, Output: 14, CacheRead: 0.175},
			Tiers: openaiTiers},
		{Match: "gpt-realtime-2*", PerMTok: contractsconfig.Rates{Input: 4, Output: 24, CacheRead: 0.40,
			AudioInput: 32, AudioOutput: 64}},

		// ---- Anthropic (platform.claude.com/docs pricing; cache write
		// 5m = 1.25×in, 1h = 2×in, read = 0.1×in) ----
		{Match: "claude-fable-5*", PerMTok: contractsconfig.Rates{Input: 10, Output: 50, CacheRead: 1, CacheWrite5m: 12.5, CacheWrite1h: 20},
			Tiers: anthTiers, Geos: anthGeos, ToolCalls: map[string]float64{"web_search_requests": 10}},
		{Match: "claude-mythos-5*", PerMTok: contractsconfig.Rates{Input: 10, Output: 50, CacheRead: 1, CacheWrite5m: 12.5, CacheWrite1h: 20},
			Tiers: anthTiers, Geos: anthGeos, ToolCalls: map[string]float64{"web_search_requests": 10}},
		{Match: "claude-opus-4*", PerMTok: contractsconfig.Rates{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite5m: 6.25, CacheWrite1h: 10},
			Tiers: anthTiers, Geos: anthGeos, ToolCalls: map[string]float64{"web_search_requests": 10}},
		// Sonnet 5 launched on intro pricing that expires 2026-08-31;
		// the dated sibling entry takes over on 2026-09-01.
		{Match: "claude-sonnet-5*", PerMTok: contractsconfig.Rates{Input: 2, Output: 10, CacheRead: 0.2, CacheWrite5m: 2.5, CacheWrite1h: 4},
			Tiers: anthTiers, Geos: anthGeos, ToolCalls: map[string]float64{"web_search_requests": 10}},
		{Match: "claude-sonnet-5*", EffectiveFrom: "2026-09-01",
			PerMTok: contractsconfig.Rates{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite5m: 3.75, CacheWrite1h: 6},
			Tiers:   anthTiers, Geos: anthGeos, ToolCalls: map[string]float64{"web_search_requests": 10}},
		{Match: "claude-sonnet-4*", PerMTok: contractsconfig.Rates{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite5m: 3.75, CacheWrite1h: 6},
			Tiers: anthTiers, Geos: anthGeos, ToolCalls: map[string]float64{"web_search_requests": 10}},
		{Match: "claude-haiku-4*", PerMTok: contractsconfig.Rates{Input: 1, Output: 5, CacheRead: 0.1, CacheWrite5m: 1.25, CacheWrite1h: 2},
			Tiers: anthTiers, Geos: anthGeos, ToolCalls: map[string]float64{"web_search_requests": 10}},

		// ---- Google Gemini (ai.google.dev/gemini-api/docs/pricing,
		// paid tier; output rates include thinking tokens) ----
		{Match: "gemini-3.5-flash*", PerMTok: contractsconfig.Rates{Input: 1.5, Output: 9, CacheRead: 0.15},
			Tiers: gemTiers, ToolCalls: map[string]float64{"web_search_queries": 14}},
		{Match: "gemini-3.1-pro*", PerMTok: contractsconfig.Rates{Input: 2, Output: 12, CacheRead: 0.20},
			LongContext: &contractsconfig.LongContextPricing{Threshold: 200_000,
				PerMTok: contractsconfig.Rates{Input: 4, Output: 18, CacheRead: 0.40}},
			Tiers: gemTiers, ToolCalls: map[string]float64{"web_search_queries": 14}},
		{Match: "gemini-3.1-flash-lite*", PerMTok: contractsconfig.Rates{Input: 0.25, Output: 1.5, CacheRead: 0.025, AudioInput: 0.5},
			Tiers: gemTiers, ToolCalls: map[string]float64{"web_search_queries": 14}},
		{Match: "gemini-3-flash*", PerMTok: contractsconfig.Rates{Input: 0.5, Output: 3, CacheRead: 0.05, AudioInput: 1},
			Tiers: gemTiers, ToolCalls: map[string]float64{"web_search_queries": 14}},
		{Match: "gemini-2.5-pro*", PerMTok: contractsconfig.Rates{Input: 1.25, Output: 10, CacheRead: 0.125},
			LongContext: &contractsconfig.LongContextPricing{Threshold: 200_000,
				PerMTok: contractsconfig.Rates{Input: 2.5, Output: 15, CacheRead: 0.25}},
			Tiers: map[string]float64{"batch": 0.5}},
		{Match: "gemini-2.5-flash*", PerMTok: contractsconfig.Rates{Input: 0.30, Output: 2.5, CacheRead: 0.03, AudioInput: 1},
			Tiers: map[string]float64{"batch": 0.5}},
	}
}
