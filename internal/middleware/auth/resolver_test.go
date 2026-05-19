package auth

import (
	"errors"
	"net/http"
	"testing"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

func fixtureConfig() *config.ResolvedConfig {
	enabled := contractsconfig.APIKey{ //nolint:gosec // test fixture, not a credential
		Secret:        "sk_live_enabled",
		Name:          "enabled-key",
		Configuration: "prod",
		Enabled:       true,
	}
	disabled := contractsconfig.APIKey{
		Secret:        "sk_live_disabled",
		Name:          "disabled-key",
		Configuration: "prod",
		Enabled:       false,
	}
	orphan := contractsconfig.APIKey{ //nolint:gosec // test fixture, not a credential
		Secret:        "sk_live_orphan",
		Name:          "orphan-key",
		Configuration: "ghost",
		Enabled:       true,
	}
	openaiCfg := contractsconfig.Configuration{
		UpstreamCredentials: map[string]string{
			"openai":    "sk-openai-upstream",
			"anthropic": "ak-anthropic-upstream",
			"gemini":    "AIza-gemini-upstream",
			"custom":    "custom-upstream",
		},
	}
	restricted := contractsconfig.Configuration{
		UpstreamCredentials: map[string]string{
			"openai": "sk-openai-restricted",
		},
	}

	cfg := &config.ResolvedConfig{
		Configurations: contractsconfig.ConfigurationsConfig{
			"prod":       openaiCfg,
			"restricted": restricted,
		},
		APIKeys: contractsconfig.APIKeysConfig{enabled, disabled, orphan},
	}
	cfg.SecretIndex = map[string]*contractsconfig.APIKey{
		enabled.Secret:  &enabled,
		disabled.Secret: &disabled,
		orphan.Secret:   &orphan,
	}
	cfg.ConfigurationIndex = map[string]*contractsconfig.Configuration{
		"prod":       &openaiCfg,
		"restricted": &restricted,
	}
	return cfg
}

func TestResolver_Managed_OpenAI(t *testing.T) {
	r := NewResolver(fixtureConfig())
	headers := http.Header{}
	headers.Set(HeaderAuthorization, "Bearer sk_live_enabled")

	ar, err := r.Resolve(headers, "openai", "chat_completions")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if ar.Mode != ModeManaged {
		t.Fatalf("mode = %q want %q", ar.Mode, ModeManaged)
	}
	if ar.Configuration == nil {
		t.Fatal("Configuration should be resolved on success")
	}
	// After the refactor, the auth resolver no longer mints the
	// upstream credential header — that's the destination builder's
	// job. The resolver propagates the Configuration pointer so the
	// destination builder can look up the credential. DropHeaders
	// is reduced to the policy header only.
	if cred := ar.Configuration.UpstreamCredentials["openai"]; cred != "sk-openai-upstream" { //nolint:gosec // test fixture, not a real credential
		t.Fatalf("Configuration.UpstreamCredentials[openai] = %q want sk-openai-upstream", cred)
	}
	if !sliceContains(ar.DropHeaders, HeaderConfiguration) {
		t.Fatalf("X-Sluice-Configuration should be dropped, got %v", ar.DropHeaders)
	}
	if ar.APIKey == nil || ar.APIKey.Name != "enabled-key" {
		t.Fatalf("APIKey not set correctly")
	}
}

func TestResolver_Managed_Anthropic(t *testing.T) {
	r := NewResolver(fixtureConfig())
	headers := http.Header{}
	headers.Set(HeaderAuthorization, "Bearer sk_live_enabled")

	ar, err := r.Resolve(headers, "anthropic", "messages")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if ar.Configuration == nil {
		t.Fatal("Configuration should be resolved")
	}
	if cred := ar.Configuration.UpstreamCredentials["anthropic"]; cred != "ak-anthropic-upstream" { //nolint:gosec // test fixture
		t.Fatalf("anthropic credential = %q want ak-anthropic-upstream", cred)
	}
	if !sliceContains(ar.DropHeaders, HeaderConfiguration) {
		t.Fatalf("X-Sluice-Configuration must be dropped, got %v", ar.DropHeaders)
	}
}

func TestResolver_Managed_Gemini(t *testing.T) {
	r := NewResolver(fixtureConfig())
	headers := http.Header{}
	headers.Set(HeaderAuthorization, "Bearer sk_live_enabled")

	ar, err := r.Resolve(headers, "gemini", "generate_content")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if ar.Configuration == nil {
		t.Fatal("Configuration should be resolved")
	}
	if cred := ar.Configuration.UpstreamCredentials["gemini"]; cred != "AIza-gemini-upstream" { //nolint:gosec // test fixture
		t.Fatalf("gemini credential = %q want AIza-gemini-upstream", cred)
	}
}

// Unknown-provider credential-format fallback is now exercised
// directly via TestUpstreamCredentialHeader_ByProvider — the auth
// resolver no longer mints the header itself, so there is nothing
// provider-specific left to test at this layer.

func TestResolver_Managed_MissingBearer(t *testing.T) {
	r := NewResolver(fixtureConfig())
	_, err := r.Resolve(http.Header{}, "openai", "chat_completions")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestResolver_Managed_MalformedAuthorization(t *testing.T) {
	cases := []string{
		"",
		"Bearer ",
		"Bearer",
		"Token sk_live_enabled",
		"sk_live_enabled",
		"Basic dXNlcjpwYXNz",
	}
	r := NewResolver(fixtureConfig())
	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			h := http.Header{}
			if v != "" {
				h.Set(HeaderAuthorization, v)
			}
			_, err := r.Resolve(h, "openai", "chat_completions")
			if !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("want ErrUnauthorized for %q, got %v", v, err)
			}
		})
	}
}

