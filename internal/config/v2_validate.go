package config

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	rulescontract "github.com/andyjmorgan/sluice-gateway/contracts/rules"
)

// knownProtocols is the set of generative protocol names a backend may serve
// and a binding may target. Passthrough families are validated separately.
var knownProtocols = map[string]struct{}{
	contractsconfig.ProtocolChat:            {},
	contractsconfig.ProtocolResponses:       {},
	contractsconfig.ProtocolMessages:        {},
	contractsconfig.ProtocolGenerateContent: {},
	contractsconfig.ProtocolEmbeddings:      {},
}

// Validate checks every cross-block invariant of the v2 config: backend and
// group definitions, binding references (backend/group exist and serve the
// protocol — protocol-preserving), model-pattern sanity, passthrough family
// references, rule/connector references, and api-key integrity. It runs before
// buildIndexes so the indexes are only ever built over a valid tree.
func (r *ResolvedConfigV2) Validate() error {
	if len(r.Configurations) == 0 {
		return ErrNoConfigurations
	}
	if err := r.validateBackends(); err != nil {
		return err
	}
	if err := r.validateGroups(); err != nil {
		return err
	}
	if err := r.validateLibrariesV2(); err != nil {
		return err
	}
	return r.validateConfigurationsV2()
}

func (r *ResolvedConfigV2) validateBackends() error {
	for name, be := range r.Backends {
		if be.BaseURL == "" {
			return fmt.Errorf("%w: backend %q: base_url is required", ErrV2Validation, name)
		}
		if len(be.Protocols) == 0 && len(be.Passthrough) == 0 {
			return fmt.Errorf("%w: backend %q: declares no protocols or passthrough families", ErrV2Validation, name)
		}
		for proto, def := range be.Protocols {
			if _, ok := knownProtocols[proto]; !ok {
				return fmt.Errorf("%w: backend %q: unknown protocol %q", ErrV2Validation, name, proto)
			}
			if err := validateAuth(def.Auth); err != nil {
				return fmt.Errorf("%w: backend %q protocol %q: %v", ErrV2Validation, name, proto, err)
			}
		}
		for fam, def := range be.Passthrough {
			if len(def.Paths) == 0 {
				return fmt.Errorf("%w: backend %q passthrough %q: declares no paths", ErrV2Validation, name, fam)
			}
			for i, p := range def.Paths {
				if p.Match == "" {
					return fmt.Errorf("%w: backend %q passthrough %q paths[%d]: match is required", ErrV2Validation, name, fam, i)
				}
				if len(p.Methods) == 0 {
					return fmt.Errorf("%w: backend %q passthrough %q paths[%d]: methods is required", ErrV2Validation, name, fam, i)
				}
			}
			if err := validateAuth(def.Auth); err != nil {
				return fmt.Errorf("%w: backend %q passthrough %q: %v", ErrV2Validation, name, fam, err)
			}
		}
	}
	return nil
}

// validateAuth enforces the same auth_format invariant as v1: a format string
// must carry exactly one {key} placeholder, and a format requires a header.
func validateAuth(a *contractsconfig.BackendAuth) error {
	if a == nil {
		return nil
	}
	if a.Format != "" {
		if a.Header == "" {
			return ErrAuthFormatWithoutHeader
		}
		if strings.Count(a.Format, "{key}") != 1 {
			return ErrInvalidAuthFormat
		}
	}
	return nil
}

func (r *ResolvedConfigV2) validateGroups() error {
	for name, g := range r.Groups {
		if len(g.Targets) == 0 {
			return fmt.Errorf("%w: group %q: declares no targets", ErrV2Validation, name)
		}
		for i, t := range g.Targets {
			if t.Backend == "" {
				return fmt.Errorf("%w: group %q targets[%d]: backend is required", ErrV2Validation, name, i)
			}
			if _, ok := r.Backends[t.Backend]; !ok {
				return fmt.Errorf("%w: group %q targets[%d]: unknown backend %q", ErrV2Validation, name, i, t.Backend)
			}
		}
	}
	return nil
}

