package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/andyjmorgan/sluice-gateway/contracts/admin"
	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	rulescontract "github.com/andyjmorgan/sluice-gateway/contracts/rules"
)

// ResolvedConfig is the merged, validated, indexed runtime view of a v2
// configuration directory (providers + bindings). It is the v2 analogue of
// ResolvedConfig; the data plane reads it through config.Store the same way.
//
// Routing is config data here: a request resolves its destination by selecting
// a binding on the owning configuration (see internal/selection), so there is
// no RouteIndex — the inbound path maps to a protocol and the configuration's
// bindings do the rest.
type ResolvedConfig struct {
	// Providers is the shared connection catalogue keyed by provider name.
	Providers contractsconfig.ProvidersConfig

	// Groups is the shared resilience-group catalogue keyed by group name.
	Groups contractsconfig.GroupsConfig

	// Configurations is the policy bundles keyed by configuration name.
	Configurations map[string]contractsconfig.Configuration

	// APIKeys is the `api_keys` block in authoring order. Lookups use
	// SecretIndex.
	APIKeys contractsconfig.APIKeysConfig

	// Rules is the top-level transform-rule library (routing actions have been
	// retired into bindings/providers/groups).
	Rules []rulescontract.RuleContract

	// Connectors is the top-level connector catalogue (unchanged from v1).
	Connectors contractsconfig.ConnectorsConfig

	// Admin is the `admin` block; nil when absent (console off).
	Admin *admin.Config

	// Telemetry is the `telemetry` block; zero value yields built-in defaults.
	Telemetry contractsconfig.Telemetry

	// SecretIndex maps an API-key secret to its owning APIKey entry.
	SecretIndex map[string]*contractsconfig.APIKey

	// ConfigurationIndex maps configuration name to a pointer-stable copy so
	// downstream code may retain it across reload boundaries.
	ConfigurationIndex map[string]*contractsconfig.Configuration

	// RuleIndex maps rule name to a pointer into Rules.
	RuleIndex map[string]*rulescontract.RuleContract

	// PerConfigurationRules maps configuration name to its resolved transform
	// rules in RuleNames order (pointer-stable into Rules).
	PerConfigurationRules map[string][]*rulescontract.RuleContract

	// ConnectorIndex maps connector name to a pointer into Connectors.
	ConnectorIndex map[string]*contractsconfig.Connector

	// SourceFiles records which file each top-level block was loaded from
	// (block name -> filename, e.g. "providers" -> "providers.yaml"). The admin
	// write path uses it to persist a mutated block back to the file it came
	// from rather than a fixed filename, so an operator's chosen layout
	// survives a round-trip and a co-located block is never dropped. Blocks
	// introduced through the API with no recorded origin fall back to a
	// canonical default file (see writer_v2.go).
	SourceFiles map[string]string
}

// v2Doc is the decode target for one YAML file's top-level blocks. Files are
// merged by top-level key; a key set by two files is a duplicate-key error.
type v2Doc struct {
	Providers      contractsconfig.ProvidersConfig          `yaml:"providers"`
	Groups         contractsconfig.GroupsConfig             `yaml:"groups"`
	Configurations map[string]contractsconfig.Configuration `yaml:"configurations"`
	APIKeys        contractsconfig.APIKeysConfig            `yaml:"api_keys"`
	Connectors     contractsconfig.ConnectorsConfig         `yaml:"connectors"`
	Rules          []rulescontract.RuleContract             `yaml:"rules"`
	Admin          *admin.Config                            `yaml:"admin"`
	Telemetry      *contractsconfig.Telemetry               `yaml:"telemetry"`

	// LegacyBackends captures the pre-rename `backends:` key solely so Load can
	// reject it with a clear message — the block is `providers:` now and the cut
	// is hard. A present key decodes to a non-zero Node (Kind != 0); an absent
	// one stays zero. Exported because yaml.v3 only populates exported fields.
	LegacyBackends yaml.Node `yaml:"backends"`
}

