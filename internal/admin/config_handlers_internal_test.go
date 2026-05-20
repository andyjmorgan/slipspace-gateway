package admin

import (
	"reflect"
	"testing"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	rulescontract "github.com/andyjmorgan/sluice-gateway/contracts/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

func TestSortedCopy(t *testing.T) {
	t.Parallel()
	if got := sortedCopy(nil); !reflect.DeepEqual(got, []string{}) {
		t.Errorf("sortedCopy(nil) = %v, want []", got)
	}
	if got := sortedCopy([]string{}); !reflect.DeepEqual(got, []string{}) {
		t.Errorf("sortedCopy([]) = %v, want []", got)
	}
	in := []string{"c", "a", "b"}
	got := sortedCopy(in)
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("sortedCopy(%v) = %v, want [a b c]", in, got)
	}
	// input slice must not be mutated
	if !reflect.DeepEqual(in, []string{"c", "a", "b"}) {
		t.Errorf("sortedCopy mutated input: %v", in)
	}
}

func TestMethodsFor(t *testing.T) {
	t.Parallel()
	resolved := &config.ResolvedConfig{
		Providers: contractsconfig.ProvidersConfig{
			"openai": contractsconfig.Provider{
				Endpoints: map[string]contractsconfig.Endpoint{
					"chat":   {Method: []string{"POST", "OPTIONS"}},
					"models": {Method: nil},
				},
			},
		},
	}

	// Known endpoint with methods → sorted copy.
	got := methodsFor(resolved, config.Route{Provider: "openai", Endpoint: "chat"})
	if !reflect.DeepEqual(got, []string{"OPTIONS", "POST"}) {
		t.Errorf("known endpoint with methods = %v, want [OPTIONS POST]", got)
	}

	// Endpoint with no methods declared → wildcard placeholder.
	got = methodsFor(resolved, config.Route{Provider: "openai", Endpoint: "models"})
	if !reflect.DeepEqual(got, []string{"*"}) {
		t.Errorf("methodless endpoint = %v, want [*]", got)
	}

	// Unknown provider → nil.
	if got := methodsFor(resolved, config.Route{Provider: "missing", Endpoint: "chat"}); got != nil {
		t.Errorf("unknown provider = %v, want nil", got)
	}

	// Unknown endpoint on known provider → nil.
	if got := methodsFor(resolved, config.Route{Provider: "openai", Endpoint: "missing"}); got != nil {
		t.Errorf("unknown endpoint = %v, want nil", got)
	}
}

func TestBuildRuleAttachments_FallbackToRuleNamesIndex(t *testing.T) {
	t.Parallel()
	// PerConfigurationRules is empty for "prod", but RuleNames + RuleIndex
	// are populated — the fallback path should resolve via the index.
	rule := &rulescontract.RuleContract{
		Name:     "redirect-haiku",
		Behavior: "continue",
	}
	resolved := &config.ResolvedConfig{
		Configurations: contractsconfig.ConfigurationsConfig{
			"prod": contractsconfig.Configuration{RuleNames: []string{"redirect-haiku", "unknown-rule"}},
		},
		RuleIndex:             map[string]*rulescontract.RuleContract{"redirect-haiku": rule},
		PerConfigurationRules: map[string][]*rulescontract.RuleContract{},
	}
	got := buildRuleAttachments("prod", resolved)
	if len(got) != 1 || got[0].Name != "redirect-haiku" {
		t.Errorf("fallback path: got %+v, want exactly redirect-haiku", got)
	}
}

func TestBuildRuleAttachments_UnknownConfigReturnsNil(t *testing.T) {
	t.Parallel()
	resolved := &config.ResolvedConfig{Configurations: contractsconfig.ConfigurationsConfig{}}
	if got := buildRuleAttachments("missing", resolved); got != nil {
		t.Errorf("unknown config = %v, want nil", got)
	}
}
