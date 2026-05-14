// Package config loads, validates, and indexes the on-disk YAML configuration
// tree for the Sluice gateway.
package config

import (
	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
)

// ResolvedConfig is the merged, validated, indexed runtime view of the
// configuration directory.
type ResolvedConfig struct {
	Gateway contractsconfig.GatewayConfig

	Providers contractsconfig.ProvidersConfig

	Configurations contractsconfig.ConfigurationsConfig

	APIKeys contractsconfig.APIKeysConfig

	SecretIndex map[string]*contractsconfig.APIKey

	ConfigurationIndex map[string]*contractsconfig.Configuration

	RouteIndex map[string]Route
}

// Route names the (provider, endpoint) pair that owns an accepted_paths entry.
type Route struct {
	Provider string

	Endpoint string
}
