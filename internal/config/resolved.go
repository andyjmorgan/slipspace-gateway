// Package config loads, validates, and indexes the on-disk YAML configuration
// tree for the Sluice gateway, and parses the SLUICE_* env vars that carry
// the per-process server configuration.
package config

import (
	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/contracts/resilience"
	rulescontract "github.com/andyjmorgan/sluice-gateway/contracts/rules"
)

// ResolvedConfig is the merged, validated, indexed runtime view of the
// configuration directory.
//
// The leading fields mirror the top-level YAML blocks under policy.yaml +
// providers.yaml; the trailing *Index fields are the lookup tables Validate
// built so request-path code can hit them with a single map read instead of
// scanning slices. Server-level configuration (bind, drain, NATS, OTel,
// logging, Prometheus) lives on ServerEnv, not here.
type ResolvedConfig struct {
	// Providers is the `providers` block keyed by provider name (openai,
	// anthropic, gemini, ...). Holds endpoint definitions, prefixes, and
	// upstream base URLs.
	Providers contractsconfig.ProvidersConfig

	// Configurations is the `configurations` block keyed by configuration
	// name. Each entry bundles upstream credentials, rule references, and
	// resilience references.
	Configurations contractsconfig.ConfigurationsConfig

	// APIKeys is the `api_keys` block as authored — a flat slice, kept in
	// authoring order. Lookups use SecretIndex; this slice exists for
	// enumeration (admin listing, audit).
	APIKeys contractsconfig.APIKeysConfig

	// Rules is the top-level `rules:` library — every rule definition
	// available for Configurations to reference by name.
	Rules []rulescontract.RuleContract

	// ResiliencePolicies is the top-level `resilience_policies:` library —
	// every named resilience policy available for Configurations to reference.
	ResiliencePolicies []resilience.ResilienceConfig

	// SecretIndex maps an API-key secret string to the owning APIKey entry
	// in APIKeys. Pointers reference the entries in place — the slice's
	// backing array must not be reordered after buildIndexes runs.
	SecretIndex map[string]*contractsconfig.APIKey

	// ConfigurationIndex maps configuration name to a pointer-stable copy
	// of the named Configuration. The pointers are owned by ResolvedConfig
	// (not aliases into the Configurations map) so downstream code may
	// retain them across reload boundaries.
	ConfigurationIndex map[string]*contractsconfig.Configuration

	// RouteIndex maps a fully-resolved URL path (with provider prefix
	// applied where required) to the (provider, endpoint) pair that owns
	// it. Populated by Validate after route emission; routing middleware
	// reads this on every request.
	RouteIndex map[string]Route

	// RuleIndex maps rule name to a pointer into Rules so middleware can
	// resolve a Configuration's RuleNames reference with a single map read.
	RuleIndex map[string]*rulescontract.RuleContract

	// PerConfigurationRules maps configuration name to the configuration's
	// resolved rule slice — pointer-stable references into Rules, sorted by
	// Priority ascending. The order in Configuration.RuleNames is preserved
	// as the tie-breaker when two rules share a priority, so operator
	// authoring order is deterministic.
	//
	// The data-plane rules middleware reads this per request rather than
	// re-resolving and re-sorting on the hot path.
	PerConfigurationRules map[string][]*rulescontract.RuleContract

	// ResilienceIndex maps resilience policy name to a pointer into
	// ResiliencePolicies.
	ResilienceIndex map[string]*resilience.ResilienceConfig
}

// Route names the (provider, endpoint) pair that owns an accepted_paths entry
// in RouteIndex.
type Route struct {
	// Provider is the providers-map key (e.g. "openai").
	Provider string

	// Endpoint is the endpoints-map key under that provider (e.g.
	// "chat_completions").
	Endpoint string
}
