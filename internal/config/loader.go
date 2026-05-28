package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	koanfyaml "github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	"gopkg.in/yaml.v3"

	"github.com/andyjmorgan/sluice-gateway/contracts/admin"
	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/contracts/resilience"
	rulescontract "github.com/andyjmorgan/sluice-gateway/contracts/rules"
)

const (
	keyProviders          = "providers"
	keyConfigurations     = "configurations"
	keyAPIKeys            = "api_keys"
	keyRules              = "rules"
	keyResiliencePolicies = "resilience_policies"
	keyConnectors         = "connectors"
	keyAdmin              = "admin"
	keyTelemetry          = "telemetry"
)

const (
	filenameProviders = "providers.yaml"
	filenamePolicy    = "policy.yaml"
	filenameAdmin     = "admin.yaml"
)

// allowedKeysByFile pins each accepted filename to the top-level keys it is
// permitted to carry. Any key in the file that is not in its set is reported
// as ErrWrongFileForKey.
var allowedKeysByFile = map[string]map[string]struct{}{
	filenameProviders: {
		keyProviders: {},
	},
	filenamePolicy: {
		keyConfigurations:     {},
		keyAPIKeys:            {},
		keyRules:              {},
		keyResiliencePolicies: {},
		keyConnectors:         {},
	},
	filenameAdmin: {
		keyAdmin:     {},
		keyTelemetry: {},
	},
}

// Load reads the YAML configuration directory at dir and returns the merged,
// validated, indexed runtime view.
//
// The directory must contain only the two accepted filenames (providers.yaml,
// policy.yaml). Each file is restricted to a specific set of top-level keys;
// keys outside that set are rejected. Server-level configuration is sourced
// from the SLUICE_* env vars via LoadEnv — it is no longer part of the YAML
// tree.
func Load(ctx context.Context, dir string) (*ResolvedConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("config: load %q: %w", dir, err)
	}

	files, err := ListConfigFiles(dir)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("config: load %q: %w", dir, ErrEmptyDirectory)
	}

	merged, err := mergeFiles(files)
	if err != nil {
		return nil, err
	}

	resolved, err := decode(merged)
	if err != nil {
		return nil, err
	}

	if err := resolved.Validate(); err != nil {
		return nil, err
	}

	resolved.buildIndexes()
	return resolved, nil
}

// ListConfigFiles enumerates dir and returns a map of accepted filename to its
// absolute path. Any non-directory entry whose name is not one of the two
// accepted filenames produces ErrUnexpectedConfigFile; subdirectories are
// skipped silently.
//
// Exported so the admin config-export bundler can iterate the same files the
// loader saw without duplicating the allowedKeysByFile list.
func ListConfigFiles(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("config: read dir %q: %w", dir, err)
	}
	out := make(map[string]string, len(allowedKeysByFile))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if _, ok := allowedKeysByFile[name]; !ok {
			return nil, fmt.Errorf("config: %s in %s: %w", name, dir, ErrUnexpectedConfigFile)
		}
		out[name] = filepath.Join(dir, name)
	}
	return out, nil
}

// mergedTree holds the per-top-level-key yaml.Node trees assembled across the
// configuration directory, alongside the filename that contributed each block
// so wrong-file errors can name the offender.
type mergedTree struct {
	nodes  map[string]*yaml.Node
	origin map[string]string
}

func mergeFiles(files map[string]string) (*mergedTree, error) {
	out := &mergedTree{
		nodes:  make(map[string]*yaml.Node),
		origin: make(map[string]string),
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		path := files[name]
		topLevel, err := parseTopLevel(path)
		if err != nil {
			return nil, err
		}
		allowed := allowedKeysByFile[name]
		for key, node := range topLevel {
			if _, ok := allowed[key]; !ok {
				return nil, fmt.Errorf("config: key %q in %s: %w", key, path, ErrWrongFileForKey)
			}
			out.nodes[key] = node
			out.origin[key] = path
		}
	}

	return out, nil
}

