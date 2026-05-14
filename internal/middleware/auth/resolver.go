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

// AuthResult is the resolved decision for a single request. Downstream
// middleware reads this off the context.
type AuthResult struct {
	Mode Mode

	APIKey *contractsconfig.APIKey

	Configuration *contractsconfig.Configuration

	ConfigurationName string

	Provider string

	Endpoint string

	DropHeaders []string

	SetHeaders http.Header
}

// Resolver is the pure-logic resolver: given inbound headers and a routed
// (provider, endpoint) pair, it returns an AuthResult or a typed error. No
// HTTP serving concerns.
type Resolver struct {
	cfg *config.ResolvedConfig
}

// NewResolver constructs a Resolver bound to cfg. The resolver does not retain
// or mutate cfg beyond reading the SecretIndex and ConfigurationIndex.
func NewResolver(cfg *config.ResolvedConfig) *Resolver {
	return &Resolver{cfg: cfg}
}

// Resolve decides the auth mode and returns the resulting policy + header
// swaps to apply when forwarding upstream.
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

	set, drop := authSwap(provider, cfg.UpstreamCredentials[provider])

	return AuthResult{
		Mode:              ModeManaged,
		APIKey:            key,
		Configuration:     cfg,
		ConfigurationName: key.Configuration,
		Provider:          provider,
		Endpoint:          endpoint,
		SetHeaders:        set,
		DropHeaders:       drop,
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
// outgoing managed-mode request. The inbound X-Sluice-Configuration header is
// always dropped — it has no meaning upstream and would only be present if the
// client tried to mix modes. For Anthropic / Gemini the inbound Authorization
// header is also dropped because the upstream credential lives on a
// provider-specific header instead.
func authSwap(provider, credential string) (http.Header, []string) {
	set := http.Header{}
	drop := []string{HeaderConfiguration}

	switch provider {
	case providerAnthropic:
		set.Set(headerAnthropicAPIKey, credential)
		drop = append(drop, HeaderAuthorization)
	case providerGemini:
		set.Set(headerGeminiAPIKey, credential)
		drop = append(drop, HeaderAuthorization)
	case providerOpenAI:
		set.Set(HeaderAuthorization, bearerPrefix+credential)
	default:
		set.Set(HeaderAuthorization, bearerPrefix+credential)
	}

	return set, drop
}
