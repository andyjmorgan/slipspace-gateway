package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"gopkg.in/yaml.v3"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	rulescontract "github.com/andyjmorgan/sluice-gateway/contracts/rules"
)

// MarshalClosure builds the self-contained per-configuration closure for
// configName: a v2 config document carrying only that configuration plus the
// backends, groups, rules, connectors, and api-keys it references. The bytes
// are a valid v2 document a gateway loads + validates verbatim (CP-2), and are
// content-addressed by the returned sha256 hex digest (CP-1). The byte output
// is deterministic — yaml.v3 sorts map keys and the slice blocks preserve
// resolved order — so an unchanged configuration always hashes the same.
//
// Per CP-3 (revised) the closure carries inline secrets, but only this
// configuration's: a fleet member serving configuration X never receives
// configuration Y's credentials, because the dependency walk is scoped to X.
//
// Returns ErrUnknownConfiguration when configName is absent.
func MarshalClosure(resolved *ResolvedConfigV2, configName string) (body []byte, hash string, err error) {
	if resolved == nil {
		return nil, "", fmt.Errorf("config: marshal closure: nil resolved config")
	}
	cfg, ok := resolved.Configurations[configName]
	if !ok {
		return nil, "", fmt.Errorf("config: marshal closure %q: %w", configName, ErrUnknownConfiguration)
	}

	backendSet, groupSet, connectorSet := closureRefs(resolved, cfg)

	backends := contractsconfig.BackendsConfig{}
	for name := range backendSet {
		if b, ok := resolved.Backends[name]; ok {
			backends[name] = b
		}
	}
	groups := contractsconfig.GroupsConfig{}
	for name := range groupSet {
		if g, ok := resolved.Groups[name]; ok {
			groups[name] = g
		}
	}
	connectors := contractsconfig.ConnectorsConfig{}
	for _, c := range resolved.Connectors {
		if connectorSet[c.Name] {
			connectors = append(connectors, c)
		}
	}
	apiKeys := contractsconfig.APIKeysConfig{}
	for _, k := range resolved.APIKeys {
		if k.Configuration == configName {
			apiKeys = append(apiKeys, k)
		}
	}
	rules := closureRules(resolved, configName)

	root := &yaml.Node{Kind: yaml.MappingNode}
	if len(backends) > 0 {
		appendBlock(root, keyBackends, backends)
	}
	if len(groups) > 0 {
		appendBlock(root, keyGroups, groups)
	}
	appendBlock(root, keyConfigurations, map[string]contractsconfig.ConfigurationV2{configName: cfg})
	if len(apiKeys) > 0 {
		appendBlock(root, keyAPIKeys, apiKeys)
	}
	if len(rules) > 0 {
		appendBlock(root, keyRules, rules)
	}
	if len(connectors) > 0 {
		appendBlock(root, keyConnectors, connectors)
	}

	body, err = yaml.Marshal(root)
	if err != nil {
		return nil, "", fmt.Errorf("config: marshal closure %q: %w", configName, err)
	}
	sum := sha256.Sum256(body)
	return body, hex.EncodeToString(sum[:]), nil
}

// closureRefs walks a configuration to collect the names of the backends,
// groups, and connectors it depends on. Credentials keys are the authoritative
// backend set (a configuration holds a credential entry per backend it uses,
// including "" no-cred backends); bindings, passthrough bindings, and the
// targets of referenced groups are unioned in for completeness.
func closureRefs(resolved *ResolvedConfigV2, cfg contractsconfig.ConfigurationV2) (backends, groups, connectors map[string]bool) {
	backends = map[string]bool{}
	groups = map[string]bool{}
	connectors = map[string]bool{}

	for backend := range cfg.Credentials {
		backends[backend] = true
	}
	for _, b := range cfg.Bindings {
		if b.Backend != "" {
			backends[b.Backend] = true
		}
		if b.Group != "" {
			groups[b.Group] = true
		}
	}
	for _, pb := range cfg.PassthroughBindings {
		if pb.Backend != "" {
			backends[pb.Backend] = true
		}
	}
	for group := range groups {
		g, ok := resolved.Groups[group]
		if !ok {
			continue
		}
		for _, tgt := range g.Targets {
			if tgt.Backend != "" {
				backends[tgt.Backend] = true
			}
		}
	}
	for _, cb := range cfg.ConnectorBindings {
		if cb.Connector != "" {
			connectors[cb.Connector] = true
		}
	}
	return backends, groups, connectors
}

func closureRules(resolved *ResolvedConfigV2, configName string) []rulescontract.RuleContract {
	ptrs := resolved.PerConfigurationRules[configName]
	out := make([]rulescontract.RuleContract, 0, len(ptrs))
	for _, p := range ptrs {
		if p != nil {
			out = append(out, *p)
		}
	}
	return out
}