// parseTopLevel loads path through koanf (the approved file+YAML layer), then
// re-marshals the tree to bytes and reparses with yaml.v3 to obtain per-key
// yaml.Node trees.
//
// The round-trip preserves verbatim rules/resilience subtrees while
// normalizing input to a guaranteed mapping shape; that lets the second-stage
// parser stay branch-free.
func parseTopLevel(path string) (map[string]*yaml.Node, error) {
	k := koanf.New(".")
	if err := k.Load(file.Provider(path), koanfyaml.Parser()); err != nil {
		return nil, fmt.Errorf("config: load %s: %w: %w", path, ErrParse, err)
	}

	raw, err := k.Marshal(koanfyaml.Parser())
	if err != nil {
		return nil, fmt.Errorf("config: round-trip %s: %w", path, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w: %w", path, ErrParse, err)
	}

	if doc.Kind == 0 || len(doc.Content) == 0 {
		return map[string]*yaml.Node{}, nil
	}
	root := doc.Content[0]

	out := make(map[string]*yaml.Node, len(root.Content)/2)
	for i := 0; i+1 < len(root.Content); i += 2 {
		out[root.Content[i].Value] = root.Content[i+1]
	}
	return out, nil
}

func decode(merged *mergedTree) (*ResolvedConfig, error) {
	out := &ResolvedConfig{
		Providers:      contractsconfig.ProvidersConfig{},
		Configurations: contractsconfig.ConfigurationsConfig{},
	}

	if node, ok := merged.nodes[keyProviders]; ok {
		if err := node.Decode(&out.Providers); err != nil {
			return nil, fmt.Errorf("config: decode providers: %w: %w", ErrParse, err)
		}
	}
	if node, ok := merged.nodes[keyConfigurations]; ok {
		if err := node.Decode(&out.Configurations); err != nil {
			return nil, fmt.Errorf("config: decode configurations: %w: %w", ErrParse, err)
		}
	}
	if node, ok := merged.nodes[keyAPIKeys]; ok {
		if err := node.Decode(&out.APIKeys); err != nil {
			return nil, fmt.Errorf("config: decode api_keys: %w: %w", ErrParse, err)
		}
	}
	if node, ok := merged.nodes[keyRules]; ok {
		if err := node.Decode(&out.Rules); err != nil {
			return nil, fmt.Errorf("config: decode rules: %w: %w", ErrParse, err)
		}
	}
	if node, ok := merged.nodes[keyResiliencePolicies]; ok {
		if err := node.Decode(&out.ResiliencePolicies); err != nil {
			return nil, fmt.Errorf("config: decode resilience_policies: %w: %w", ErrParse, err)
		}
	}
	if node, ok := merged.nodes[keyConnectors]; ok {
		if err := node.Decode(&out.Connectors); err != nil {
			return nil, fmt.Errorf("config: decode connectors: %w: %w", ErrParse, err)
		}
	}
	if node, ok := merged.nodes[keyAdmin]; ok {
		out.Admin = &admin.Config{}
		if err := node.Decode(out.Admin); err != nil {
			return nil, fmt.Errorf("config: decode admin: %w: %w", ErrParse, err)
		}
	}
	if node, ok := merged.nodes[keyTelemetry]; ok {
		if err := node.Decode(&out.Telemetry); err != nil {
			return nil, fmt.Errorf("config: decode telemetry: %w: %w", ErrParse, err)
		}
	}
	return out, nil
}

// Validate enforces cross-block invariants — every api_key references a
// known configuration, every provider with prefix_required has a prefix,
// and no two endpoints can claim the same fully-resolved route path.
//
// Returns the first violation as a wrapped sentinel error.
func (r *ResolvedConfig) Validate() error {
	if r.Admin != nil {
		if err := r.Admin.Validate(); err != nil {
			return fmt.Errorf("config: %w", err)
		}
	}
	if len(r.Configurations) == 0 {
		return fmt.Errorf("config: validate: %w", ErrNoConfigurations)
	}

	if err := r.validateLibraries(); err != nil {
		return err
	}

	if err := r.validateConnectors(); err != nil {
		return err
	}

	for i, key := range r.APIKeys {
		if _, ok := r.Configurations[key.Configuration]; !ok {
			return fmt.Errorf(
				"config: api_keys[%d] %q: %w (name=%q)",
				i, key.Configuration, ErrUnknownConfiguration, key.Name,
			)
		}
	}

	seen := make(map[string]Route)
	providerNames := make([]string, 0, len(r.Providers))
	for name := range r.Providers {
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)
	for _, providerName := range providerNames {
		p := r.Providers[providerName]
		if p.PrefixRequired && p.Prefix == "" {
			return fmt.Errorf("config: provider %q: %w", providerName, ErrPrefixRequiredEmpty)
		}
		if err := validateAuthOverride(p.AuthHeader, p.AuthFormat); err != nil {
			return fmt.Errorf("config: provider %q: %w", providerName, err)
		}
		endpointNames := make([]string, 0, len(p.Endpoints))
		for name := range p.Endpoints {
			endpointNames = append(endpointNames, name)
		}
		sort.Strings(endpointNames)
		for _, endpointName := range endpointNames {
			e := p.Endpoints[endpointName]
			if err := validateAuthOverride(e.AuthHeader, e.AuthFormat); err != nil {
				return fmt.Errorf("config: provider %q endpoint %q: %w", providerName, endpointName, err)
			}
			for _, route := range emitRoutes(providerName, endpointName, p, e) {
				if prev, dup := seen[route.Path]; dup {
					return fmt.Errorf(
						"config: route %q claimed by %s.%s and %s.%s: %w",
						route.Path, prev.Provider, prev.Endpoint, providerName, endpointName, ErrPathCollision,
					)
				}
				seen[route.Path] = Route{Provider: providerName, Endpoint: endpointName}
			}
		}
	}

	return nil
}

// emittedRoute is one fully-resolved RouteIndex entry for a
// (provider, endpoint, accepted_path) triple.
type emittedRoute struct {
	// Path is the prefixed gateway-side path the router matches against.
	Path string

	// AcceptedPath is the un-prefixed accepted_paths value the route was
	// emitted from. Carried through to Route so the destination builder
	// can mirror it as the upstream path template for endpoints that
	// declare no explicit Path.
	AcceptedPath string

	Provider string
	Endpoint string
}

// emitRoutes expands a (provider, endpoint) pair into every accepted_path it
// claims, generating both the prefixed and bare forms unless prefix_required
// pins it to prefixed-only. An endpoint may set PrefixOptional: true to
// escape the provider's prefix_required for its own accepted_paths only —
// the bare form emits alongside the prefixed one regardless of the
// provider-level setting. This is what lets Anthropic expose its native
// /v1/messages at the gateway root while keeping /anthropic/v1/chat/completions
// and /anthropic/v1/models behind the prefix (where they'd collide with
// openai's bare claims on the same paths).
func emitRoutes(providerName, endpointName string, p contractsconfig.Provider, e contractsconfig.Endpoint) []emittedRoute {
	out := make([]emittedRoute, 0, len(e.AcceptedPaths)*2)
	bareEmits := !p.PrefixRequired || e.PrefixOptional
	for _, ap := range e.AcceptedPaths {
		if p.Prefix != "" {
			out = append(out, emittedRoute{
				Path:         "/" + p.Prefix + ap,
				AcceptedPath: ap,
				Provider:     providerName,
				Endpoint:     endpointName,
			})
		}
		if bareEmits {
			out = append(out, emittedRoute{
				Path:         ap,
				AcceptedPath: ap,
				Provider:     providerName,
				Endpoint:     endpointName,
			})
		}
	}
	return out
}

// RevalidateAndIndex runs Validate followed by buildIndexes on r — the
// same finalization sequence Load performs after decode. The admin-
// write path calls this on a cloned snapshot before persisting and
// publishing via Store.Replace, so the index tables a swap publishes
// are always consistent with the validated structural state.
func (r *ResolvedConfig) RevalidateAndIndex() error {
	if err := r.Validate(); err != nil {
		return err
	}
	r.buildIndexes()
	return nil
}

func (r *ResolvedConfig) buildIndexes() {
	r.SecretIndex = make(map[string]*contractsconfig.APIKey, len(r.APIKeys))
	for i := range r.APIKeys {
		key := &r.APIKeys[i]
		r.SecretIndex[key.Secret] = key
	}

	r.ConfigurationIndex = make(map[string]*contractsconfig.Configuration, len(r.Configurations))
	for name, cfg := range r.Configurations {
		entry := cfg
		r.ConfigurationIndex[name] = &entry
	}

	r.RouteIndex = make(map[string]Route)
	for providerName, p := range r.Providers {
		for endpointName, e := range p.Endpoints {
			for _, route := range emitRoutes(providerName, endpointName, p, e) {
				r.RouteIndex[route.Path] = Route{
					Provider:     route.Provider,
					Endpoint:     route.Endpoint,
					AcceptedPath: route.AcceptedPath,
				}
			}
		}
	}

	r.RuleIndex = make(map[string]*rulescontract.RuleContract, len(r.Rules))
	for i := range r.Rules {
		rule := &r.Rules[i]
		r.RuleIndex[rule.Name] = rule
	}

	// Resolve each configuration's RuleNames into a priority-sorted slice
	// of pointer-stable rule references. Unknown names are caught by
	// validateLibraries before this runs; skip silently here rather than
	// duplicate the error surface.
	r.PerConfigurationRules = make(map[string][]*rulescontract.RuleContract, len(r.Configurations))
	for name, cfg := range r.Configurations {
		if len(cfg.RuleNames) == 0 {
			continue
		}
		rules := make([]*rulescontract.RuleContract, 0, len(cfg.RuleNames))
		for _, ruleName := range cfg.RuleNames {
			if rule, ok := r.RuleIndex[ruleName]; ok {
				rules = append(rules, rule)
			}
		}
		// Evaluation order = YAML list order. No priority sort.
		r.PerConfigurationRules[name] = rules
	}

	r.ResilienceIndex = make(map[string]*resilience.ResilienceConfig, len(r.ResiliencePolicies))
	for i := range r.ResiliencePolicies {
		pol := &r.ResiliencePolicies[i]
		r.ResilienceIndex[pol.Name] = pol
	}

	r.ConnectorIndex = make(map[string]*contractsconfig.Connector, len(r.Connectors))
	for i := range r.Connectors {
		c := &r.Connectors[i]
		r.ConnectorIndex[c.Name] = c
	}
}

// validateLibraries enforces uniqueness within the top-level rules and
// resilience_policies libraries, runs each entry's own Validate, and confirms
// every Configuration reference resolves to a library entry.
//
// The library checks live here (rather than inline in Validate) because they
// pivot on cross-block invariants: uniqueness within the library plus
// reference integrity from Configurations. Splitting them out keeps the main
// Validate readable.
func (r *ResolvedConfig) validateLibraries() error {
	ruleNames := make(map[string]int, len(r.Rules))
	ruleIDs := make(map[string]int)
	for i := range r.Rules {
		rule := &r.Rules[i]
		if prev, dup := ruleNames[rule.Name]; dup {
			return fmt.Errorf("config: rules[%d] and rules[%d] name=%q: %w", prev, i, rule.Name, ErrDuplicateRuleName)
		}
		ruleNames[rule.Name] = i
		if rule.ID != nil {
			id := rule.ID.String()
			if prev, dup := ruleIDs[id]; dup {
				return fmt.Errorf("config: rules[%d] and rules[%d] id=%s: %w", prev, i, id, ErrDuplicateRuleID)
			}
			ruleIDs[id] = i
		}
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("config: rules[%d]: %w", i, err)
		}
	}

	policyNames := make(map[string]int, len(r.ResiliencePolicies))
	policyIDs := make(map[string]int)
	for i := range r.ResiliencePolicies {
		pol := &r.ResiliencePolicies[i]
		if prev, dup := policyNames[pol.Name]; dup {
			return fmt.Errorf("config: resilience_policies[%d] and resilience_policies[%d] name=%q: %w", prev, i, pol.Name, ErrDuplicateResilienceName)
		}
		policyNames[pol.Name] = i
		if pol.ID != nil {
			id := pol.ID.String()
			if prev, dup := policyIDs[id]; dup {
				return fmt.Errorf("config: resilience_policies[%d] and resilience_policies[%d] id=%s: %w", prev, i, id, ErrDuplicateResilienceID)
			}
			policyIDs[id] = i
		}
		if err := pol.Validate(); err != nil {
			return fmt.Errorf("config: resilience_policies[%d]: %w", i, err)
		}
	}

	for name, cfg := range r.Configurations {
		for _, ruleName := range cfg.RuleNames {
			if _, ok := ruleNames[ruleName]; !ok {
				return fmt.Errorf("config: configuration %q rule_names %q: %w", name, ruleName, ErrUnknownRuleName)
			}
		}
		if cfg.ResilienceName != nil {
			polIdx, ok := policyNames[*cfg.ResilienceName]
			if !ok {
				return fmt.Errorf("config: configuration %q resilience_name %q: %w", name, *cfg.ResilienceName, ErrUnknownResilienceName)
			}
			pol := &r.ResiliencePolicies[polIdx]
			for ti, target := range pol.Targets {
				if _, ok := cfg.UpstreamCredentials[target.Provider]; !ok {
					return fmt.Errorf("config: configuration %q resilience_name %q targets[%d] %q provider %q: %w",
						name, *cfg.ResilienceName, ti, target.Name, target.Provider, ErrTargetProviderMissingCredential)
				}
			}
		}
	}

	// Cross-validate every useResiliencePolicy rule action against the
	// resilience_policies library so unknown policy names are a
	// startup error, not a silent runtime miss.
	for i := range r.Rules {
		rule := &r.Rules[i]
		for ai, act := range rule.Actions {
			use, ok := act.(*rulescontract.UseResiliencePolicyAction)
			if !ok {
				continue
			}
			name := strings.TrimSpace(use.PolicyName)
			if name == "" {
				// Empty PolicyName explicitly clears state.PolicyRef
				// at runtime; nothing to validate against the library.
				continue
			}
			if _, ok := policyNames[name]; !ok {
				return fmt.Errorf("config: rules[%d] %q actions[%d] useResiliencePolicy %q: %w",
					i, rule.Name, ai, name, ErrUnknownResilienceName)
			}
		}
	}
	return nil
}