// validateLibrariesV2 enforces rule and connector library uniqueness and runs
// each entry's own Validate. Mirrors v1 validateLibraries, scoped to the blocks
// v2 still carries (rules + connectors; resilience is now groups).
func (r *ResolvedConfigV2) validateLibrariesV2() error {
	ruleNames := make(map[string]int, len(r.Rules))
	for i := range r.Rules {
		rule := &r.Rules[i]
		if prev, dup := ruleNames[rule.Name]; dup {
			return fmt.Errorf("config: rules[%d] and rules[%d] name=%q: %w", prev, i, rule.Name, ErrDuplicateRuleName)
		}
		ruleNames[rule.Name] = i
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("config: rules[%d]: %w", i, err)
		}
	}

	connNames := make(map[string]int, len(r.Connectors))
	for i := range r.Connectors {
		c := &r.Connectors[i]
		if prev, dup := connNames[c.Name]; dup {
			return fmt.Errorf("config: connectors[%d] and connectors[%d] name=%q: %w", prev, i, c.Name, ErrDuplicateConnectorName)
		}
		connNames[c.Name] = i
		if err := c.Validate(); err != nil {
			return fmt.Errorf("config: connectors[%d]: %w", i, err)
		}
	}
	return nil
}

func (r *ResolvedConfigV2) validateConfigurationsV2() error {
	secrets := make(map[string]int, len(r.APIKeys))
	ids := make(map[uuid.UUID]int, len(r.APIKeys))
	for i := range r.APIKeys {
		k := &r.APIKeys[i]
		if k.Secret == "" {
			return fmt.Errorf("%w: api_keys[%d] %q: secret is required", ErrV2Validation, i, k.Name)
		}
		if prev, dup := secrets[k.Secret]; dup {
			return fmt.Errorf("%w: api_keys[%d] and api_keys[%d]: duplicate secret", ErrV2Validation, prev, i)
		}
		secrets[k.Secret] = i
		if k.ID != nil {
			if prev, dup := ids[*k.ID]; dup {
				return fmt.Errorf("%w: api_keys[%d] and api_keys[%d]: duplicate id %s", ErrV2Validation, prev, i, k.ID)
			}
			ids[*k.ID] = i
		}
		if _, ok := r.Configurations[k.Configuration]; !ok {
			return fmt.Errorf("%w: api_keys[%d] %q references %q", ErrUnknownConfiguration, i, k.Name, k.Configuration)
		}
	}

	for name, cfg := range r.Configurations {
		for backend := range cfg.Credentials {
			if _, ok := r.Backends[backend]; !ok {
				return fmt.Errorf("%w: configuration %q credentials reference unknown backend %q", ErrV2Validation, name, backend)
			}
		}
		if err := r.validateBindings(name, cfg); err != nil {
			return err
		}
		for i, pb := range cfg.PassthroughBindings {
			be, ok := r.Backends[pb.Backend]
			if !ok {
				return fmt.Errorf("%w: configuration %q passthrough_bindings[%d]: unknown backend %q", ErrV2Validation, name, i, pb.Backend)
			}
			if _, ok := be.Passthrough[pb.Family]; !ok {
				return fmt.Errorf("%w: configuration %q passthrough_bindings[%d]: backend %q has no passthrough family %q", ErrV2Validation, name, i, pb.Backend, pb.Family)
			}
		}
		for _, ruleName := range cfg.RuleNames {
			if !ruleDefined(r.Rules, ruleName) {
				return fmt.Errorf("%w: configuration %q references rule %q", ErrUnknownRuleName, name, ruleName)
			}
		}
		for i, cb := range cfg.ConnectorBindings {
			if !connectorDefined(r.Connectors, cb.Connector) {
				return fmt.Errorf("%w: configuration %q connector_bindings[%d] references %q", ErrUnknownConnectorReference, name, i, cb.Connector)
			}
		}
	}
	return nil
}

