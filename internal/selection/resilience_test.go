package selection_test

import (
	"encoding/json"
	"testing"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	contractsres "github.com/andyjmorgan/slipspace-gateway/contracts/resilience"
	contractsrules "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/selection"
)

// synthProviders is the two-provider catalogue the synthesiser tests resolve
// groups against; both serve chat so a chat binding at a group is valid.
var synthProviders = contractsconfig.ProvidersConfig{
	"openai": {BaseURL: "http://o", Protocols: map[string]contractsconfig.ProviderProtocol{"chat": {Path: "/c"}}},
	"ollama": {BaseURL: "http://l", Protocols: map[string]contractsconfig.ProviderProtocol{"chat": {Path: "/c"}}},
}

func synthGroup() contractsconfig.Group {
	return contractsconfig.Group{
		Mode:                         contractsres.ModeLoadBalanceWithFailover,
		FailureStatusCodes:           []int{502, 503},
		CircuitBreaker:               &contractsres.CircuitBreakerConfig{Enabled: true, FailureThreshold: 3, CooldownSeconds: 30},
		StrictWeights:                true,
		ResponseHeaderTimeoutSeconds: 20,
		Targets: []contractsconfig.Target{
			{Provider: "openai", Alias: "gpt-4o", Weight: 70},
			{Provider: "ollama"}, // no alias, unset weight
		},
	}
}

func actionTypes(acts []contractsrules.Action) []string {
	out := make([]string, 0, len(acts))
	for _, a := range acts {
		out = append(out, a.ActionType())
	}
	return out
}

func TestGroupResilienceConfig_Shape(t *testing.T) {
	rc := selection.GroupResilienceConfig("pool", synthGroup())

	if rc.Name != "pool" || rc.Mode != contractsres.ModeLoadBalanceWithFailover {
		t.Fatalf("name/mode = %q/%q", rc.Name, rc.Mode)
	}
	if !rc.StrictWeights || rc.ResponseHeaderTimeoutSeconds != 20 || len(rc.FailureStatusCodes) != 2 {
		t.Errorf("orchestration fields not carried: %+v", rc)
	}
	if rc.CircuitBreaker == nil || rc.CircuitBreaker.FailureThreshold != 3 {
		t.Errorf("circuit breaker not carried: %+v", rc.CircuitBreaker)
	}
	if len(rc.Targets) != 2 {
		t.Fatalf("targets = %d, want 2", len(rc.Targets))
	}

	first, second := rc.Targets[0], rc.Targets[1]
	if first.Name != "openai" || first.Provider != "openai" || first.Order != 1 || first.Weight != 70 {
		t.Errorf("targets[0] = %+v", first)
	}
	if got := actionTypes(first.Actions); len(got) != 2 || got[0] != "changeProvider" || got[1] != "changeModelName" {
		t.Errorf("targets[0] actions = %v, want [changeProvider changeModelName]", got)
	}
	if second.Name != "ollama" || second.Order != 2 || second.Weight != 1 {
		t.Errorf("targets[1] = %+v (weight 0 must default to 1, order is declaration position)", second)
	}
	if got := actionTypes(second.Actions); len(got) != 1 || got[0] != "changeProvider" {
		t.Errorf("targets[1] actions = %v, want [changeProvider] (no alias)", got)
	}

	// The synthesised shape must itself pass the contracts validator: this is
	// what config validation runs at load, so a synthesiser that emitted an
	// invalid shape would reject every group.
	if err := rc.Validate(); err != nil {
		t.Fatalf("synthesised config fails Validate: %v", err)
	}
}

// TestGroup_ResilienceConfig_MatchesAuthored proves the two entry points agree:
// the config the request path synthesises from the resolved Group is exactly
// the config validation synthesised from the authored Group at load.
func TestGroup_ResilienceConfig_MatchesAuthored(t *testing.T) {
	g := synthGroup()
	cfg := contractsconfig.Configuration{
		Credentials: map[string]string{"openai": "k", "ollama": ""},
		Bindings:    []contractsconfig.Binding{{Protocol: "chat", Group: "pool"}},
	}
	dest, err := selection.Select("chat", "any-model", cfg, synthProviders, contractsconfig.GroupsConfig{"pool": g})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if dest.Group == nil {
		t.Fatalf("Select did not resolve a group destination: %+v", dest)
	}

	fromResolved, err := json.Marshal(dest.Group.ResilienceConfig())
	if err != nil {
		t.Fatalf("marshal resolved: %v", err)
	}
	fromAuthored, err := json.Marshal(selection.GroupResilienceConfig("pool", g))
	if err != nil {
		t.Fatalf("marshal authored: %v", err)
	}
	if string(fromResolved) != string(fromAuthored) {
		t.Fatalf("request-time and load-time synthesis diverge:\n resolved: %s\n authored: %s", fromResolved, fromAuthored)
	}
}

func TestSingleTargetResilienceConfig(t *testing.T) {
	rc := selection.SingleTargetResilienceConfig(selection.Target{Provider: "azure", Alias: "deployment-x"})
	if rc.Name != "binding:azure" || rc.Mode != contractsres.ModeNone {
		t.Fatalf("name/mode = %q/%q", rc.Name, rc.Mode)
	}
	if len(rc.Targets) != 1 || rc.Targets[0].Provider != "azure" || rc.Targets[0].Order != 1 {
		t.Fatalf("targets = %+v", rc.Targets)
	}
	if got := actionTypes(rc.Targets[0].Actions); len(got) != 2 || got[1] != "changeModelName" {
		t.Errorf("actions = %v, want provider switch + alias rewrite", got)
	}
	if err := rc.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestProviderSwitchActions(t *testing.T) {
	plain := selection.ProviderSwitchActions("openai", "")
	if len(plain) != 1 {
		t.Fatalf("no alias: %d actions, want 1", len(plain))
	}
	cp, ok := plain[0].(*contractsrules.ChangeProviderAction)
	if !ok || cp.NewProvider != "openai" {
		t.Fatalf("plain[0] = %#v, want ChangeProviderAction{openai}", plain[0])
	}

	aliased := selection.ProviderSwitchActions("openai", "gpt-4o")
	if len(aliased) != 2 {
		t.Fatalf("alias: %d actions, want 2", len(aliased))
	}
	cm, ok := aliased[1].(*contractsrules.ChangeModelNameAction)
	if !ok || cm.NewModelName != "gpt-4o" {
		t.Fatalf("aliased[1] = %#v, want ChangeModelNameAction{gpt-4o}", aliased[1])
	}
}
