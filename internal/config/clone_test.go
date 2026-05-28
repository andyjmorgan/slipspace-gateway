package config

import (
	"testing"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	rulescontract "github.com/andyjmorgan/sluice-gateway/contracts/rules"
)

func TestResolvedConfig_Clone_NilInput(t *testing.T) {
	t.Parallel()
	var r *ResolvedConfig
	if got := r.Clone(); got != nil {
		t.Fatalf("Clone() on nil = %v, want nil", got)
	}
}

func TestResolvedConfig_Clone_RulesMutationDoesNotLeakIntoOriginal(t *testing.T) {
	t.Parallel()
	orig := &ResolvedConfig{
		Rules: []rulescontract.RuleContract{
			{Name: "alpha", Behavior: rulescontract.BehaviorContinue},
			{Name: "beta", Behavior: rulescontract.BehaviorContinue},
		},
	}

	clone := orig.Clone()
	if len(clone.Rules) != 2 {
		t.Fatalf("clone has %d rules, want 2", len(clone.Rules))
	}

	// Mutate the clone — neither the slice header nor the per-element
	// contents must affect the original.
	clone.Rules = append(clone.Rules, rulescontract.RuleContract{Name: "gamma"})
	clone.Rules[0].Behavior = rulescontract.BehaviorExit

	if len(orig.Rules) != 2 {
		t.Errorf("original grew to %d rules after clone append", len(orig.Rules))
	}
	if orig.Rules[0].Behavior != rulescontract.BehaviorContinue {
		t.Errorf("original rule[0].Behavior changed to %q after clone mutation", orig.Rules[0].Behavior)
	}
}

func TestResolvedConfig_Clone_ConfigurationRuleNamesIsolation(t *testing.T) {
	t.Parallel()
	orig := &ResolvedConfig{
		Configurations: contractsconfig.ConfigurationsConfig{
			"dev": {RuleNames: []string{"rule-a", "rule-b"}},
		},
	}

	clone := orig.Clone()
	devClone := clone.Configurations["dev"]
	devClone.RuleNames = append(devClone.RuleNames, "rule-c")
	clone.Configurations["dev"] = devClone

	if got := len(orig.Configurations["dev"].RuleNames); got != 2 {
		t.Errorf("original RuleNames len = %d, want 2", got)
	}
	if got := len(clone.Configurations["dev"].RuleNames); got != 3 {
		t.Errorf("clone RuleNames len = %d, want 3", got)
	}
}

func TestResolvedConfig_Clone_NilSlicesAndMapsStayNil(t *testing.T) {
	t.Parallel()
	orig := &ResolvedConfig{
		Configurations: contractsconfig.ConfigurationsConfig{
			"empty": {},
		},
	}

	clone := orig.Clone()
	got := clone.Configurations["empty"]
	if got.RuleNames != nil {
		t.Errorf("empty RuleNames became non-nil after clone")
	}
	if got.Tags != nil {
		t.Errorf("empty Tags became non-nil after clone")
	}
	if got.UpstreamCredentials != nil {
		t.Errorf("empty UpstreamCredentials became non-nil after clone")
	}
}

func TestResolvedConfig_Clone_IndexesAreReset(t *testing.T) {
	t.Parallel()
	rule := rulescontract.RuleContract{Name: "alpha"}
	orig := &ResolvedConfig{
		Rules:                 []rulescontract.RuleContract{rule},
		RuleIndex:             map[string]*rulescontract.RuleContract{"alpha": &rule},
		PerConfigurationRules: map[string][]*rulescontract.RuleContract{"dev": {&rule}},
	}

	clone := orig.Clone()

	if clone.RuleIndex != nil {
		t.Errorf("clone.RuleIndex must be nil so buildIndexes repopulates")
	}
	if clone.PerConfigurationRules != nil {
		t.Errorf("clone.PerConfigurationRules must be nil so buildIndexes repopulates")
	}
}

func TestResolvedConfig_Clone_PopulatedMapsAndBindingsAreDeepCopied(t *testing.T) {
	t.Parallel()
	orig := &ResolvedConfig{
		Configurations: contractsconfig.ConfigurationsConfig{
			"full": {
				RuleNames:           []string{"alpha"},
				Tags:                map[string]string{"tier": "dev", "team": "platform"},
				UpstreamCredentials: map[string]string{"openai": "sk-dev"},
				ConnectorBindings: []contractsconfig.ConnectorBinding{
					{Connector: "audit"},
				},
			},
		},
	}

	clone := orig.Clone()
	cl := clone.Configurations["full"]
	cl.Tags["team"] = "infrastructure"
	cl.UpstreamCredentials["openai"] = "sk-other"
	cl.ConnectorBindings[0].Connector = "different"

	if orig.Configurations["full"].Tags["team"] != "platform" {
		t.Errorf("original Tags mutated through clone")
	}
	if orig.Configurations["full"].UpstreamCredentials["openai"] != "sk-dev" {
		t.Errorf("original UpstreamCredentials mutated through clone")
	}
	if orig.Configurations["full"].ConnectorBindings[0].Connector != "audit" {
		t.Errorf("original ConnectorBindings mutated through clone")
	}
}

func TestResolvedConfig_RevalidateAndIndex_HappyPath(t *testing.T) {
	t.Parallel()
	rule := rulescontract.RuleContract{
		Name: "alpha",
		Condition: &rulescontract.ProviderCondition{
			Type: "provider", Operator: rulescontract.EnumEquals, ExpectedProvider: "openai",
		},
		Actions: []rulescontract.Action{
			&rulescontract.SetHeaderAction{Type: "setHeader", HeaderName: "X-Tag", HeaderAction: rulescontract.HeaderSet, HeaderValue: "ok"},
		},
	}
	r := &ResolvedConfig{
		Configurations: contractsconfig.ConfigurationsConfig{
			"dev": {RuleNames: []string{"alpha"}},
		},
		Rules: []rulescontract.RuleContract{rule},
	}
	if err := r.RevalidateAndIndex(); err != nil {
		t.Fatalf("RevalidateAndIndex: %v", err)
	}
	if r.RuleIndex["alpha"] == nil {
		t.Errorf("RuleIndex not rebuilt")
	}
	if len(r.PerConfigurationRules["dev"]) != 1 {
		t.Errorf("PerConfigurationRules not rebuilt")
	}
}

func TestResolvedConfig_RevalidateAndIndex_ValidationError(t *testing.T) {
	t.Parallel()
	r := &ResolvedConfig{
		Configurations: contractsconfig.ConfigurationsConfig{
			"dev": {RuleNames: []string{"missing"}},
		},
	}
	if err := r.RevalidateAndIndex(); err == nil {
		t.Fatal("RevalidateAndIndex want error for unknown rule reference, got nil")
	}
}

func TestResolvedConfig_Clone_APIKeysIsolated(t *testing.T) {
	t.Parallel()
	orig := &ResolvedConfig{
		APIKeys: []contractsconfig.APIKey{
			{Name: "key-1", Configuration: "dev", Secret: "sk_test", Enabled: true},
		},
	}

	clone := orig.Clone()
	clone.APIKeys = append(clone.APIKeys, contractsconfig.APIKey{Name: "key-2"})
	clone.APIKeys[0].Enabled = false

	if got := len(orig.APIKeys); got != 1 {
		t.Errorf("original APIKeys grew to %d", got)
	}
	if !orig.APIKeys[0].Enabled {
		t.Errorf("original APIKeys[0].Enabled mutated to false")
	}
}
