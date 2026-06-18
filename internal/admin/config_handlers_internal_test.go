package admin

import (
	"reflect"
	"testing"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	rulescontract "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/config"
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

func TestBuildRuleAttachments_FallbackToRuleNamesIndex(t *testing.T) {
	t.Parallel()
	// PerConfigurationRules is empty for "prod", but RuleNames + RuleIndex
	// are populated — the fallback path should resolve via the index.
	rule := &rulescontract.RuleContract{
		Name:     "redirect-haiku",
		Behavior: "continue",
	}
	resolved := &config.ResolvedConfig{
		Configurations: map[string]contractsconfig.Configuration{
			"prod": {RuleNames: []string{"redirect-haiku", "unknown-rule"}},
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
	resolved := &config.ResolvedConfig{Configurations: map[string]contractsconfig.Configuration{}}
	if got := buildRuleAttachments("missing", resolved); got != nil {
		t.Errorf("unknown config = %v, want nil", got)
	}
}
