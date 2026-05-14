package config

import "gopkg.in/yaml.v3"

// ConfigurationsConfig is the merged `configurations` block — a map from
// configuration name to its policy bundle.
type ConfigurationsConfig map[string]Configuration

// Configuration is a reusable policy bundle. v1.0 accepts Rules and Resilience
// in the YAML but does not evaluate them; the raw yaml.Node trees are preserved
// so v1.1 can decode them without a schema migration.
type Configuration struct {
	AllowedEndpoints []string `yaml:"allowed_endpoints" json:"allowed_endpoints"`

	UpstreamCredentials map[string]string `yaml:"upstream_credentials,omitempty" json:"upstream_credentials,omitempty"`

	Rules []yaml.Node `yaml:"rules,omitempty" json:"-"`

	Resilience *yaml.Node `yaml:"resilience,omitempty" json:"-"`

	Tags map[string]string `yaml:"tags,omitempty" json:"tags,omitempty"`
}
