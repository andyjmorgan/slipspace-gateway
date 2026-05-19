package config

// ProvidersConfig is the merged `providers` block — a map from provider name
// to the routes the gateway exposes for that provider.
type ProvidersConfig map[string]Provider

// Provider is a single upstream provider's base URL and endpoint catalogue.
type Provider struct {
	// BaseURL is the upstream provider's root (e.g.,
	// "https://api.openai.com"); the forwarder appends each Endpoint.Path to
	// it when proxying.
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

	// RequiredHeaders are headers the gateway injects on every forwarded
	// request to this provider (e.g., "anthropic-version: 2023-06-01").
	RequiredHeaders map[string]string `yaml:"required_headers,omitempty" json:"required_headers,omitempty"`

	// AuthHeader, when non-empty, overrides the outgoing HTTP header name
	// into which managed-mode credentials are injected for every endpoint
	// of this provider. Empty defers to the per-endpoint override or, if
	// that is also empty, to the historical provider-native default
	// (Authorization for OpenAI, x-api-key for Anthropic, x-goog-api-key
	// for Gemini).
	AuthHeader string `yaml:"auth_header,omitempty" json:"auth_header,omitempty"`

	// AuthFormat templates the credential value injected into AuthHeader.
	// Supports a single `{key}` placeholder substituted with the resolved
	// upstream credential. Only consulted when an override AuthHeader is
	// in effect; with the default header path the per-provider format is
	// used. Empty (with an override AuthHeader set) renders the raw
	// credential.
	AuthFormat string `yaml:"auth_format,omitempty" json:"auth_format,omitempty"`

	// Endpoints is the provider's endpoint catalogue keyed by logical name
	// (e.g., "chat_completions", "messages"). The key seeds the route index
	// and shows up in telemetry as the resolved endpoint name.
	Endpoints map[string]Endpoint `yaml:"endpoints" json:"endpoints"`
}

// Endpoint describes a single provider endpoint the gateway accepts. The
// `accepted_paths` list seeds the route index.
type Endpoint struct {
	// Path is the upstream path the gateway forwards to, appended to
	// Provider.BaseURL.
	Path string `yaml:"path" json:"path"`

	// Method is the set of HTTP methods the endpoint accepts.
	Method []string `yaml:"method" json:"method"`

	// AcceptedPaths is the inbound path patterns the gateway matches against
	// the client request. Routing builds its index from this list.
	AcceptedPaths []string `yaml:"accepted_paths" json:"accepted_paths"`

	// AcceptsStreaming records whether the endpoint supports SSE streaming
	// responses; affects how the forwarder handles the response body.
	AcceptsStreaming bool `yaml:"accepts_streaming,omitempty" json:"accepts_streaming,omitempty"`

	// RequestKind names the typed request shape (e.g., "openai.chat",
	// "anthropic.messages") so middleware can deserialise the body into the
	// right model type with DynamicProperties preservation.
	RequestKind string `yaml:"request_kind" json:"request_kind"`

	// AuthHeader, when non-empty, overrides the outgoing HTTP header name
	// into which managed-mode credentials are injected for this endpoint.
	// Empty defers to the provider-level override or, if that is also
	// empty, to the historical provider-native default. The override is
	// load-bearing for OpenAI-compat surfaces on Anthropic and Gemini —
	// both want Authorization: Bearer rather than the provider's native
	// header.
	AuthHeader string `yaml:"auth_header,omitempty" json:"auth_header,omitempty"`

	// AuthFormat templates the credential value injected into AuthHeader.
	// Supports a single `{key}` placeholder substituted with the resolved
	// upstream credential. Only consulted when an override AuthHeader is
	// in effect; with the default header path the per-provider format is
	// used. Empty (with an override AuthHeader set) renders the raw
	// credential.
	AuthFormat string `yaml:"auth_format,omitempty" json:"auth_format,omitempty"`

	// PrefixOptional, when true, escapes Provider.PrefixRequired for
	// this single endpoint — bare AcceptedPaths emit as routes even
	// though the rest of the provider's endpoints stay prefix-only.
	// The mechanism is provider-agnostic: any prefix-required provider
	// can flip individual endpoints back to bare-routable to expose a
	// native upstream path at the gateway root while keeping
	// collision-prone endpoints behind the prefix. Anthropic's
	// `/v1/messages` is the canonical example: bare so vanilla
	// Anthropic SDKs work pointed at the gateway root, while the
	// OpenAI-compat `/v1/chat/completions` on the same provider stays
	// prefix-only to avoid colliding with openai. No effect when
	// Provider.PrefixRequired is false (the endpoint already emits
	// both forms).
	PrefixOptional bool `yaml:"prefix_optional,omitempty" json:"prefix_optional,omitempty"`
}
