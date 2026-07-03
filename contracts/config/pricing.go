package config

import (
	"fmt"
	"time"
)

// Pricing is the `pricing:` top-level block. It turns the charge
// quantities the gateway already extracts (token buckets, server-tool
// call counts, service tier, inference geo) into per-request USD cost
// estimates, emitted on the cost meter, the gen_ai span/event, and the
// connector Record.
//
// The gateway ships an embedded, versioned default rate card for the
// current frontier models (internal/pricing/defaults.go); Models entries
// override or extend it under the same matching rules. Absence of the
// block — or Enabled: false — disables costing entirely: no cost
// attributes, no cost meter, no unmatched counter.
type Pricing struct {
	// Enabled turns costing on. Nil (key omitted) means enabled — the
	// block's presence is the opt-in; the explicit key exists so an
	// operator can park a rate card without deleting it.
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`

	// UseDefaults controls whether the embedded default rate card is
	// consulted for models no Models entry matches. Nil means true.
	// Operators who want fully explicit pricing set it false — unmatched
	// models are then reported unpriced rather than silently defaulted.
	UseDefaults *bool `yaml:"use_defaults,omitempty" json:"use_defaults,omitempty"`

	// Models is the operator rate card: overrides and additions matched
	// against the request's post-rule model id. A Models entry that
	// matches always wins over the embedded defaults.
	Models []ModelPricing `yaml:"models,omitempty" json:"models,omitempty"`
}

// ModelPricing is one rate-card entry. Matching: Match is either an
// exact model id or a prefix ending in `*`; the longest matching pattern
// wins across the merged (operator + embedded) card, and an optional
// Provider restricts the entry to requests routed to that provider name.
// Multiple entries may share a Match with different EffectiveFrom dates —
// the entry with the latest effective date not after the request time is
// chosen (an undated entry is effective since always).
type ModelPricing struct {
	// Match is the model pattern: exact id, or prefix + trailing `*`
	// (e.g. "claude-sonnet-5*"). Required.
	Match string `yaml:"match" json:"match"`

	// Provider optionally restricts the entry to one provider name (the
	// operator's providers.yaml key). Empty matches any provider —
	// model-id vocabularies are vendor-unique in practice.
	Provider string `yaml:"provider,omitempty" json:"provider,omitempty"`

	// EffectiveFrom is an ISO date (YYYY-MM-DD, UTC midnight) this entry
	// starts applying. Empty = effective since always. Lets a card carry
	// an announced price change (e.g. an intro price expiring) without a
	// same-day config edit.
	EffectiveFrom string `yaml:"effective_from,omitempty" json:"effective_from,omitempty"`

	// PerMTok is the USD price per million tokens for each charge
	// category. A category left zero is free — rates are explicit, never
	// derived (the embedded defaults spell out every category).
	PerMTok Rates `yaml:"per_mtok" json:"per_mtok"`

	// LongContext, when set, switches PerMTok-equivalent rates in when
	// the request's gross input exceeds Threshold tokens (Gemini >200k
	// tiers, OpenAI long-context pricing).
	LongContext *LongContextPricing `yaml:"long_context,omitempty" json:"long_context,omitempty"`

	// Tiers maps a provider-reported service_tier value to a whole-
	// request multiplier applied to the token categories (batch 0.5,
	// priority 1.8–2.5, ...). Unlisted tiers — including the common
	// standard/default/auto — multiply by 1.
	Tiers map[string]float64 `yaml:"tiers,omitempty" json:"tiers,omitempty"`

	// Geos maps a provider-reported inference_geo value to a multiplier
	// applied to the token categories (Anthropic "us" = 1.1). Unlisted
	// geos multiply by 1.
	Geos map[string]float64 `yaml:"geos,omitempty" json:"geos,omitempty"`

	// ToolCalls maps a server-tool counter key — the provider's own wire
	// vocabulary as carried in the Record's server_tool_use map
	// (web_search_requests, web_search_call, web_search_queries, ...) —
	// to a USD price per 1,000 calls. Counters with no listed rate cost
	// nothing. Tier/geo multipliers do not apply to tool calls.
	ToolCalls map[string]float64 `yaml:"tool_calls,omitempty" json:"tool_calls,omitempty"`
}

// Rates is the per-million-token USD price for each charge category.
type Rates struct {
	// Input prices uncached, non-audio input tokens.
	Input float64 `yaml:"input,omitempty" json:"input,omitempty"`

	// Output prices non-audio output tokens (reasoning/thinking tokens
	// bill as plain output on every current vendor).
	Output float64 `yaml:"output,omitempty" json:"output,omitempty"`

	// CacheRead prices input tokens served from the provider cache.
	CacheRead float64 `yaml:"cache_read,omitempty" json:"cache_read,omitempty"`

	// CacheWrite5m / CacheWrite1h price cache-write input tokens by TTL
	// tier. A response reporting only the flat cache-creation total is
	// priced entirely at the 5m rate (the provider default TTL).
	CacheWrite5m float64 `yaml:"cache_write_5m,omitempty" json:"cache_write_5m,omitempty"`
	CacheWrite1h float64 `yaml:"cache_write_1h,omitempty" json:"cache_write_1h,omitempty"`

	// AudioInput / AudioOutput price the audio-modality token shares.
	// Zero means the model has no separate audio rate and audio tokens
	// bill at the plain Input/Output rates.
	AudioInput  float64 `yaml:"audio_input,omitempty" json:"audio_input,omitempty"`
	AudioOutput float64 `yaml:"audio_output,omitempty" json:"audio_output,omitempty"`
}

// LongContextPricing switches alternate rates in above an input-token
// threshold.
type LongContextPricing struct {
	// Threshold is the gross input-token count (cache-inclusive) above
	// which the long-context rates apply. Required, > 0.
	Threshold int `yaml:"threshold" json:"threshold"`

	// PerMTok is the replacement rate card above the threshold.
	// Categories left zero fall back to the entry's base PerMTok rate —
	// vendors typically reprice only input/output/cache_read.
	PerMTok Rates `yaml:"per_mtok" json:"per_mtok"`
}

// On reports whether costing is enabled: block present and Enabled not
// explicitly false.
func (p *Pricing) On() bool {
	return p != nil && (p.Enabled == nil || *p.Enabled)
}

// DefaultsOn reports whether the embedded default rate card backs the
// operator entries.
func (p *Pricing) DefaultsOn() bool {
	return p != nil && (p.UseDefaults == nil || *p.UseDefaults)
}

// Validate checks the pricing block. Errors carry the entry index and
// field so an operator can find the offending YAML line.
func (p *Pricing) Validate() error {
	if p == nil {
		return nil
	}
	for i := range p.Models {
		m := &p.Models[i]
		if m.Match == "" {
			return fmt.Errorf("pricing: models[%d]: match is required", i)
		}
		if m.EffectiveFrom != "" {
			if _, err := time.Parse("2006-01-02", m.EffectiveFrom); err != nil {
				return fmt.Errorf("pricing: models[%d] (%s): effective_from %q is not YYYY-MM-DD: %w", i, m.Match, m.EffectiveFrom, err)
			}
		}
		if err := m.PerMTok.validate(fmt.Sprintf("models[%d] (%s) per_mtok", i, m.Match)); err != nil {
			return err
		}
		if m.LongContext != nil {
			if m.LongContext.Threshold <= 0 {
				return fmt.Errorf("pricing: models[%d] (%s): long_context.threshold must be > 0", i, m.Match)
			}
			if err := m.LongContext.PerMTok.validate(fmt.Sprintf("models[%d] (%s) long_context.per_mtok", i, m.Match)); err != nil {
				return err
			}
		}
		for tier, mult := range m.Tiers {
			if mult < 0 {
				return fmt.Errorf("pricing: models[%d] (%s): tiers[%s] must be >= 0", i, m.Match, tier)
			}
		}
		for geo, mult := range m.Geos {
			if mult < 0 {
				return fmt.Errorf("pricing: models[%d] (%s): geos[%s] must be >= 0", i, m.Match, geo)
			}
		}
		for k, rate := range m.ToolCalls {
			if rate < 0 {
				return fmt.Errorf("pricing: models[%d] (%s): tool_calls[%s] must be >= 0", i, m.Match, k)
			}
		}
	}
	return nil
}

func (r *Rates) validate(where string) error {
	for _, f := range []struct {
		name string
		v    float64
	}{
		{"input", r.Input}, {"output", r.Output}, {"cache_read", r.CacheRead},
		{"cache_write_5m", r.CacheWrite5m}, {"cache_write_1h", r.CacheWrite1h},
		{"audio_input", r.AudioInput}, {"audio_output", r.AudioOutput},
	} {
		if f.v < 0 {
			return fmt.Errorf("pricing: %s.%s must be >= 0", where, f.name)
		}
	}
	return nil
}
