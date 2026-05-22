package config

// ConfigurationsConfig is the merged `configurations` block — a map from
// configuration name to its policy bundle.
type ConfigurationsConfig map[string]Configuration

// Configuration is a reusable policy bundle. Rules and resilience are attached
// by name from the top-level `rules:` and `resilience_policies:` libraries
// declared alongside the configurations in `policy.yaml`.
//
// Per-endpoint authorization is implicit: managed mode can only forward to
// providers that have an entry in UpstreamCredentials (no credential, no
// forward); passthrough mode is gated by the upstream's own auth on the
// BYOK token the client carries. A per-endpoint allow-list adds no real
// authorization on top of that, so the field is intentionally absent.
type Configuration struct {
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

	// ConnectorBindings attaches one or more connectors to this
	// configuration with per-binding sampling / filter / size-cap
	// overrides. An empty slice means "no capture" — the body-capture
	// middleware short-circuits when no bindings are present.
	ConnectorBindings []ConnectorBinding `yaml:"connector_bindings,omitempty" json:"connector_bindings,omitempty"`
}
