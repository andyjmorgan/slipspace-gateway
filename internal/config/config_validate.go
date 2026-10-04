package config

import (
	"fmt"
	"maps"
	"strings"

	"github.com/google/uuid"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	contractsres "github.com/andyjmorgan/slipspace-gateway/contracts/resilience"
	rulescontract "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/selection"
)

// knownProtocols is the set of generative protocol names a provider may serve
// and a binding may target. Passthrough families are validated separately.
var knownProtocols = map[string]struct{}{
	contractsconfig.ProtocolChat:            {},
	contractsconfig.ProtocolResponses:       {},
	contractsconfig.ProtocolMessages:        {},
	contractsconfig.ProtocolGenerateContent: {},
	contractsconfig.ProtocolEmbeddings:      {},
}

// Validate checks every cross-block invariant of the v2 config: provider and
// group definitions, binding references (provider/group exist and serve the
// protocol — protocol-preserving), model-pattern sanity, passthrough family
// references, rule/connector references, and api-key integrity. It runs before
// buildIndexes so the indexes are only ever built over a valid tree.
func (r *ResolvedConfig) Validate() error {
	if len(r.Configurations) == 0 {
		return ErrNoConfigurations
	}
	if err := r.validateProviders(); err != nil {
		return err
	}
	if err := r.validateGroups(); err != nil {
		return err
	}
	if err := r.validateLibraries(); err != nil {
		return err
	}
	if err := r.Pricing.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrValidation, err)
	}
	if err := r.validateAdvisors(); err != nil {
		return err
	}
	return r.validateConfigurations()
}

// validateAdvisors checks the advisors block and every configuration's
// agent_routing reference into it.
func (r *ResolvedConfig) validateAdvisors() error {
	for name, a := range r.Advisors {
		if err := a.Validate(name); err != nil {
			return fmt.Errorf("%w: %v", ErrValidation, err)
		}
	}
	for cfgName, cfg := range r.Configurations {
		ar := cfg.AgentRouting
		if ar == nil {
			continue
		}
		if _, ok := r.Advisors[ar.Advisor]; !ok {
			return fmt.Errorf("%w: configuration %q: agent_routing advisor %q: %v",
				ErrValidation, cfgName, ar.Advisor, contractsconfig.ErrAgentRoutingAdvisor)
		}
		if len(ar.AllowModels) == 0 {
			return fmt.Errorf("%w: configuration %q: %v",
				ErrValidation, cfgName, contractsconfig.ErrAgentRoutingAllowModels)
		}
	}
	return nil
}

// validIdentifier reports whether name is an operator-authored identifier the
// gateway can use verbatim as a telemetry label and as one half of the
// circuit-breaker (group, provider) key: non-empty, starting with a letter or
// digit, and otherwise drawn from letters, digits, '.', '_' and '-'. The
// breaker store keys on the pair structurally, so the charset is not a
// delimiter-safety requirement — it keeps names sane in metric labels, admin
// URLs (/api/v1/config/{groups,providers}/{name}) and YAML written back by the
// admin API.
func validIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case i > 0 && (r == '.' || r == '_' || r == '-'):
		default:
			return false
		}
	}
	return true
}

// identifierRule is the operator-facing description of validIdentifier, used
// verbatim in every name-rejection message.
const identifierRule = "must start with a letter or digit and contain only letters, digits, '.', '_' or '-'"

func (r *ResolvedConfig) validateProviders() error {
	for name, be := range r.Providers {
		if !validIdentifier(name) {
			return fmt.Errorf("%w: provider %q: name %s", ErrValidation, name, identifierRule)
		}
		if be.BaseURL == "" {
			return fmt.Errorf("%w: provider %q: base_url is required", ErrValidation, name)
		}
		if len(be.Protocols) == 0 && len(be.Passthrough) == 0 {
			return fmt.Errorf("%w: provider %q: declares no protocols or passthrough families", ErrValidation, name)
		}
		for proto, def := range be.Protocols {
			if _, ok := knownProtocols[proto]; !ok {
				return fmt.Errorf("%w: provider %q: unknown protocol %q", ErrValidation, name, proto)
			}
			if err := validateAuth(def.Auth); err != nil {
				return fmt.Errorf("%w: provider %q protocol %q: %v", ErrValidation, name, proto, err)
			}
		}
		for fam, def := range be.Passthrough {
			if len(def.Paths) == 0 {
				return fmt.Errorf("%w: provider %q passthrough %q: declares no paths", ErrValidation, name, fam)
			}
			for i, p := range def.Paths {
				if p.Match == "" {
					return fmt.Errorf("%w: provider %q passthrough %q paths[%d]: match is required", ErrValidation, name, fam, i)
				}
				if len(p.Methods) == 0 {
					return fmt.Errorf("%w: provider %q passthrough %q paths[%d]: methods is required", ErrValidation, name, fam, i)
				}
			}
			if err := validateAuth(def.Auth); err != nil {
				return fmt.Errorf("%w: provider %q passthrough %q: %v", ErrValidation, name, fam, err)
			}
		}
	}
	return nil
}

