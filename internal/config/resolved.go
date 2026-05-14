// Package config loads, validates, and indexes the on-disk YAML configuration
// tree for the Sluice gateway.
package config

import (
	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
)

// ResolvedConfig is the merged, validated, indexed runtime view of the
// configuration directory.
//
// The first four fields mirror the top-level YAML blocks; the trailing
// *Index fields are the lookup tables Validate built so request-path code
// can hit them with a single map read instead of scanning slices.
type ResolvedConfig struct {
	// Gateway carries the top-level `gateway` block (bind addr, reporting,
	// resilience defaults). Always populated, even when the YAML omits it —
	// the zero value is the default policy.
	Gateway contractsconfig.GatewayConfig

	// Providers is the `providers` block keyed by provider name (openai,
	// anthropic, gemini, ...). Holds endpoint definitions, prefixes, and
	// upstream base URLs.
	Providers contractsconfig.ProvidersConfig

	// Configurations is the `configurations` block keyed by configuration
	// name. Each entry bundles allowed_endpoints, rules, resilience, and
	// upstream credentials.
	Configurations contractsconfig.ConfigurationsConfig

	// APIKeys is the `api_keys` block as authored — a flat slice, kept in
	// authoring order. Lookups use SecretIndex; this slice exists for
	// enumeration (admin listing, audit).
	APIKeys contractsconfig.APIKeysConfig

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
