package config

// APIKeysConfig is the merged `api_keys` block — a flat list of gateway-issued
// keys, each pointing at a named Configuration.
type APIKeysConfig []APIKey

// APIKey is a single gateway-issued credential.
type APIKey struct {
	Secret string `yaml:"secret" json:"secret"`

	Name string `yaml:"name" json:"name"`

	Configuration string `yaml:"configuration" json:"configuration"`

	Enabled bool `yaml:"enabled" json:"enabled"`
}
