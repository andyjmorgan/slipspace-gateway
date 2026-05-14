package config

import "gopkg.in/yaml.v3"

// ConfigurationsConfig is the merged `configurations` block — a map from
// configuration name to its policy bundle.
type ConfigurationsConfig map[string]Configuration

// Configuration is a reusable policy bundle. v1.0 accepts Rules and Resilience
// in the YAML but does not evaluate them; the raw yaml.Node trees are preserved
// so v1.1 can decode them without a schema migration.
type Configuration struct {
	// AllowedEndpoints is the list of "<provider>.<endpoint>" identifiers a
	// request resolved to this Configuration is permitted to call. Anything
	// else gets 403'd before forwarding.
	AllowedEndpoints []string `yaml:"allowed_endpoints" json:"allowed_endpoints"`

	// UpstreamCredentials maps provider name to the upstream API key the
	// gateway substitutes on the way out. Populated for managed-mode auth;
	// passthrough mode leaves the client's Authorization header intact.
	UpstreamCredentials map[string]string `yaml:"upstream_credentials,omitempty" json:"upstream_credentials,omitempty"`

	// Rules holds the parsed-but-undecoded rule definitions. v1.0 stores them
	// as raw yaml.Node so the shape is locked without committing to a Go
	// schema; v1.1 decodes them through the rules package and flips
	// evaluation on. Superseded by RuleNames + the top-level `rules:` library
	// and scheduled for removal in commit 2 of the three-plane refactor.
	Rules []yaml.Node `yaml:"rules,omitempty" json:"-"`

	// Resilience holds the parsed-but-undecoded resilience policy. Like
	// Rules, it is locked-in-shape for v1.0 and decoded in v1.1. Superseded
	// by ResilienceName + the top-level `resilience_policies:` library and
	// scheduled for removal in commit 2 of the three-plane refactor.
	Resilience *yaml.Node `yaml:"resilience,omitempty" json:"-"`

	// RuleNames lists the names of rules from the top-level `rules:` library
	// that this Configuration applies. Resolved at load time against the
	// library; unknown names are an error.
	RuleNames []string `yaml:"rule_names,omitempty" json:"rule_names,omitempty"`

	// ResilienceName names the resilience policy from the top-level
	// `resilience_policies:` library this Configuration uses. nil means
	// "no resilience policy applied" (single-attempt, no breaker).
	ResilienceName *string `yaml:"resilience_name,omitempty" json:"resilience_name,omitempty"`

	// Tags are arbitrary key/value labels propagated to telemetry and
	// reporting events for this Configuration.
	Tags map[string]string `yaml:"tags,omitempty" json:"tags,omitempty"`
}
