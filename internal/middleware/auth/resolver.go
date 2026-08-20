package auth

import (
	"net/http"
	"strings"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/config"
)

// Mode is the auth resolution mode for a request.
type Mode string

// Mode values exposed in AuthResult and emitted in structured logs as the
// "mode" field.
const (
	ModeManaged Mode = "managed"

	ModePassthrough Mode = "passthrough"
)

// Inbound header names. Passthrough mode is selected by either of two headers,
// checked in this order:
//
//  1. X-Slipspace-Identity carries a SlipSpace api-key secret; the matching key's
//     Configuration is used. The api-key is unguessable, which is the whole
//     reason this header exists. Preferred form for all new integrations.
//
//  2. X-Slipspace-Configuration carries the configuration name directly. The
//     name is human-readable and guessable — kept only as a deprecated
//     compatibility path. The resolver flags every use via
//     AuthResult.LegacyConfigurationHeader so the HTTP handler can emit a
//     deprecation warning. Slated for removal once callers migrate.
//
// When both passthrough headers are present X-Slipspace-Identity wins and the
// legacy header is flagged. With neither present, resolution falls through to
// managed mode and the SlipSpace secret is discovered from Authorization,
// x-api-key, or x-goog-api-key in that order — the latter two let vanilla
// provider SDKs work zero-config without forcing callers to inject an
// Authorization header.
const (
	HeaderIdentity      = "X-Slipspace-Identity"      //nolint:gosec // header name, not a credential
	HeaderConfiguration = "X-Slipspace-Configuration" // deprecated; see HeaderIdentity
	HeaderAuthorization = "Authorization"

	bearerPrefix = "Bearer "
)

// Pre-rename header names, still accepted as a silent fallback for the two
// passthrough selectors so in-flight clients keep working across the cutover.
// The current X-Slipspace-* names always win when both are present. Deliberately
// kept out of user-facing docs — remove once all callers have migrated.
const (
	legacyHeaderIdentity      = "X-Sluice-Identity" //nolint:gosec // header name, not a credential
	legacyHeaderConfiguration = "X-Sluice-Configuration"
)

// firstHeader returns the trimmed value of the first present header in names,
// in the order given. Used to prefer the current selector header while still
// honoring its legacy name.
func firstHeader(h http.Header, names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(h.Get(n)); v != "" {
			return v
		}
	}
	return ""
}

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
	// Mode is which auth scheme matched: ModeManaged when a SlipSpace-issued
	// bearer was presented in a credential header, ModePassthrough when
	// either passthrough selector header (X-Slipspace-Identity or
	// X-Slipspace-Configuration) routed the request.
	Mode Mode

	// APIKey is the matched SlipSpace api-key. Populated in managed mode and
	// in X-Slipspace-Identity passthrough — both rely on a key in
	// SecretIndex. Nil only on the legacy X-Slipspace-Configuration
	// passthrough path (which selects policy by configuration name and
	// has no key to attribute). Downstream code must nil-check.
	APIKey *contractsconfig.APIKey

	// Configuration is the resolved v2 policy bundle (providers credentials +
	// bindings + rules). Nil only when resolution failed before configuration
	// lookup.
	Configuration *contractsconfig.Configuration

	// ConfigurationName is the name the policy was looked up by — the
	// X-Slipspace-Configuration value (legacy passthrough) or
	// APIKey.Configuration (managed and identity-passthrough). Retained
	// for structured logging even when Configuration is nil.
	ConfigurationName string

	// DropHeaders names inbound headers the forwarder must strip before
	// sending upstream. Auth always emits both passthrough selector
	// headers here — they are policy-routing metadata, not credentials.
	// The destination builder layers additional drops on top based on the
	// post-rule provider + credential decision (e.g. dropping the inbound
	// Authorization when managed mode is forwarding to a provider that
	// uses a non-Bearer credential header).
	DropHeaders []string

	// LegacyConfigurationHeader is true when X-Slipspace-Configuration drove
	// resolution. The HTTP handler emits a structured deprecation warning
	// every time this fires so operators can spot un-migrated callers.
	// Cleared on managed and on X-Slipspace-Identity paths.
	LegacyConfigurationHeader bool
}

