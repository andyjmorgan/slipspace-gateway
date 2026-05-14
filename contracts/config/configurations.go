package config

// ConfigurationsConfig is the merged `configurations` block — a map from
// configuration name to its policy bundle.
type ConfigurationsConfig map[string]Configuration

// Configuration is a reusable policy bundle. Rules and resilience are attached
// by name from the top-level `rules:` and `resilience_policies:` libraries
// declared alongside the configurations in `policy.yaml`.
type Configuration struct {
	// AllowedEndpoints is the list of "<provider>.<endpoint>" identifiers a
	// request resolved to this Configuration is permitted to call. Anything
	// else gets 403'd before forwarding.
	AllowedEndpoints []string `yaml:"allowed_endpoints" json:"allowed_endpoints"`

	// UpstreamCredentials maps provider name to the upstream API key the
	// gateway substitutes on the way out. Populated for managed-mode auth;
	// passthrough mode leaves the client's Authorization header intact.
	UpstreamCredentials map[string]string `yaml:"upstream_credentials,omitempty" json:"upstream_credentials,omitempty"`

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
