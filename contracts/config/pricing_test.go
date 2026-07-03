package config

import (
	"strings"
	"testing"
)

func validEntry() ModelPricing {
	return ModelPricing{Match: "m*", PerMTok: Rates{Input: 1, Output: 2}}
}

// TestPricing_Validate_OK covers the accepting paths: nil block, empty
// block, and a fully-populated entry.
func TestPricing_Validate_OK(t *testing.T) {
	t.Parallel()
	var nilBlock *Pricing
	if err := nilBlock.Validate(); err != nil {
		t.Errorf("nil block: %v", err)
	}
	full := &Pricing{Models: []ModelPricing{{
		Match:         "claude-*",
		Provider:      "anthropic",
		EffectiveFrom: "2026-09-01",
		PerMTok:       Rates{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite5m: 3.75, CacheWrite1h: 6, AudioInput: 1, AudioOutput: 2},
		LongContext:   &LongContextPricing{Threshold: 200_000, PerMTok: Rates{Input: 6}},
		Tiers:         map[string]float64{"batch": 0.5},
		Geos:          map[string]float64{"us": 1.1},
		ToolCalls:     map[string]float64{"web_search_requests": 10},
	}}}
	if err := full.Validate(); err != nil {
		t.Errorf("full entry: %v", err)
	}
}

// TestPricing_Validate_Rejects walks every rejection branch with the
// offending field named in the error.
func TestPricing_Validate_Rejects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		mutate  func(*ModelPricing)
		wantSub string
	}{
		{"empty match", func(m *ModelPricing) { m.Match = "" }, "match is required"},
		{"bad date", func(m *ModelPricing) { m.EffectiveFrom = "September 1" }, "not YYYY-MM-DD"},
		{"negative rate", func(m *ModelPricing) { m.PerMTok.Output = -1 }, "per_mtok.output must be >= 0"},
		{"negative audio", func(m *ModelPricing) { m.PerMTok.AudioInput = -0.5 }, "audio_input must be >= 0"},
		{"zero threshold", func(m *ModelPricing) {
			m.LongContext = &LongContextPricing{Threshold: 0}
		}, "long_context.threshold must be > 0"},
		{"negative LC rate", func(m *ModelPricing) {
			m.LongContext = &LongContextPricing{Threshold: 1, PerMTok: Rates{CacheRead: -1}}
		}, "long_context.per_mtok.cache_read must be >= 0"},
		{"negative tier", func(m *ModelPricing) { m.Tiers = map[string]float64{"batch": -0.5} }, "tiers[batch] must be >= 0"},
		{"negative geo", func(m *ModelPricing) { m.Geos = map[string]float64{"us": -1} }, "geos[us] must be >= 0"},
		{"negative tool rate", func(m *ModelPricing) { m.ToolCalls = map[string]float64{"x": -1} }, "tool_calls[x] must be >= 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := validEntry()
			tc.mutate(&m)
			err := (&Pricing{Models: []ModelPricing{m}}).Validate()
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("err = %v, want containing %q", err, tc.wantSub)
			}
		})
	}
}

// TestPricing_OnAndDefaultsOn covers the tri-state gate helpers.
func TestPricing_OnAndDefaultsOn(t *testing.T) {
	t.Parallel()
	var nilBlock *Pricing
	if nilBlock.On() || nilBlock.DefaultsOn() {
		t.Errorf("nil block: On/DefaultsOn = true, want false")
	}
	f, tr := false, true
	if !(&Pricing{}).On() || !(&Pricing{Enabled: &tr}).On() {
		t.Errorf("present block should be on by default and with enabled: true")
	}
	if (&Pricing{Enabled: &f}).On() {
		t.Errorf("enabled: false should be off")
	}
	if !(&Pricing{}).DefaultsOn() {
		t.Errorf("defaults should back the card by default")
	}
	if (&Pricing{UseDefaults: &f}).DefaultsOn() {
		t.Errorf("use_defaults: false should hide the embedded card")
	}
}
