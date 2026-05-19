package auth

import (
	"net/http"
	"strings"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

// Mode is the auth resolution mode for a request.
type Mode string

// Mode values exposed in AuthResult and emitted in structured logs as the
// "mode" field.
const (
	ModeManaged Mode = "managed"

	ModePassthrough Mode = "passthrough"
)

// Inbound header names. X-Sluice-Configuration is the mode discriminator: its
// presence forces passthrough resolution regardless of any credential header
// on the same request. In managed mode (header absent), the Sluice secret is
// discovered from Authorization, x-api-key, or x-goog-api-key in that order —
// the latter two let vanilla provider SDKs work zero-config without forcing
// callers to inject an Authorization header.
const (
	HeaderConfiguration = "X-Sluice-Configuration"
	HeaderAuthorization = "Authorization"

	bearerPrefix = "Bearer "
)

// Provider names that the auth swap recognizes. Anything else is treated as a
// generic Bearer swap.
const (
	providerOpenAI    = "openai"
	providerAnthropic = "anthropic"
	providerGemini    = "gemini"
)

// Upstream credential header names per provider convention.
const (
	headerAnthropicAPIKey = "x-api-key"

	headerGeminiAPIKey = "x-goog-api-key" //nolint:gosec // header name, not a credential
)

// AuthResult is the resolved auth decision for a single request, stashed
// on the request context by HTTPHandler and read by downstream middleware
// (bodycapture, forwarder).
type AuthResult struct {
	// Mode is which auth scheme matched: ModeManaged when a Sluice-issued
	// bearer was presented, ModePassthrough when the X-Sluice-Configuration
	// header selected the policy.
	Mode Mode

	// APIKey is the matched managed-mode API key, or nil in passthrough
	// mode (which has no API key — the client uses its own upstream
	// credential). Downstream code must nil-check before dereferencing.
	APIKey *contractsconfig.APIKey

	// Configuration is the resolved policy bundle. Nil only when
	// resolution failed before configuration lookup.
	Configuration *contractsconfig.Configuration

	// ConfigurationName is the name the policy was looked up by — the
	// X-Sluice-Configuration value (passthrough) or APIKey.Configuration
	// (managed). Retained for structured logging even when Configuration
	// is nil.
	ConfigurationName string

	// Provider is the upstream provider name routed to (openai, anthropic,
	// gemini, ...). Injected from the routing decision; not resolved by
	// auth.
	Provider string

	// Endpoint is the upstream endpoint name routed to. Injected from the
	// routing decision; not resolved by auth.
	Endpoint string

	// DropHeaders names inbound headers the forwarder must strip before
	// sending upstream. Auth always emits X-Sluice-Configuration here —
	// it is policy-routing metadata, not a credential. The destination
	// builder layers additional drops on top based on the post-rule
	// provider + credential decision (e.g. dropping the inbound
	// Authorization when managed mode is forwarding to a provider that
	// uses a non-Bearer credential header).
	DropHeaders []string
}

// Resolver decides the auth outcome for a request: given inbound headers and
// the routed (provider, endpoint) pair, it returns an AuthResult plus the
// header swap to apply, or a typed sentinel error.
//
// Resolver contains no HTTP serving concerns and is safe to call from tests
// without standing up a server. HTTPHandler is the http.Handler adapter.
type Resolver struct {
	cfg *config.ResolvedConfig
}

// NewResolver constructs a Resolver bound to cfg.
//
// The resolver retains cfg by pointer but reads only SecretIndex and
// ConfigurationIndex; it does not mutate either. Callers that swap the
// underlying config must build a fresh Resolver.
func NewResolver(cfg *config.ResolvedConfig) *Resolver {
	return &Resolver{cfg: cfg}
}

// Resolve decides the auth mode and returns the resulting AuthResult plus
// the header swap to apply when forwarding upstream. The X-Sluice-
// Configuration header takes precedence over any bearer token on the same
// request — if present, resolution is always passthrough.
func (r *Resolver) Resolve(headers http.Header, provider, endpoint string) (AuthResult, error) {
	if r == nil || r.cfg == nil {
		return AuthResult{}, ErrUnknownConfiguration
	}

	if configName := strings.TrimSpace(headers.Get(HeaderConfiguration)); configName != "" {
		return r.resolvePassthrough(configName, provider, endpoint)
	}

	return r.resolveManaged(headers, provider, endpoint)
}

func (r *Resolver) resolvePassthrough(configName, provider, endpoint string) (AuthResult, error) {
	cfg, ok := r.cfg.ConfigurationIndex[configName]
	if !ok {
		return AuthResult{}, ErrUnknownConfiguration
	}

	return AuthResult{
		Mode:              ModePassthrough,
		Configuration:     cfg,
		ConfigurationName: configName,
		Provider:          provider,
		Endpoint:          endpoint,
		DropHeaders:       []string{HeaderConfiguration},
	}, nil
}

// managedKeySource names the inbound header a Sluice key was discovered on.
// The forwarder uses this to add the source header to DropHeaders so the
// raw Sluice secret never leaks upstream — the destination builder will
// inject the resolved upstream credential under the per-provider /
// per-endpoint header anyway.
type managedKeySource struct {
	header string
	token  string
}

// discoverManagedKey walks the inbound headers in the order Airia's
// gateway does — Authorization Bearer first (the explicit signal), then
// the provider-native shapes (x-api-key for Anthropic SDKs, x-goog-api-key
// for Gemini SDKs). The first header whose value is a known Sluice secret
// wins. A header may be present with a value that is not in SecretIndex
// (e.g. an OpenAI sk- key supplied by a misconfigured client) — that case
// short-circuits at the first present header and returns ErrUnauthorized,
// it does not fall through to the next header so an attacker cannot
// confuse the resolution by stuffing multiple headers.
func (r *Resolver) discoverManagedKey(headers http.Header) (managedKeySource, bool) {
	if v := headers.Get(HeaderAuthorization); v != "" {
		if token, ok := extractBearer(v); ok {
			return managedKeySource{header: HeaderAuthorization, token: token}, true
		}
	}
	if v := strings.TrimSpace(headers.Get(headerAnthropicAPIKey)); v != "" {
		return managedKeySource{header: headerAnthropicAPIKey, token: v}, true
	}
	if v := strings.TrimSpace(headers.Get(headerGeminiAPIKey)); v != "" {
		return managedKeySource{header: headerGeminiAPIKey, token: v}, true
	}
	return managedKeySource{}, false
}

func (r *Resolver) resolveManaged(headers http.Header, provider, endpoint string) (AuthResult, error) {
	src, ok := r.discoverManagedKey(headers)
	if !ok {
		return AuthResult{Mode: ModeManaged, Provider: provider, Endpoint: endpoint}, ErrUnauthorized
	}

	key, ok := r.cfg.SecretIndex[src.token]
	if !ok || key == nil {
		return AuthResult{Mode: ModeManaged, Provider: provider, Endpoint: endpoint}, ErrUnauthorized
	}

	if !key.Enabled {
		return AuthResult{
			Mode:     ModeManaged,
			APIKey:   key,
			Provider: provider,
			Endpoint: endpoint,
		}, ErrUnauthorized
	}

	cfg, ok := r.cfg.ConfigurationIndex[key.Configuration]
	if !ok {
		return AuthResult{
			Mode:              ModeManaged,
			APIKey:            key,
			ConfigurationName: key.Configuration,
			Provider:          provider,
			Endpoint:          endpoint,
		}, ErrUnknownConfiguration
	}

	drops := []string{HeaderConfiguration}
	// Always drop whichever native header carried the Sluice secret. The
	// destination builder will mint the correct upstream credential header
	// for the resolved (provider, endpoint) pair — leaving the inbound
	// header in place would either leak the Sluice secret upstream
	// (Authorization to OpenAI) or collide with the freshly-injected
	// upstream credential header (x-api-key to Anthropic).
	if src.header != HeaderConfiguration {
		drops = append(drops, src.header)
	}

	return AuthResult{
		Mode:              ModeManaged,
		APIKey:            key,
		Configuration:     cfg,
		ConfigurationName: key.Configuration,
		Provider:          provider,
		Endpoint:          endpoint,
		DropHeaders:       drops,
	}, nil
}

// extractBearer parses an Authorization header value. The match is
// case-insensitive on the scheme name per RFC 7235 §2.1.
func extractBearer(v string) (string, bool) {
	if len(v) < len(bearerPrefix) {
		return "", false
	}
	if !strings.EqualFold(v[:len(bearerPrefix)], bearerPrefix) {
		return "", false
	}
	token := strings.TrimSpace(v[len(bearerPrefix):])
	if token == "" {
		return "", false
	}
	return token, true
}

// UpstreamCredentialHeader returns the (header name, header value)
// pair the gateway uses to convey credential to provider for managed-
// mode requests. Exported so downstream consumers — chiefly the
// rules engine's ChangeApiKey action — can re-mint the credential
// header for a post-rule provider without duplicating the per-
// provider format table.
//
// Unknown providers fall back to a Bearer Authorization swap so a
// rule that retargets an as-yet-unmodelled provider still produces a
// reasonable outgoing shape.
func UpstreamCredentialHeader(provider, credential string) (name string, value string) {
	switch provider {
	case providerAnthropic:
		return headerAnthropicAPIKey, credential
	case providerGemini:
		return headerGeminiAPIKey, credential
	case providerOpenAI:
		return HeaderAuthorization, bearerPrefix + credential
	default:
		return HeaderAuthorization, bearerPrefix + credential
	}
}