// Resolver decides the auth outcome for a request from the inbound headers
// alone: it identifies the Configuration and returns an AuthResult plus the
// header swap to apply, or a typed sentinel error. It makes no routing
// decision — provider, protocol, and upstream target are resolved downstream
// by internal/selection from the Configuration's bindings (CLAUDE.md
// invariant 7), so the resolver runs upstream of routing, not after it.
//
// Resolver contains no HTTP serving concerns and is safe to call from tests
// without standing up a server. HTTPHandler is the http.Handler adapter.
type Resolver struct {
	// store is the live configuration snapshot the resolver reads
	// SecretIndex and ConfigurationIndex through on every Resolve.
	// Holding the Store rather than a ResolvedConfig snapshot lets the
	// admin-write path swap configuration without rebuilding the
	// resolver — the next Resolve sees the new snapshot atomically.
	store *config.Store
}

// NewResolver constructs a Resolver bound to store. The resolver loads
// a snapshot from store at the top of every Resolve and passes that
// snapshot through to the sub-resolve methods, so a Replace landing
// mid-request still produces a consistent answer (the request sees
// either the pre-swap or the post-swap snapshot, never a mix).
func NewResolver(store *config.Store) *Resolver {
	return &Resolver{store: store}
}

// Resolve decides the auth mode and returns the resulting AuthResult plus
// the header swap to apply when forwarding upstream. Either passthrough
// selector header takes precedence over any bearer token on the same
// request — when present, resolution is always passthrough. Between the
// two, X-Slipspace-Identity wins over the deprecated X-Slipspace-Configuration.
func (r *Resolver) Resolve(headers http.Header) (AuthResult, error) {
	if r == nil || r.store == nil {
		return AuthResult{}, ErrUnknownConfiguration
	}
	snap := r.store.Snapshot()
	if snap == nil {
		return AuthResult{}, ErrUnknownConfiguration
	}

	identityToken := firstHeader(headers, HeaderIdentity, legacyHeaderIdentity)
	legacyConfigName := firstHeader(headers, HeaderConfiguration, legacyHeaderConfiguration)
	legacyPresent := legacyConfigName != ""

	if identityToken != "" {
		return r.resolveIdentityPassthrough(snap, identityToken, legacyPresent)
	}

	if legacyPresent {
		return r.resolveLegacyPassthrough(snap, legacyConfigName)
	}

	return r.resolveManaged(snap, headers)
}

// resolveIdentityPassthrough looks the supplied api-key secret up in
// SecretIndex and routes through the key's Configuration without
// substituting upstream credentials. Unknown or disabled keys fail with
// ErrUnauthorized so attackers cannot probe configuration names by
// presenting random identity values.
func (r *Resolver) resolveIdentityPassthrough(snap *config.ResolvedConfig, token string, legacyAlsoPresent bool) (AuthResult, error) {
	key, ok := snap.SecretIndex[token]
	if !ok || key == nil {
		return AuthResult{Mode: ModePassthrough, DropHeaders: passthroughDropHeaders()}, ErrUnauthorized
	}
	if !key.Enabled {
		return AuthResult{
			Mode:        ModePassthrough,
			APIKey:      key,
			DropHeaders: passthroughDropHeaders(),
		}, ErrUnauthorized
	}
	cfg, ok := snap.ConfigurationIndex[key.Configuration]
	if !ok {
		return AuthResult{
			Mode:              ModePassthrough,
			APIKey:            key,
			ConfigurationName: key.Configuration,
			DropHeaders:       passthroughDropHeaders(),
		}, ErrUnknownConfiguration
	}
	return AuthResult{
		Mode:                      ModePassthrough,
		APIKey:                    key,
		Configuration:             cfg,
		ConfigurationName:         key.Configuration,
		DropHeaders:               passthroughDropHeaders(),
		LegacyConfigurationHeader: legacyAlsoPresent,
	}, nil
}