// validateConnectors enforces uniqueness within the top-level connectors
// block, runs each connector's own Validate, and confirms every
// Configuration's ConnectorBinding.Connector resolves to a defined
// connector.
func (r *ResolvedConfig) validateConnectors() error {
	names := make(map[string]int, len(r.Connectors))
	for i := range r.Connectors {
		c := &r.Connectors[i]
		if prev, dup := names[c.Name]; dup {
			return fmt.Errorf("config: connectors[%d] and connectors[%d] name=%q: %w",
				prev, i, c.Name, ErrDuplicateConnectorName)
		}
		names[c.Name] = i
		if err := c.Validate(); err != nil {
			return fmt.Errorf("config: connectors[%d]: %w", i, err)
		}
	}

	configNames := make([]string, 0, len(r.Configurations))
	for name := range r.Configurations {
		configNames = append(configNames, name)
	}
	sort.Strings(configNames)
	for _, configName := range configNames {
		cfg := r.Configurations[configName]
		for bi := range cfg.ConnectorBindings {
			b := &cfg.ConnectorBindings[bi]
			if err := b.Validate(); err != nil {
				return fmt.Errorf("config: configurations[%q].connector_bindings[%d]: %w",
					configName, bi, err)
			}
			if _, ok := names[b.Connector]; !ok {
				return fmt.Errorf("config: configurations[%q].connector_bindings[%d] connector=%q: %w",
					configName, bi, b.Connector, ErrUnknownConnectorReference)
			}
		}
	}
	return nil
}

// authFormatPlaceholder is the literal substring the auth_format
// validator and the destination builder both look for. Kept here so the
// two sites cannot drift.
const authFormatPlaceholder = "{key}"

// validateAuthOverride enforces the auth_header / auth_format invariants
// the destination builder relies on: setting a format without a header
// would be silently ignored at runtime, so we reject it at load time; and
// every format must carry exactly one {key} placeholder so the runtime
// substitution is unambiguous.
func validateAuthOverride(header, format string) error {
	if format == "" {
		return nil
	}
	if header == "" {
		return ErrAuthFormatWithoutHeader
	}
	if strings.Count(format, authFormatPlaceholder) != 1 {
		return ErrInvalidAuthFormat
	}
	return nil
}
