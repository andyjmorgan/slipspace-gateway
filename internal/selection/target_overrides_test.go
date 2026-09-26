package selection_test

import (
	"testing"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/selection"
)

// overridesFixture is a minimal provider + configuration pair for exercising
// the per-target Path / Query override plumbing (issue #409).
func overridesFixture() (contractsconfig.Configuration, contractsconfig.ProvidersConfig, contractsconfig.GroupsConfig) {
	providers := contractsconfig.ProvidersConfig{
		"azure": {
			BaseURL: "https://az.example.com",
			Query:   map[string]string{"api-version": "2024-02-01", "keep": "provider"},
			Protocols: map[string]contractsconfig.ProviderProtocol{
				"chat": {Path: "/openai/chat/completions"},
			},
		},
	}
	cfg := contractsconfig.Configuration{
		Credentials: map[string]string{"azure": "k"},
		Bindings: []contractsconfig.Binding{
			{
				Protocol: "chat", Models: []string{"dep-*"}, Provider: "azure",
				Path:  "/openai/deployments/gpt4o/chat/completions",
				Query: map[string]string{"api-version": "2025-01-01"},
			},
			{Protocol: "chat", Models: []string{"grp-*"}, Group: "ha"},
			{Protocol: "chat", Provider: "azure"},
		},
	}
	groups := contractsconfig.GroupsConfig{
		"ha": {
			Mode: "failover",
			Targets: []contractsconfig.Target{
				{Provider: "azure", Path: "/openai/deployments/primary/chat/completions", Query: map[string]string{"api-version": "g1"}},
				{Provider: "azure"},
			},
		},
	}
	return cfg, providers, groups
}

func TestSelect_RetainsAuthoredOverrides(t *testing.T) {
	t.Parallel()
	cfg, providers, groups := overridesFixture()

	t.Run("binding overrides retained", func(t *testing.T) {
		t.Parallel()
		dest, err := selection.Select("chat", "dep-1", cfg, providers, groups)
		if err != nil {
			t.Fatalf("select: %v", err)
		}
		tgt := dest.Single
		if tgt.PathOverride != "/openai/deployments/gpt4o/chat/completions" {
			t.Errorf("PathOverride = %q", tgt.PathOverride)
		}
		if tgt.QueryOverride["api-version"] != "2025-01-01" || len(tgt.QueryOverride) != 1 {
			t.Errorf("QueryOverride = %v, want only the authored override", tgt.QueryOverride)
		}
		// Effective values still composed as before.
		if tgt.Path != tgt.PathOverride {
			t.Errorf("Path = %q, want override to win", tgt.Path)
		}
		if tgt.Query["api-version"] != "2025-01-01" || tgt.Query["keep"] != "provider" {
			t.Errorf("Query = %v, want provider ∪ override", tgt.Query)
		}
	})

	t.Run("group target overrides retained per target", func(t *testing.T) {
		t.Parallel()
		dest, err := selection.Select("chat", "grp-1", cfg, providers, groups)
		if err != nil {
			t.Fatalf("select: %v", err)
		}
		ts := dest.Group.Targets
		if ts[0].PathOverride == "" || ts[0].QueryOverride["api-version"] != "g1" {
			t.Errorf("targets[0] overrides not retained: %+v", ts[0])
		}
		if ts[1].PathOverride != "" || ts[1].QueryOverride != nil {
			t.Errorf("targets[1] should carry no overrides: %+v", ts[1])
		}
	})

	t.Run("no overrides yields empty carriers", func(t *testing.T) {
		t.Parallel()
		dest, err := selection.Select("chat", "plain", cfg, providers, groups)
		if err != nil {
			t.Fatalf("select: %v", err)
		}
		if dest.Single.PathOverride != "" || dest.Single.QueryOverride != nil {
			t.Errorf("unexpected overrides: %+v", dest.Single)
		}
		if dest.Single.Path != "/openai/chat/completions" {
			t.Errorf("Path = %q, want provider default", dest.Single.Path)
		}
	})
}

// TestResolveTarget_AppliesCarriedOverrides is the final-handler contract:
// re-resolving on the post-rule provider with the carried overrides must
// yield the same transport Select produced, and re-resolving without them
// must fall back to the provider defaults (the pre-fix behaviour the e2e
// guards against).
func TestResolveTarget_AppliesCarriedOverrides(t *testing.T) {
	t.Parallel()
	cfg, providers, _ := overridesFixture()

	with, err := selection.ResolveTarget("chat", contractsconfig.Target{
		Provider: "azure",
		Path:     "/openai/deployments/gpt4o/chat/completions",
		Query:    map[string]string{"api-version": "2025-01-01"},
	}, cfg, providers)
	if err != nil {
		t.Fatalf("resolve with overrides: %v", err)
	}
	if with.Path != "/openai/deployments/gpt4o/chat/completions" {
		t.Errorf("Path = %q, want binding override", with.Path)
	}
	if with.Query["api-version"] != "2025-01-01" || with.Query["keep"] != "provider" {
		t.Errorf("Query = %v, want override composed over provider default", with.Query)
	}

	without, err := selection.ResolveTarget("chat", contractsconfig.Target{Provider: "azure"}, cfg, providers)
	if err != nil {
		t.Fatalf("resolve without overrides: %v", err)
	}
	if without.Path != "/openai/chat/completions" || without.Query["api-version"] != "2024-02-01" {
		t.Errorf("provider-only resolution = path %q query %v; want provider defaults", without.Path, without.Query)
	}
}
