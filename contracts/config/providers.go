package config

// ProvidersConfig is the merged `providers` block — a map from provider name
// to the routes the gateway exposes for that provider.
type ProvidersConfig map[string]Provider

// Provider is a single upstream provider's base URL and endpoint catalogue.
type Provider struct {
	BaseURL string `yaml:"base_url" json:"base_url"`

	// Prefix is the URL path segment that disambiguates this provider when
	// multiple providers share an `accepted_paths` entry (e.g., /v1/models).
	// Empty means the provider only matches bare paths (legacy single-provider
	// deploys).
	Prefix string `yaml:"prefix,omitempty" json:"prefix,omitempty"`

	// PrefixRequired controls whether the prefix is mandatory. When false
	// (the default), the provider matches BOTH `/<prefix><accepted_path>` and
	// `<accepted_path>` (with the latter being the "default" provider for that
	// path). When true, only the prefixed form matches.
	PrefixRequired bool `yaml:"prefix_required,omitempty" json:"prefix_required,omitempty"`

	RequiredHeaders map[string]string `yaml:"required_headers,omitempty" json:"required_headers,omitempty"`

	Endpoints map[string]Endpoint `yaml:"endpoints" json:"endpoints"`
}

// Endpoint describes a single provider endpoint the gateway accepts. The
// `accepted_paths` list seeds the route index.
type Endpoint struct {
	Path string `yaml:"path" json:"path"`

	Method []string `yaml:"method" json:"method"`

	AcceptedPaths []string `yaml:"accepted_paths" json:"accepted_paths"`

	AcceptsStreaming bool `yaml:"accepts_streaming,omitempty" json:"accepts_streaming,omitempty"`

	RequestKind string `yaml:"request_kind" json:"request_kind"`
}
