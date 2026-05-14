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

// Inbound header names. The Sluice configuration header triggers passthrough
// mode; if present it wins over any managed bearer token on the same request.
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
	// sending upstream — always includes X-Sluice-Configuration and, for
	// providers with non-bearer credentials, the inbound Authorization
	// header. DropHeaders is applied before SetHeaders, so setting and
	// dropping the same name is a no-op net of the set.
	DropHeaders []string

	// SetHeaders carries the upstream credential headers the forwarder
	// must inject (e.g. Authorization, x-api-key, x-goog-api-key). Empty
	// in passthrough mode — the client's own credential header is
	// forwarded verbatim.
	SetHeaders http.Header

	// AuthHeader names the upstream HTTP header the gateway will set on the
	// outgoing request to carry the resolved credential. For managed mode:
	// "Authorization" for OpenAI, "x-api-key" for Anthropic, "x-goog-api-key"
	// for Gemini. For passthrough mode this is empty — the inbound
	// Authorization is forwarded verbatim by the cmd/gateway layer.
	AuthHeader string
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

	if !endpointAllowed(cfg, provider, endpoint) {
		return AuthResult{
			Mode:              ModePassthrough,
			Configuration:     cfg,
			ConfigurationName: configName,
			Provider:          provider,
			Endpoint:          endpoint,
		}, ErrEndpointNotAllowed
	}

	return AuthResult{
		Mode:              ModePassthrough,
		Configuration:     cfg,
		ConfigurationName: configName,
		Provider:          provider,
		Endpoint:          endpoint,
	}, nil
}

func (r *Resolver) resolveManaged(headers http.Header, provider, endpoint string) (AuthResult, error) {
	token, ok := extractBearer(headers.Get(HeaderAuthorization))
	if !ok {
		return AuthResult{Mode: ModeManaged, Provider: provider, Endpoint: endpoint}, ErrUnauthorized
	}

	key, ok := r.cfg.SecretIndex[token]
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

	if !endpointAllowed(cfg, provider, endpoint) {
		return AuthResult{
			Mode:              ModeManaged,
			APIKey:            key,
			Configuration:     cfg,
			ConfigurationName: key.Configuration,
			Provider:          provider,
			Endpoint:          endpoint,
		}, ErrEndpointNotAllowed
	}

	set, drop, authHeader := authSwap(provider, cfg.UpstreamCredentials[provider])

	return AuthResult{
		Mode:              ModeManaged,
		APIKey:            key,
		Configuration:     cfg,
		ConfigurationName: key.Configuration,
		Provider:          provider,
		Endpoint:          endpoint,
		SetHeaders:        set,
		DropHeaders:       drop,
		AuthHeader:        authHeader,
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

func endpointAllowed(cfg *contractsconfig.Configuration, provider, endpoint string) bool {
	want := provider + "." + endpoint
	for _, allowed := range cfg.AllowedEndpoints {
		if allowed == want {
			return true
		}
	}
	return false
}

// authSwap builds the header set/drop pair the forwarder applies to the
// outgoing managed-mode request, plus the name of the upstream credential
// header for diagnostic logging. The inbound X-Sluice-Configuration header is
// always dropped — it has no meaning upstream and would only be present if the
// client tried to mix modes. For Anthropic / Gemini the inbound Authorization
// header is also dropped because the upstream credential lives on a
// provider-specific header instead.
//
// The third return value is the canonical name of the credential header
// injected into SetHeaders ("Authorization", "x-api-key", "x-goog-api-key").
// Unknown providers fall back to a Bearer Authorization swap; the header name
// reflects that.
func authSwap(provider, credential string) (http.Header, []string, string) {
	set := http.Header{}
	drop := []string{HeaderConfiguration}
	var authHeader string

	switch provider {
	case providerAnthropic:
		set.Set(headerAnthropicAPIKey, credential)
		drop = append(drop, HeaderAuthorization)
		authHeader = headerAnthropicAPIKey
	case providerGemini:
		set.Set(headerGeminiAPIKey, credential)
		drop = append(drop, HeaderAuthorization)
		authHeader = headerGeminiAPIKey
	case providerOpenAI:
		set.Set(HeaderAuthorization, bearerPrefix+credential)
		authHeader = HeaderAuthorization
	default:
		set.Set(HeaderAuthorization, bearerPrefix+credential)
		authHeader = HeaderAuthorization
	}

	return set, drop, authHeader
}