func TestResolver_Managed_CaseInsensitiveScheme(t *testing.T) {
	r := NewResolver(fixtureConfig())
	headers := http.Header{}
	headers.Set(HeaderAuthorization, "bearer sk_live_enabled")
	if _, err := r.Resolve(headers, "openai", "chat_completions"); err != nil {
		t.Fatalf("lowercase scheme should be accepted, got %v", err)
	}
}

func TestResolver_Managed_UnknownSecret(t *testing.T) {
	r := NewResolver(fixtureConfig())
	headers := http.Header{}
	headers.Set(HeaderAuthorization, "Bearer sk_live_nope")
	_, err := r.Resolve(headers, "openai", "chat_completions")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestResolver_Managed_DisabledKey(t *testing.T) {
	r := NewResolver(fixtureConfig())
	headers := http.Header{}
	headers.Set(HeaderAuthorization, "Bearer sk_live_disabled")
	ar, err := r.Resolve(headers, "openai", "chat_completions")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
	if ar.APIKey == nil || ar.APIKey.Enabled {
		t.Fatalf("APIKey should be populated and disabled so the handler can classify result=disabled_key")
	}
}

func TestResolver_Managed_UnknownConfigurationOnKey(t *testing.T) {
	r := NewResolver(fixtureConfig())
	headers := http.Header{}
	headers.Set(HeaderAuthorization, "Bearer sk_live_orphan")
	_, err := r.Resolve(headers, "openai", "chat_completions")
	if !errors.Is(err, ErrUnknownConfiguration) {
		t.Fatalf("want ErrUnknownConfiguration, got %v", err)
	}
}

func TestResolver_Passthrough_Happy(t *testing.T) {
	r := NewResolver(fixtureConfig())
	headers := http.Header{}
	headers.Set(HeaderConfiguration, "prod")
	headers.Set(HeaderAuthorization, "Bearer some-byok-token")

	ar, err := r.Resolve(headers, "anthropic", "messages")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if ar.Mode != ModePassthrough {
		t.Fatalf("mode = %q want %q", ar.Mode, ModePassthrough)
	}
	if ar.APIKey != nil {
		t.Fatalf("APIKey must be nil in passthrough")
	}
	if len(ar.DropHeaders) != 1 || ar.DropHeaders[0] != HeaderConfiguration {
		t.Fatalf("passthrough DropHeaders must be [%s] (policy header), got %v", HeaderConfiguration, ar.DropHeaders)
	}
	if ar.ConfigurationName != "prod" {
		t.Fatalf("ConfigurationName = %q want prod", ar.ConfigurationName)
	}
}

func TestResolver_Passthrough_UnknownConfiguration(t *testing.T) {
	r := NewResolver(fixtureConfig())
	headers := http.Header{}
	headers.Set(HeaderConfiguration, "ghost")

	_, err := r.Resolve(headers, "openai", "chat_completions")
	if !errors.Is(err, ErrUnknownConfiguration) {
		t.Fatalf("want ErrUnknownConfiguration, got %v", err)
	}
	// Sentinel-wrap regression guard: ErrUnknownConfiguration in this
	// package wraps config.ErrUnknownConfiguration so a single
	// errors.Is succeeds against either sentinel. Important for the
	// v1.1 api server, which will do both load-time validate and
	// request-time resolve in one call site.
	if !errors.Is(err, config.ErrUnknownConfiguration) {
		t.Fatalf("want errors.Is(err, config.ErrUnknownConfiguration) to succeed via the auth wrap, got %v", err)
	}
}

func TestResolver_PassthroughWinsOverBearer(t *testing.T) {
	r := NewResolver(fixtureConfig())
	headers := http.Header{}
	headers.Set(HeaderConfiguration, "prod")
	headers.Set(HeaderAuthorization, "Bearer sk_live_enabled")

	ar, err := r.Resolve(headers, "openai", "chat_completions")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if ar.Mode != ModePassthrough {
		t.Fatalf("passthrough must win when both headers set; got mode=%q", ar.Mode)
	}
	if ar.APIKey != nil {
		t.Fatalf("APIKey must be nil even when a valid bearer is present in passthrough mode")
	}
}

func TestResolver_PassthroughHeaderWhitespaceTrimmed(t *testing.T) {
	r := NewResolver(fixtureConfig())
	headers := http.Header{}
	headers.Set(HeaderConfiguration, "  prod  ")

	ar, err := r.Resolve(headers, "openai", "chat_completions")
	if err != nil {
		t.Fatalf("want success, got %v", err)
	}
	if ar.ConfigurationName != "prod" {
		t.Fatalf("ConfigurationName = %q want prod (trimmed)", ar.ConfigurationName)
	}
}

func TestResolver_PassthroughEmptyHeaderFallsThroughToManaged(t *testing.T) {
	r := NewResolver(fixtureConfig())
	headers := http.Header{}
	headers.Set(HeaderConfiguration, "   ")
	headers.Set(HeaderAuthorization, "Bearer sk_live_enabled")

	ar, err := r.Resolve(headers, "openai", "chat_completions")
	if err != nil {
		t.Fatalf("want success, got %v", err)
	}
	if ar.Mode != ModeManaged {
		t.Fatalf("blank X-Sluice-Configuration must not trigger passthrough; got %q", ar.Mode)
	}
}

func TestResolver_NilGuards(t *testing.T) {
	var r *Resolver
	_, err := r.Resolve(http.Header{}, "openai", "chat_completions")
	if !errors.Is(err, ErrUnknownConfiguration) {
		t.Fatalf("nil resolver: want ErrUnknownConfiguration, got %v", err)
	}

	r2 := &Resolver{cfg: nil}
	_, err = r2.Resolve(http.Header{}, "openai", "chat_completions")
	if !errors.Is(err, ErrUnknownConfiguration) {
		t.Fatalf("nil cfg: want ErrUnknownConfiguration, got %v", err)
	}
}

func TestUpstreamCredentialHeader_ByProvider(t *testing.T) {
	tests := []struct {
		provider  string
		wantName  string
		wantValue string
	}{
		{providerOpenAI, HeaderAuthorization, "Bearer cred"},
		{providerAnthropic, headerAnthropicAPIKey, "cred"},
		{providerGemini, headerGeminiAPIKey, "cred"},
		{"unknown-provider", HeaderAuthorization, "Bearer cred"},
	}
	for _, tc := range tests {
		t.Run(tc.provider, func(t *testing.T) {
			name, value := UpstreamCredentialHeader(tc.provider, "cred")
			if name != tc.wantName {
				t.Errorf("name = %q want %q", name, tc.wantName)
			}
			if value != tc.wantValue {
				t.Errorf("value = %q want %q", value, tc.wantValue)
			}
		})
	}
}

// v1.0.7: vanilla provider SDKs ship their native API key header by default
// (anthropic.Anthropic → x-api-key, google-genai → x-goog-api-key). The
// resolver discovers the Sluice secret from any of three inbound headers so
// callers don't need to inject Authorization manually.

func TestResolver_Managed_DiscoversViaXAPIKey(t *testing.T) {
	r := NewResolver(fixtureConfig())
	headers := http.Header{}
	headers.Set(headerAnthropicAPIKey, "sk_live_enabled")

	ar, err := r.Resolve(headers, "anthropic", "messages")
	if err != nil {
		t.Fatalf("want success, got %v", err)
	}
	if ar.Mode != ModeManaged {
		t.Fatalf("mode = %q want managed", ar.Mode)
	}
	if !sliceContains(ar.DropHeaders, headerAnthropicAPIKey) {
		t.Fatalf("x-api-key must be dropped from outbound; got %v", ar.DropHeaders)
	}
	if !sliceContains(ar.DropHeaders, HeaderConfiguration) {
		t.Fatalf("policy header must always be dropped; got %v", ar.DropHeaders)
	}
}

func TestResolver_Managed_DiscoversViaXGoogAPIKey(t *testing.T) {
	r := NewResolver(fixtureConfig())
	headers := http.Header{}
	headers.Set(headerGeminiAPIKey, "sk_live_enabled")

	ar, err := r.Resolve(headers, "gemini", "generate_content")
	if err != nil {
		t.Fatalf("want success, got %v", err)
	}
	if ar.Mode != ModeManaged {
		t.Fatalf("mode = %q want managed", ar.Mode)
	}
	if !sliceContains(ar.DropHeaders, headerGeminiAPIKey) {
		t.Fatalf("x-goog-api-key must be dropped from outbound; got %v", ar.DropHeaders)
	}
}

// TestResolver_Managed_AuthorizationWinsOverNativeHeaders fixes the
// discovery order: when Authorization Bearer carries a valid Sluice secret,
// any concurrent x-api-key / x-goog-api-key is ignored even if it would also
// resolve. Bearer is the explicit, historical signal — making it primary
// minimises behaviour change for existing clients.
func TestResolver_Managed_AuthorizationWinsOverNativeHeaders(t *testing.T) {
	r := NewResolver(fixtureConfig())
	headers := http.Header{}
	headers.Set(HeaderAuthorization, "Bearer sk_live_enabled")
	headers.Set(headerAnthropicAPIKey, "sk_live_orphan") // would route to a different (broken) configuration
	headers.Set(headerGeminiAPIKey, "sk_live_disabled")  // would 401

	ar, err := r.Resolve(headers, "openai", "chat_completions")
	if err != nil {
		t.Fatalf("want success via Authorization, got %v", err)
	}
	if ar.APIKey == nil || ar.APIKey.Name != "enabled-key" {
		t.Fatalf("Authorization Bearer must win; got APIKey=%+v", ar.APIKey)
	}
	if !sliceContains(ar.DropHeaders, HeaderAuthorization) {
		t.Fatalf("Authorization must be dropped on managed-mode success; got %v", ar.DropHeaders)
	}
}

// TestResolver_Managed_XAPIKeyWinsOverXGoogAPIKey covers the secondary
// priority — when there is no Authorization, anthropic-native beats
// gemini-native. The order is fixed (not provider-aware) so a single client
// hitting multiple endpoints behaves predictably.
func TestResolver_Managed_XAPIKeyWinsOverXGoogAPIKey(t *testing.T) {
	r := NewResolver(fixtureConfig())
	headers := http.Header{}
	headers.Set(headerAnthropicAPIKey, "sk_live_enabled")
	headers.Set(headerGeminiAPIKey, "sk_live_disabled") // would 401 if it won

	ar, err := r.Resolve(headers, "openai", "chat_completions")
	if err != nil {
		t.Fatalf("want success via x-api-key, got %v", err)
	}
	if ar.APIKey == nil || ar.APIKey.Name != "enabled-key" {
		t.Fatalf("x-api-key must win; got %+v", ar.APIKey)
	}
}

// TestResolver_Managed_MalformedAuthorizationFallsThroughToNative locks the
// behaviour where a malformed Authorization (e.g. "Token foo") does not
// short-circuit — the resolver tries x-api-key next, matching Airia's
// "any-header-can-carry-the-key" pattern.
func TestResolver_Managed_MalformedAuthorizationFallsThroughToNative(t *testing.T) {
	r := NewResolver(fixtureConfig())
	headers := http.Header{}
	headers.Set(HeaderAuthorization, "Token bogus")
	headers.Set(headerAnthropicAPIKey, "sk_live_enabled")

	ar, err := r.Resolve(headers, "anthropic", "messages")
	if err != nil {
		t.Fatalf("want success via x-api-key fallthrough, got %v", err)
	}
	if ar.APIKey == nil || ar.APIKey.Name != "enabled-key" {
		t.Fatalf("APIKey not resolved via x-api-key fallthrough")
	}
}

// TestResolver_Managed_NativeHeaderUnknownSecret enforces that an x-api-key
// value not present in SecretIndex returns ErrUnauthorized — does NOT fall
// through to x-goog-api-key. Otherwise an attacker could probe by stuffing
// every header.
func TestResolver_Managed_NativeHeaderUnknownSecret(t *testing.T) {
	r := NewResolver(fixtureConfig())
	headers := http.Header{}
	headers.Set(headerAnthropicAPIKey, "sk-anthropic-customer-byok") // not a Sluice secret
	headers.Set(headerGeminiAPIKey, "sk_live_enabled")               // ignored, x-api-key short-circuits

	_, err := r.Resolve(headers, "anthropic", "messages")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized (no x-api-key→x-goog-api-key fallthrough), got %v", err)
	}
}