// Load reads the v2 YAML configuration directory at dir and returns the
// merged, validated, indexed runtime view. Every *.yaml file is read; the
// top-level blocks are merged by key and a key set by two files is rejected.
func Load(ctx context.Context, dir string) (*ResolvedConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("config: loadv2 %q: %w", dir, err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("config: loadv2 read dir %q: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		names = append(names, e.Name())
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("config: loadv2 %q: %w", dir, ErrEmptyDirectory)
	}
	sort.Strings(names)

	r := &ResolvedConfig{}
	seen := map[string]string{} // top-level block -> filename that set it
	for _, name := range names {
		raw, rerr := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // dir is operator-trusted config root
		if rerr != nil {
			return nil, fmt.Errorf("config: loadv2 read %q: %w", name, rerr)
		}
		var doc v2Doc
		if uerr := yaml.Unmarshal(raw, &doc); uerr != nil {
			return nil, fmt.Errorf("config: loadv2 parse %q: %w", name, uerr)
		}
		if doc.LegacyBackends.Kind != 0 {
			return nil, fmt.Errorf("config: loadv2 %q: %w", name, ErrLegacyProvidersKey)
		}
		if merr := r.mergeDoc(name, &doc, seen); merr != nil {
			return nil, merr
		}
	}

	r.SourceFiles = seen

	if verr := r.Validate(); verr != nil {
		return nil, verr
	}
	r.buildIndexes()
	return r, nil
}

// mergeDoc folds one file's blocks into r, rejecting a top-level block set by
// more than one file.
func (r *ResolvedConfig) mergeDoc(file string, doc *v2Doc, seen map[string]string) error {
	claim := func(block string, set bool) error {
		if !set {
			return nil
		}
		if prev, dup := seen[block]; dup {
			return fmt.Errorf("config: loadv2 %q sets %q already set by %q: %w", file, block, prev, ErrDuplicateKey)
		}
		seen[block] = file
		return nil
	}
	if err := claim("providers", len(doc.Providers) > 0); err != nil {
		return err
	}
	if err := claim("groups", len(doc.Groups) > 0); err != nil {
		return err
	}
	if err := claim("configurations", len(doc.Configurations) > 0); err != nil {
		return err
	}
	if err := claim("api_keys", len(doc.APIKeys) > 0); err != nil {
		return err
	}
	if err := claim("connectors", len(doc.Connectors) > 0); err != nil {
		return err
	}
	if err := claim("rules", len(doc.Rules) > 0); err != nil {
		return err
	}
	if err := claim("admin", doc.Admin != nil); err != nil {
		return err
	}
	if err := claim("telemetry", doc.Telemetry != nil); err != nil {
		return err
	}

	if len(doc.Providers) > 0 {
		r.Providers = doc.Providers
	}
	if len(doc.Groups) > 0 {
		r.Groups = doc.Groups
	}
	if len(doc.Configurations) > 0 {
		r.Configurations = doc.Configurations
	}
	if len(doc.APIKeys) > 0 {
		r.APIKeys = doc.APIKeys
	}
	if len(doc.Connectors) > 0 {
		r.Connectors = doc.Connectors
	}
	if len(doc.Rules) > 0 {
		r.Rules = doc.Rules
	}
	if doc.Admin != nil {
		r.Admin = doc.Admin
	}
	if doc.Telemetry != nil {
		r.Telemetry = *doc.Telemetry
	}
	return nil
}

// buildIndexes constructs the lookup tables the data plane reads per request.
// Mirrors ResolvedConfig.buildIndexes; called after Validate.
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

	r.RuleIndex = make(map[string]*rulescontract.RuleContract, len(r.Rules))
	for i := range r.Rules {
		rule := &r.Rules[i]
		r.RuleIndex[rule.Name] = rule
	}

	r.PerConfigurationRules = make(map[string][]*rulescontract.RuleContract, len(r.Configurations))
	for name, cfg := range r.Configurations {
		if len(cfg.RuleNames) == 0 {
			continue
		}
		resolved := make([]*rulescontract.RuleContract, 0, len(cfg.RuleNames))
		for _, ruleName := range cfg.RuleNames {
			if rule, ok := r.RuleIndex[ruleName]; ok {
				resolved = append(resolved, rule)
			}
		}
		r.PerConfigurationRules[name] = resolved
	}

	r.ConnectorIndex = make(map[string]*contractsconfig.Connector, len(r.Connectors))
	for i := range r.Connectors {
		c := &r.Connectors[i]
		r.ConnectorIndex[c.Name] = c
	}
}
