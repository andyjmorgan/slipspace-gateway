package config

import (
	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	rulescontract "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
)

func cloneAPIKeys(in []contractsconfig.APIKey) []contractsconfig.APIKey {
	if in == nil {
		return nil
	}
	out := make([]contractsconfig.APIKey, len(in))
	copy(out, in)
	return out
}

// cloneRules duplicates each rule into a fresh slot so writers can mutate
// fields without aliasing the live snapshot's rule pointers. The
// polymorphic Condition + Actions fields are NOT deep-copied — the write
// flow replaces them wholesale (the admin write path swaps in a fresh
// Condition/Action sub-tree decoded from the request body) so aliasing
// is not a correctness concern. Mutating an existing rule in place is
// not part of the supported write surface; admin edits always go
// through replace-the-whole-rule.
func cloneRules(in []rulescontract.RuleContract) []rulescontract.RuleContract {
	if in == nil {
		return nil
	}
	out := make([]rulescontract.RuleContract, len(in))
	copy(out, in)
	return out
}

func cloneStringSlice(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneConnectorBindings(in []contractsconfig.ConnectorBinding) []contractsconfig.ConnectorBinding {
	if in == nil {
		return nil
	}
	out := make([]contractsconfig.ConnectorBinding, len(in))
	copy(out, in)
	return out
}