// TestResolver_Passthrough_NativeHeaderIgnored guards the load-bearing
// invariant: when X-Sluice-Configuration is present, the resolver never
// looks at x-api-key / x-goog-api-key. The client's upstream credential
// stays in its native header and is forwarded verbatim. Critical for the
// Claude Code passthrough flow where x-api-key carries an Anthropic key
// that must not be confused with a Sluice secret.
func TestResolver_Passthrough_NativeHeaderIgnored(t *testing.T) {
	r := NewResolver(fixtureConfig())
	headers := http.Header{}
	headers.Set(HeaderConfiguration, "prod")
	headers.Set(headerAnthropicAPIKey, "sk_live_enabled") // would resolve in managed mode

	ar, err := r.Resolve(headers, "anthropic", "messages")
	if err != nil {
		t.Fatalf("want passthrough success, got %v", err)
	}
	if ar.Mode != ModePassthrough {
		t.Fatalf("mode = %q want passthrough", ar.Mode)
	}
	if ar.APIKey != nil {
		t.Fatalf("APIKey must be nil in passthrough")
	}
	for _, h := range ar.DropHeaders {
		if h == headerAnthropicAPIKey {
			t.Fatalf("x-api-key MUST NOT be dropped in passthrough — it carries the upstream key")
		}
	}
}

func sliceContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
