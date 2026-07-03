package config

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/internal/pricing"
)

const v2Pricing = `
pricing:
  enabled: true
  models:
    - match: "local-model*"
      per_mtok:
        input: 0.5
        output: 1.5
`

// TestLoad_PricingBlock proves the pricing block loads, compiles into a
// PricingTable, and that operator entries price alongside the embedded
// defaults.
func TestLoad_PricingBlock(t *testing.T) {
	dir := writeDir(t, map[string]string{
		"providers.yaml": v2Providers,
		"policy.yaml":    v2Policy,
		"pricing.yaml":   v2Pricing,
	})
	r, err := Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.Pricing == nil || !r.Pricing.On() {
		t.Fatalf("Pricing = %+v, want enabled block", r.Pricing)
	}
	if r.PricingTable == nil {
		t.Fatalf("PricingTable = nil, want compiled table")
	}
	if r.SourceFiles["pricing"] != "pricing.yaml" {
		t.Errorf("SourceFiles[pricing] = %q, want pricing.yaml", r.SourceFiles["pricing"])
	}
	c, ok := r.PricingTable.Cost(pricing.Input{Model: "local-model-7b", At: time.Now(), In: 1_000_000})
	if !ok {
		t.Fatalf("operator entry did not match")
	}
	if got := c.ByCategory[pricing.CategoryInput]; got != 0.5 {
		t.Errorf("input cost = %v, want 0.5", got)
	}
	// Clone shares the block read-only and the rebuilt indexes recompile
	// the table (invariant #9 write path).
	clone := r.Clone()
	if err := clone.RevalidateAndIndex(); err != nil {
		t.Fatalf("RevalidateAndIndex: %v", err)
	}
	if clone.PricingTable == nil {
		t.Errorf("clone PricingTable = nil after reindex")
	}
}

// TestLoad_PricingAbsent leaves costing off: nil block, nil table.
func TestLoad_PricingAbsent(t *testing.T) {
	dir := writeDir(t, map[string]string{
		"providers.yaml": v2Providers,
		"policy.yaml":    v2Policy,
	})
	r, err := Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.Pricing != nil || r.PricingTable != nil {
		t.Errorf("Pricing/PricingTable = %+v/%v, want nil/nil", r.Pricing, r.PricingTable)
	}
}

// TestLoad_PricingDisabled: enabled: false keeps the block but compiles
// no table.
func TestLoad_PricingDisabled(t *testing.T) {
	dir := writeDir(t, map[string]string{
		"providers.yaml": v2Providers,
		"policy.yaml":    v2Policy,
		"pricing.yaml":   "pricing:\n  enabled: false\n",
	})
	r, err := Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.Pricing == nil || r.Pricing.On() {
		t.Fatalf("Pricing = %+v, want present but off", r.Pricing)
	}
	if r.PricingTable != nil {
		t.Errorf("PricingTable = %v, want nil when disabled", r.PricingTable)
	}
}

// TestLoad_PricingInvalid surfaces a validation error with the entry
// context.
func TestLoad_PricingInvalid(t *testing.T) {
	dir := writeDir(t, map[string]string{
		"providers.yaml": v2Providers,
		"policy.yaml":    v2Policy,
		"pricing.yaml":   "pricing:\n  models:\n    - match: \"\"\n      per_mtok: { input: 1 }\n",
	})
	_, err := Load(context.Background(), dir)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}
