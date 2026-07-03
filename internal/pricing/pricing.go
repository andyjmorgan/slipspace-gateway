// Package pricing turns the charge quantities the gateway extracts from
// provider responses (token buckets, server-tool call counts, service
// tier, inference geo) into per-request USD cost estimates against a
// rate card: the embedded versioned defaults merged with the operator's
// `pricing:` block.
//
// Cost is an estimate at observation time — the raw quantities stay on
// the span and Record, so history is re-priceable. An unmatched model
// yields no cost (never a guessed one); the reporter surfaces that as
// the unmatched counter.
package pricing

import (
	"sort"
	"strings"
	"time"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
)

// Charge-category names, shared by the cost meter's category label, the
// span's slipspace.cost.<category>.usd attributes, and the Record's
// by_category map.
const (
	CategoryInput      = "input"
	CategoryOutput     = "output"
	CategoryCacheRead  = "cache_read"
	CategoryCacheWrite = "cache_write"
	CategoryToolCalls  = "tool_calls"
)

// Input is everything cost computation needs about one completed
// request. Token fields follow the events.Request conventions: In is
// gross (cache-inclusive), the audio/cache fields are sub-buckets.
type Input struct {
	Provider     string
	Model        string
	ServiceTier  string
	InferenceGeo string

	// At selects among effective-dated rate entries; the request start
	// time.
	At time.Time

	In              int
	Out             int
	Cached          int
	CacheCreation   int
	CacheCreation5m int
	CacheCreation1h int
	InputAudio      int
	OutputAudio     int

	ServerToolUse map[string]int
}

// Cost is the computed estimate. ByCategory holds only non-zero
// categories; Total is their sum.
type Cost struct {
	Total      float64
	ByCategory map[string]float64

	// Version identifies the rate card that produced the estimate:
	// the embedded defaults version, suffixed "+overrides" when the
	// operator card contributed entries.
	Version string
}

// Table is the merged, immutable rate card. Built once per config load
// (never per request) and read concurrently by the reporters.
type Table struct {
	entries []entry
	version string
}

// entry is one compiled rate-card row.
type entry struct {
	prefix   string // match with any trailing * removed
	exact    bool   // no trailing * — model must equal prefix
	provider string
	from     time.Time // zero = effective since always
	m        contractsconfig.ModelPricing
}

// New compiles the operator block against the embedded defaults. cfg may
// be nil (costing off) — callers gate on cfg.On() before use, but a nil
// receiver Table is also safe and matches nothing.
func New(cfg *contractsconfig.Pricing) *Table {
	t := &Table{version: DefaultsVersion}
	if cfg.DefaultsOn() {
		t.entries = compile(defaults())
	} else {
		t.version = "custom"
	}
	if cfg != nil && len(cfg.Models) > 0 {
		// Operator entries are compiled after (and sorted above) the
		// defaults at equal specificity, so an operator row always wins
		// a tie against an embedded row with the same pattern.
		t.entries = append(t.entries, compile(cfg.Models)...)
		if cfg.DefaultsOn() {
			t.version += "+overrides"
		}
	}
	// Longest prefix first; operator rows before embedded rows on equal
	// length (stable sort preserves append order within a length class —
	// operator entries were appended last, so reverse the comparison by
	// walking matches in order and preferring later entries on ties).
	sort.SliceStable(t.entries, func(i, j int) bool {
		return len(t.entries[i].prefix) > len(t.entries[j].prefix)
	})
	return t
}

func compile(models []contractsconfig.ModelPricing) []entry {
	out := make([]entry, 0, len(models))
	for _, m := range models {
		e := entry{provider: m.Provider, m: m}
		if p, ok := strings.CutSuffix(m.Match, "*"); ok {
			e.prefix = p
		} else {
			e.prefix = m.Match
			e.exact = true
		}
		if m.EffectiveFrom != "" {
			if ts, err := time.Parse("2006-01-02", m.EffectiveFrom); err == nil {
				e.from = ts
			}
		}
		out = append(out, e)
	}
	return out
}

// Version names the rate card (defaults version, "+overrides" when the
// operator card contributed).
func (t *Table) Version() string {
	if t == nil {
		return ""
	}
	return t.version
}

