package config

// APIKeysConfig is the merged `api_keys` block — a flat list of gateway-issued
// keys, each pointing at a named Configuration.
type APIKeysConfig []APIKey

// APIKey is a single gateway-issued credential.
type APIKey struct {
	// Secret is the bearer token clients present (conventionally prefixed
	// "sk_live_…" for production keys or "sk_dev_…" for development keys).
	// Authentication compares this in constant time.
	Secret string `yaml:"secret" json:"secret"`

	// Name is a human-readable label surfaced in logs and reporting events;
	// it carries no auth meaning.
	Name string `yaml:"name" json:"name"`

	// Configuration is the name of the Configuration this key resolves to.
	// Many keys may share one Configuration.
	Configuration string `yaml:"configuration" json:"configuration"`

	// Enabled toggles the key without removing it. A disabled key
	// authenticates structurally but is rejected before forwarding.
	Enabled bool `yaml:"enabled" json:"enabled"`
}
