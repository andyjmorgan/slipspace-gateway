package selection_test

import (
	"encoding/json"
	"testing"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	contractsres "github.com/andyjmorgan/slipspace-gateway/contracts/resilience"
	"github.com/andyjmorgan/slipspace-gateway/internal/selection"
)

// canaryGroup lists one provider twice — a weighted alias canary — plus a
// second provider, with the timeout / retry knobs set so the synthesiser's
// plumbing of every new field is observable.
func canaryGroup() contractsconfig.Group {
	return contractsconfig.Group{
		Mode:           contractsres.ModeLoadBalance,
		TimeoutSeconds: 30,
		Retry: &contractsres.RetryConfig{
			Enabled: true, MaxAttempts: 3, BackoffType: contractsres.BackoffExponential, DelayMilliseconds: 100, MaxDelayMs: 1000,
		},
		Targets: []contractsconfig.Target{
			{Provider: "openai", Alias: "gpt-4o", Weight: 90},
			{Provider: "openai", Alias: "gpt-4o-canary", Weight: 10, TimeoutSeconds: 5},
			{Provider: "ollama"},
		},
	}
}

func TestGroupResilienceConfig_SameProviderTwice_DistinctNamesRealProvider(t *testing.T) {
	rc := selection.GroupResilienceConfig("canary", canaryGroup())

	if len(rc.Targets) != 3 {
		t.Fatalf("targets = %d; want 3", len(rc.Targets))
	}
	wantNames := []string{"openai#gpt-4o", "openai#gpt-4o-canary", "ollama"}
	wantProviders := []string{"openai", "openai", "ollama"}
	for i, tgt := range rc.Targets {
		if tgt.Name != wantNames[i] {
			t.Errorf("targets[%d].Name = %q; want %q", i, tgt.Name, wantNames[i])
		}
		if tgt.Provider != wantProviders[i] {
			t.Errorf("targets[%d].Provider = %q; want the real provider %q (transport re-resolution reads it)", i, tgt.Provider, wantProviders[i])
		}
		if tgt.Order != i+1 {
			t.Errorf("targets[%d].Order = %d; want %d", i, tgt.Order, i+1)
		}
	}
	if rc.Targets[1].TimeoutSeconds != 5 || rc.Targets[0].TimeoutSeconds != 0 {
		t.Errorf("per-target timeout_seconds = %d/%d; want 0/5", rc.Targets[0].TimeoutSeconds, rc.Targets[1].TimeoutSeconds)
	}
	if rc.TimeoutSeconds != 30 {
		t.Errorf("policy TimeoutSeconds = %d; want 30", rc.TimeoutSeconds)
	}
	if rc.Retry == nil || rc.Retry.MaxAttempts != 3 || rc.Retry.BackoffType != contractsres.BackoffExponential {
		t.Errorf("Retry not carried through: %+v", rc.Retry)
	}
	// The synthesised config must pass the contracts validator — distinct
	// names are exactly what ErrDuplicateTargetName would otherwise trip on.
	if err := rc.Validate(); err != nil {
		t.Errorf("synthesised canary group failed Validate: %v", err)
	}
}

func TestGroupResilienceConfig_SingleUseProvider_KeepsPlainName(t *testing.T) {
	rc := selection.GroupResilienceConfig("pool", synthGroup())
	for i, tgt := range rc.Targets {
		if tgt.Name != tgt.Provider {
			t.Errorf("targets[%d].Name = %q; a provider listed once must keep its plain provider name (%q) so existing labels do not move", i, tgt.Name, tgt.Provider)
		}
	}
}

func TestSelect_GroupTargets_CarryResolvedNameAndTimeout(t *testing.T) {
	g := canaryGroup()
	cfg := contractsconfig.Configuration{
		Credentials: map[string]string{"openai": "k", "ollama": ""},
		Bindings:    []contractsconfig.Binding{{Protocol: "chat", Group: "canary"}},
	}
	dest, err := selection.Select("chat", "any-model", cfg, synthProviders, contractsconfig.GroupsConfig{"canary": g})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if dest.Group == nil {
		t.Fatalf("Select did not resolve a group destination: %+v", dest)
	}
	wantNames := []string{"openai#gpt-4o", "openai#gpt-4o-canary", "ollama"}
	for i, tgt := range dest.Group.Targets {
		if tgt.Name != wantNames[i] {
			t.Errorf("resolved targets[%d].Name = %q; want %q", i, tgt.Name, wantNames[i])
		}
	}
	if dest.Group.Targets[1].TimeoutSeconds != 5 {
		t.Errorf("resolved targets[1].TimeoutSeconds = %d; want 5", dest.Group.Targets[1].TimeoutSeconds)
	}
	if dest.Group.TimeoutSeconds != 30 || dest.Group.Retry == nil {
		t.Errorf("resolved group timeout/retry not carried: %d / %+v", dest.Group.TimeoutSeconds, dest.Group.Retry)
	}

	// Request-time and load-time synthesis must still agree for the canary
	// shape — the resolved Target.Name feeds the same ResilienceTarget.Name
	// the validator saw.
	fromResolved, _ := json.Marshal(dest.Group.ResilienceConfig())
	fromAuthored, _ := json.Marshal(selection.GroupResilienceConfig("canary", g))
	if string(fromResolved) != string(fromAuthored) {
		t.Fatalf("request-time and load-time synthesis diverge:\n resolved: %s\n authored: %s", fromResolved, fromAuthored)
	}
}

func TestSelect_SingleBinding_TargetNameIsProvider(t *testing.T) {
	cfg := contractsconfig.Configuration{
		Credentials: map[string]string{"openai": "k"},
		Bindings:    []contractsconfig.Binding{{Protocol: "chat", Provider: "openai"}},
	}
	dest, err := selection.Select("chat", "any-model", cfg, synthProviders, nil)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if dest.Single == nil || dest.Single.Name != "openai" {
		t.Errorf("single-binding target name = %+v; want the provider name", dest.Single)
	}
}

func TestGroup_ResilienceConfig_EmptyNameFallsBackToProvider(t *testing.T) {
	// A hand-built resolved Group (no Select) has no Name on its targets;
	// the synthesiser must not emit an empty ResilienceTarget.Name.
	g := selection.Group{
		Name: "manual",
		Mode: contractsres.ModeFailover,
		Targets: []selection.Target{
			{Provider: "openai"},
			{Provider: "ollama", TimeoutSeconds: 7},
		},
	}
	rc := g.ResilienceConfig()
	if rc.Targets[0].Name != "openai" || rc.Targets[1].Name != "ollama" {
		t.Errorf("names = %q/%q; want the provider names", rc.Targets[0].Name, rc.Targets[1].Name)
	}
	if rc.Targets[1].TimeoutSeconds != 7 {
		t.Errorf("targets[1].TimeoutSeconds = %d; want 7", rc.Targets[1].TimeoutSeconds)
	}
}