// validateAuth enforces the auth-block invariant carried over from v1: an
// auth.format string must contain exactly one {key} placeholder, and a format
// requires an auth.header at the same level. Applies identically to a provider
// protocol and to a passthrough family.
func validateAuth(a *contractsconfig.ProviderAuth) error {
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

// validateGroups checks every entry of the groups block. The shape checks
// (identifier name, explicit mode, at least one target, every target names a
// known provider, no two targets that are the same use of one provider) run
// first with group-scoped messages; then the group is synthesised into the
// exact ResilienceConfig the orchestrator will run
// (selection.GroupResilienceConfig) and that config's own Validate runs, which
// closes the contracts/resilience rule set — unknown mode, circuit-breaker
// ranges, enabled-breaker cooldown, per-mode order/weight, retry ranges,
// distinct target names — over the authored group. A group that passes here
// therefore cannot degrade at runtime: the orchestrator sees precisely what
// was validated.
//
// A provider may be listed more than once when the entries differ in alias,
// path or query — a weighted alias canary on one provider is a legal group,
// and Group.TargetNames gives each arm its own breaker key and `target`
// label. Two entries that agree on all of those are indistinguishable at
// runtime (the same upstream call under the same identity) and are rejected.
//
// Mode is required here even though ResilienceConfig.Validate treats "" as
// ModeNone: an omitted mode is far more often a typo'd key than a deliberate
// single-target group, and the silent fallback was the failure mode of #474.
func (r *ResolvedConfig) validateGroups() error {
	for name, g := range r.Groups {
		if !validIdentifier(name) {
			return fmt.Errorf("%w: group %q: name %s", ErrValidation, name, identifierRule)
		}
		if g.Mode == "" {
			return fmt.Errorf("%w: group %q: mode is required (one of %s)", ErrValidation, name, knownModesList())
		}
		if len(g.Targets) == 0 {
			return fmt.Errorf("%w: group %q: declares no targets", ErrValidation, name)
		}
		for i, t := range g.Targets {
			if t.Provider == "" {
				return fmt.Errorf("%w: group %q targets[%d]: provider is required", ErrValidation, name, i)
			}
			if _, ok := r.Providers[t.Provider]; !ok {
				return fmt.Errorf("%w: group %q targets[%d]: unknown provider %q", ErrValidation, name, i, t.Provider)
			}
			for prev := 0; prev < i; prev++ {
				if sameTargetUse(g.Targets[prev], t) {
					return fmt.Errorf("%w: group %q targets[%d]: provider %q with the same alias, path and query is already listed at targets[%d] (the two arms would be indistinguishable; give one a different alias, path or query)", ErrValidation, name, i, t.Provider, prev)
				}
			}
		}
		rc := selection.GroupResilienceConfig(name, g)
		if err := rc.Validate(); err != nil {
			return fmt.Errorf("%w: group %q: %w", ErrValidation, name, err)
		}
	}
	return nil
}

// sameTargetUse reports whether two group targets are the same use of one
// provider — identical provider, alias, path and query. Weight and timeout are
// deliberately not compared: they tune an arm without changing what it calls,
// so two such entries would still be one indistinguishable upstream identity.
func sameTargetUse(a, b contractsconfig.Target) bool {
	return a.Provider == b.Provider && a.Alias == b.Alias && a.Path == b.Path && maps.Equal(a.Query, b.Query)
}

// knownModesList renders the closed ResilienceMode set for error messages.
func knownModesList() string {
	return strings.Join([]string{
		string(contractsres.ModeFailover),
		string(contractsres.ModeLoadBalance),
		string(contractsres.ModeLoadBalanceWithFailover),
		string(contractsres.ModeNone),
	}, ", ")
}

// validateLibraries enforces rule and connector library uniqueness and runs
// each entry's own Validate. Mirrors v1 validateLibraries, scoped to the blocks
// v2 still carries (rules + connectors; resilience is now groups).
func (r *ResolvedConfig) validateLibraries() error {
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
		if conditionHasRetiredEndpoint(rule.Condition) {
			return fmt.Errorf("config: rules[%d] %q: %w", i, rule.Name, ErrRetiredEndpointCondition)
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

// conditionHasRetiredEndpoint reports whether cond (or, recursively, any child
// of a RuleGroup) is the retired "endpoint" discriminator, which the condition
// registry decodes to an inert UnknownCondition. See ErrRetiredEndpointCondition.
func conditionHasRetiredEndpoint(cond rulescontract.Condition) bool {
	switch c := cond.(type) {
	case *rulescontract.UnknownCondition:
		return c.Type == "endpoint"
	case *rulescontract.RuleGroup:
		for _, child := range c.Children {
			if conditionHasRetiredEndpoint(child) {
				return true
			}
		}
	}
	return false
}

func (r *ResolvedConfig) validateConfigurations() error {
	secrets := make(map[string]int, len(r.APIKeys))
	ids := make(map[uuid.UUID]int, len(r.APIKeys))
	for i := range r.APIKeys {
		k := &r.APIKeys[i]
		if k.Secret == "" {
			return fmt.Errorf("%w: api_keys[%d] %q: secret is required", ErrValidation, i, k.Name)
		}
		if prev, dup := secrets[k.Secret]; dup {
			return fmt.Errorf("%w: api_keys[%d] and api_keys[%d]: duplicate secret", ErrValidation, prev, i)
		}
		secrets[k.Secret] = i
		if k.ID != nil {
			if prev, dup := ids[*k.ID]; dup {
				return fmt.Errorf("%w: api_keys[%d] and api_keys[%d]: duplicate id %s", ErrValidation, prev, i, k.ID)
			}
			ids[*k.ID] = i
		}
		if _, ok := r.Configurations[k.Configuration]; !ok {
			return fmt.Errorf("%w: api_keys[%d] %q references %q", ErrUnknownConfiguration, i, k.Name, k.Configuration)
		}
	}

	for name, cfg := range r.Configurations {
		for provider := range cfg.Credentials {
			if _, ok := r.Providers[provider]; !ok {
				return fmt.Errorf("%w: configuration %q credentials reference unknown provider %q", ErrValidation, name, provider)
			}
		}
		if err := r.validateBindings(name, cfg); err != nil {
			return err
		}
		for i, pb := range cfg.PassthroughBindings {
			be, ok := r.Providers[pb.Provider]
			if !ok {
				return fmt.Errorf("%w: configuration %q passthrough_bindings[%d]: unknown provider %q", ErrValidation, name, i, pb.Provider)
			}
			if _, ok := be.Passthrough[pb.Family]; !ok {
				return fmt.Errorf("%w: configuration %q passthrough_bindings[%d]: provider %q has no passthrough family %q", ErrValidation, name, i, pb.Provider, pb.Family)
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
// targets exactly one of provider/group, references resolve, the destination
// serves the binding's protocol (protocol-preserving), model patterns are
// well-formed, and no two bindings on the same protocol collide on an exact
// model or a duplicate catch-all.
func (r *ResolvedConfig) validateBindings(name string, cfg contractsconfig.Configuration) error {
	catchAll := map[string]int{}   // protocol -> binding index that is a catch-all
	exact := map[string]struct{}{} // protocol|model exact pairs already claimed
	for i, b := range cfg.Bindings {
		if _, ok := knownProtocols[b.Protocol]; !ok {
			return fmt.Errorf("%w: configuration %q bindings[%d]: unknown protocol %q", ErrValidation, name, i, b.Protocol)
		}
		hasProvider := b.Provider != ""
		hasGroup := b.Group != ""
		if hasProvider == hasGroup {
			return fmt.Errorf("%w: configuration %q bindings[%d]: set exactly one of provider or group", ErrValidation, name, i)
		}
		if hasProvider {
			be, ok := r.Providers[b.Provider]
			if !ok {
				return fmt.Errorf("%w: configuration %q bindings[%d]: unknown provider %q", ErrValidation, name, i, b.Provider)
			}
			if _, ok := be.Protocols[b.Protocol]; !ok {
				return fmt.Errorf("%w: configuration %q bindings[%d]: provider %q does not serve protocol %q", ErrValidation, name, i, b.Provider, b.Protocol)
			}
		} else {
			g, ok := r.Groups[b.Group]
			if !ok {
				return fmt.Errorf("%w: configuration %q bindings[%d]: unknown group %q", ErrValidation, name, i, b.Group)
			}
			for _, t := range g.Targets {
				be := r.Providers[t.Provider] // existence already checked in validateGroups
				if _, ok := be.Protocols[b.Protocol]; !ok {
					return fmt.Errorf("%w: configuration %q bindings[%d]: group %q target %q does not serve protocol %q (groups are protocol-preserving)", ErrValidation, name, i, b.Group, t.Provider, b.Protocol)
				}
			}
		}
		if err := validateModelPatterns(b.Models); err != nil {
			return fmt.Errorf("%w: configuration %q bindings[%d]: %v", ErrValidation, name, i, err)
		}
		// Collision detection within a protocol: at most one catch-all, and no
		// duplicate exact model. Prefix-overlap detection is deferred.
		if len(b.Models) == 0 {
			if prev, dup := catchAll[b.Protocol]; dup {
				return fmt.Errorf("%w: configuration %q bindings[%d] and bindings[%d]: two catch-all bindings for protocol %q", ErrValidation, name, prev, i, b.Protocol)
			}
			catchAll[b.Protocol] = i
		}
		for _, m := range b.Models {
			if strings.HasSuffix(m, "*") {
				continue
			}
			key := b.Protocol + "|" + m
			if _, dup := exact[key]; dup {
				return fmt.Errorf("%w: configuration %q bindings[%d]: model %q already bound on protocol %q", ErrValidation, name, i, m, b.Protocol)
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