// match picks the winning rate entry for (provider, model, at): longest
// pattern wins; among candidates with the same pattern the latest
// effective date not after `at` wins, with operator rows beating
// embedded rows on full ties (they sort later and matching keeps the
// last equal candidate).
func (t *Table) match(provider, model string, at time.Time) (contractsconfig.ModelPricing, bool) {
	if t == nil || model == "" {
		return contractsconfig.ModelPricing{}, false
	}
	var (
		found    bool
		best     entry
		bestLen  = -1
		bestFrom time.Time
	)
	for _, e := range t.entries {
		if e.provider != "" && e.provider != provider {
			continue
		}
		if e.exact {
			if model != e.prefix {
				continue
			}
		} else if !strings.HasPrefix(model, e.prefix) {
			continue
		}
		if !e.from.IsZero() && e.from.After(at) {
			continue
		}
		// entries are sorted longest-first, so once a match exists any
		// shorter pattern loses; among equal-length patterns prefer the
		// later effective date, then the later row (operator overrides).
		if found && len(e.prefix) < bestLen {
			break
		}
		if !found || e.from.After(bestFrom) || e.from.Equal(bestFrom) {
			found, best, bestLen, bestFrom = true, e, len(e.prefix), e.from
		}
	}
	return best.m, found
}

// Cost computes the USD estimate for one request. ok is false when no
// rate entry matches the model — the caller reports unpriced rather
// than guessing zero.
func (t *Table) Cost(in Input) (Cost, bool) {
	m, ok := t.match(in.Provider, in.Model, in.At)
	if !ok {
		return Cost{}, false
	}

	rates := m.PerMTok
	if m.LongContext != nil && in.In > m.LongContext.Threshold {
		rates = mergeRates(m.PerMTok, m.LongContext.PerMTok)
	}

	const mtok = 1_000_000.0

	// Sub-buckets are clamped so a provider accounting quirk (e.g. a
	// cached count exceeding gross input) can't yield negative spend.
	uncached := clamp(in.In - in.Cached - in.CacheCreation)
	inAudio := min(in.InputAudio, uncached)
	inText := uncached - inAudio
	outAudio := min(in.OutputAudio, in.Out)
	outText := in.Out - outAudio

	audioIn, audioOut := rates.AudioInput, rates.AudioOutput
	if audioIn == 0 {
		audioIn = rates.Input // no separate audio rate: bills as input
	}
	if audioOut == 0 {
		audioOut = rates.Output
	}

	write5m, write1h := in.CacheCreation5m, in.CacheCreation1h
	if write5m == 0 && write1h == 0 {
		// Flat total only: price at the 5m (default TTL) rate.
		write5m = in.CacheCreation
	}

	by := map[string]float64{
		CategoryInput:      (float64(inText)*rates.Input + float64(inAudio)*audioIn) / mtok,
		CategoryOutput:     (float64(outText)*rates.Output + float64(outAudio)*audioOut) / mtok,
		CategoryCacheRead:  float64(in.Cached) * rates.CacheRead / mtok,
		CategoryCacheWrite: (float64(write5m)*rates.CacheWrite5m + float64(write1h)*rates.CacheWrite1h) / mtok,
	}

	// Tier and geo multipliers scale the token categories only — vendors
	// price tool calls flat regardless of tier.
	mult := multiplier(m.Tiers, in.ServiceTier) * multiplier(m.Geos, in.InferenceGeo)
	for k := range by {
		by[k] *= mult
	}

	var tool float64
	for k, n := range in.ServerToolUse {
		if rate, has := m.ToolCalls[k]; has && n > 0 {
			tool += float64(n) * rate / 1000.0
		}
	}
	if tool > 0 {
		by[CategoryToolCalls] = tool
	}

	c := Cost{ByCategory: make(map[string]float64, len(by)), Version: t.version}
	for k, v := range by {
		if v > 0 {
			c.ByCategory[k] = v
			c.Total += v
		}
	}
	return c, true
}

// mergeRates overlays lc on base: categories lc leaves zero keep the
// base rate (vendors typically reprice only input/output/cache_read in
// their long-context tier).
func mergeRates(base, lc contractsconfig.Rates) contractsconfig.Rates {
	pick := func(over, def float64) float64 {
		if over > 0 {
			return over
		}
		return def
	}
	return contractsconfig.Rates{
		Input:        pick(lc.Input, base.Input),
		Output:       pick(lc.Output, base.Output),
		CacheRead:    pick(lc.CacheRead, base.CacheRead),
		CacheWrite5m: pick(lc.CacheWrite5m, base.CacheWrite5m),
		CacheWrite1h: pick(lc.CacheWrite1h, base.CacheWrite1h),
		AudioInput:   pick(lc.AudioInput, base.AudioInput),
		AudioOutput:  pick(lc.AudioOutput, base.AudioOutput),
	}
}

func multiplier(m map[string]float64, key string) float64 {
	if key == "" || m == nil {
		return 1
	}
	if v, ok := m[key]; ok {
		return v
	}
	return 1
}

func clamp(v int) int {
	if v < 0 {
		return 0
	}
	return v
}