// validateBindings checks one configuration's generative bindings: each
// targets exactly one of backend/group, references resolve, the destination
// serves the binding's protocol (protocol-preserving), model patterns are
// well-formed, and no two bindings on the same protocol collide on an exact
// model or a duplicate catch-all.
func (r *ResolvedConfigV2) validateBindings(name string, cfg contractsconfig.ConfigurationV2) error {
	catchAll := map[string]int{}   // protocol -> binding index that is a catch-all
	exact := map[string]struct{}{} // protocol|model exact pairs already claimed
	for i, b := range cfg.Bindings {
		if _, ok := knownProtocols[b.Protocol]; !ok {
			return fmt.Errorf("%w: configuration %q bindings[%d]: unknown protocol %q", ErrV2Validation, name, i, b.Protocol)
		}
		hasBackend := b.Backend != ""
		hasGroup := b.Group != ""
		if hasBackend == hasGroup {
			return fmt.Errorf("%w: configuration %q bindings[%d]: set exactly one of backend or group", ErrV2Validation, name, i)
		}
		if hasBackend {
			be, ok := r.Backends[b.Backend]
			if !ok {
				return fmt.Errorf("%w: configuration %q bindings[%d]: unknown backend %q", ErrV2Validation, name, i, b.Backend)
			}
			if _, ok := be.Protocols[b.Protocol]; !ok {
				return fmt.Errorf("%w: configuration %q bindings[%d]: backend %q does not serve protocol %q", ErrV2Validation, name, i, b.Backend, b.Protocol)
			}
		} else {
			g, ok := r.Groups[b.Group]
			if !ok {
				return fmt.Errorf("%w: configuration %q bindings[%d]: unknown group %q", ErrV2Validation, name, i, b.Group)
			}
			for _, t := range g.Targets {
				be := r.Backends[t.Backend] // existence already checked in validateGroups
				if _, ok := be.Protocols[b.Protocol]; !ok {
					return fmt.Errorf("%w: configuration %q bindings[%d]: group %q target %q does not serve protocol %q (groups are protocol-preserving)", ErrV2Validation, name, i, b.Group, t.Backend, b.Protocol)
				}
			}
		}
		if err := validateModelPatterns(b.Models); err != nil {
			return fmt.Errorf("%w: configuration %q bindings[%d]: %v", ErrV2Validation, name, i, err)
		}
		// Collision detection within a protocol: at most one catch-all, and no
		// duplicate exact model. Prefix-overlap detection is deferred.
		if len(b.Models) == 0 {
			if prev, dup := catchAll[b.Protocol]; dup {
				return fmt.Errorf("%w: configuration %q bindings[%d] and bindings[%d]: two catch-all bindings for protocol %q", ErrV2Validation, name, prev, i, b.Protocol)
			}
			catchAll[b.Protocol] = i
		}
		for _, m := range b.Models {
			if strings.HasSuffix(m, "*") {
				continue
			}
			key := b.Protocol + "|" + m
			if _, dup := exact[key]; dup {
				return fmt.Errorf("%w: configuration %q bindings[%d]: model %q already bound on protocol %q", ErrV2Validation, name, i, m, b.Protocol)
			}
			exact[key] = struct{}{}
		}
	}
	return nil
}

// validateModelPatterns rejects interior or multiple wildcards; only a single
// trailing `*` is supported (matching the runtime matcher).
func validateModelPatterns(patterns []string) error {
	for _, p := range patterns {
		star := strings.Count(p, "*")
		if star == 0 {
			continue
		}
		if star > 1 || !strings.HasSuffix(p, "*") {
			return fmt.Errorf("model pattern %q: only a single trailing '*' is supported", p)
		}
	}
	return nil
}

func ruleDefined(rules []rulescontract.RuleContract, name string) bool {
	for i := range rules {
		if rules[i].Name == name {
			return true
		}
	}
	return false
}

func connectorDefined(connectors contractsconfig.ConnectorsConfig, name string) bool {
	for i := range connectors {
		if connectors[i].Name == name {
			return true
		}
	}
	return false
}