// resolveLegacyPassthrough is the original X-Slipspace-Configuration path.
// Marked legacy because the configuration name is human-readable and
// guessable; X-Slipspace-Identity supersedes it.
func (r *Resolver) resolveLegacyPassthrough(snap *config.ResolvedConfig, configName string) (AuthResult, error) {
	cfg, ok := snap.ConfigurationIndex[configName]
	if !ok {
		return AuthResult{LegacyConfigurationHeader: true}, ErrUnknownConfiguration
	}

	return AuthResult{
		Mode:                      ModePassthrough,
		Configuration:             cfg,
		ConfigurationName:         configName,
		DropHeaders:               passthroughDropHeaders(),
		LegacyConfigurationHeader: true,
	}, nil
}

// passthroughDropHeaders returns the constant drop list for any
// passthrough resolution. Both selector headers go upstream-blacklisted
// regardless of which one was actually present — they are gateway
// metadata, never anything the provider should see.
func passthroughDropHeaders() []string {
	return []string{HeaderIdentity, HeaderConfiguration, legacyHeaderIdentity, legacyHeaderConfiguration}
}

// managedKeySource names the inbound header a SlipSpace key was discovered on.
// The forwarder uses this to add the source header to DropHeaders so the
// raw SlipSpace secret never leaks upstream — the destination builder will
// inject the resolved upstream credential under the per-provider /
// per-endpoint header anyway.
type managedKeySource struct {
	header string
	token  string
}

// discoverManagedKey walks the inbound headers in the order Airia's
// gateway does — Authorization Bearer first (the explicit signal), then
// the provider-native shapes (x-api-key for Anthropic SDKs, x-goog-api-key
// for Gemini SDKs). The first header whose value is a known SlipSpace secret
// wins. A header may be present with a value that is not in SecretIndex
// (e.g. an OpenAI sk- key supplied by a misconfigured client) — that
// value short-circuits: resolveManaged returns ErrUnauthorized rather
// than trying the next header, so an attacker cannot confuse resolution
// by stuffing multiple headers. A malformed Authorization (no parseable
// `Bearer ` token) is a discovery miss, not a value, and does fall
// through to x-api-key then x-goog-api-key — see
// TestResolver_Managed_MalformedAuthorizationFallsThroughToNative.
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

func (r *Resolver) resolveManaged(snap *config.ResolvedConfig, headers http.Header) (AuthResult, error) {
	src, ok := r.discoverManagedKey(headers)
	if !ok {
		return AuthResult{Mode: ModeManaged}, ErrUnauthorized
	}

	key, ok := snap.SecretIndex[src.token]
	if !ok || key == nil {
		return AuthResult{Mode: ModeManaged}, ErrUnauthorized
	}

	if !key.Enabled {
		return AuthResult{
			Mode:   ModeManaged,
			APIKey: key,
		}, ErrUnauthorized
	}

	cfg, ok := snap.ConfigurationIndex[key.Configuration]
	if !ok {
		return AuthResult{
			Mode:              ModeManaged,
			APIKey:            key,
			ConfigurationName: key.Configuration,
		}, ErrUnknownConfiguration
	}

	drops := passthroughDropHeaders()
	// Always drop whichever native header carried the SlipSpace secret. The
	// destination builder will mint the correct upstream credential header
	// for the resolved (provider, endpoint) pair — leaving the inbound
	// header in place would either leak the SlipSpace secret upstream
	// (Authorization to OpenAI) or collide with the freshly-injected
	// upstream credential header (x-api-key to Anthropic).
	if src.header != HeaderConfiguration && src.header != HeaderIdentity {
		drops = append(drops, src.header)
	}

	return AuthResult{
		Mode:              ModeManaged,
		APIKey:            key,
		Configuration:     cfg,
		ConfigurationName: key.Configuration,
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
// mode requests. Exported so the single credential mint site — the
// gateway's destination builder (cmd/gateway/destination.go::
// resolveCredentialHeaders, via its per-provider format helper
// credentialHeaderFor) — can re-mint the header for a post-rule
// changeApiKey override against the post-rule provider's format
// without duplicating the per-provider format table (invariant #6).
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
