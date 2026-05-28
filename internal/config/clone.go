package config

import (
	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	rulescontract "github.com/andyjmorgan/sluice-gateway/contracts/rules"
)

// Clone returns a copy of r safe for the admin-write mutation flow:
// modify the clone, run Validate + buildIndexes on the clone, and only
// then publish via Store.Replace.
//
// Depth contract:
//
//   - Rules: deep-copied so an add/edit/delete on the clone's rule slice
//     does not change pointers the still-live snapshot's rule readers
//     hold. Indexes (RuleIndex, PerConfigurationRules) are cleared on
//     the clone — buildIndexes rebuilds them from the new Rules slice.
//   - Configurations: deep-copied at the map level, with each entry's
//     mutable slice fields (RuleNames, Tags, ConnectorBindings) copied
//     so an admin write that touches RuleNames does not bleed into the
//     live configuration.
//   - APIKeys: deep-copied because the api_keys writer will append /
//     remove entries.
//   - Providers, ResiliencePolicies, Connectors, Admin, Telemetry:
//     shallow-copied. These are not write targets in the current admin
//     surface; when they become writable, this method grows additional
//     deep-copy branches.
//   - All *Index fields are nil on the clone — Validate's buildIndexes
//     repopulates them from the canonical slices/maps.
//
// Pointer stability: this Clone deliberately invalidates the per-rule
// pointers that the snapshot's PerConfigurationRules slices hold. The
// caller must therefore Replace the entire snapshot atomically rather
// than splicing the clone into the live one — which is the only path
// the Store offers anyway. Holders of pre-Replace pointers (e.g. an
// in-flight rule evaluator that already loaded the old snapshot) keep
// reading their snapshot consistently per the Store contract.
func (r *ResolvedConfig) Clone() *ResolvedConfig {
	if r == nil {
		return nil
	}

	out := &ResolvedConfig{
		// Shallow-copy maps that are not yet writable.
		Providers:          r.Providers,
		ResiliencePolicies: r.ResiliencePolicies,
		Connectors:         r.Connectors,
		Admin:              r.Admin,
		Telemetry:          r.Telemetry,
	}

	out.Configurations = cloneConfigurations(r.Configurations)
	out.APIKeys = cloneAPIKeys(r.APIKeys)
	out.Rules = cloneRules(r.Rules)

	// Indexes are intentionally nil; caller runs buildIndexes after
	// mutating the clone.
	return out
}

// cloneConfigurations deep-copies the configurations map, snapshotting
// each entry's mutable slices so an admin write that touches RuleNames
// does not leak into the live snapshot.
func cloneConfigurations(in contractsconfig.ConfigurationsConfig) contractsconfig.ConfigurationsConfig {
	if in == nil {
		return nil
	}
	out := make(contractsconfig.ConfigurationsConfig, len(in))
	for name, cfg := range in {
		dup := cfg
		dup.RuleNames = cloneStringSlice(cfg.RuleNames)
		dup.Tags = cloneStringMap(cfg.Tags)
		dup.UpstreamCredentials = cloneStringMap(cfg.UpstreamCredentials)
		dup.ConnectorBindings = cloneConnectorBindings(cfg.ConnectorBindings)
		out[name] = dup
	}
	return out
}

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
