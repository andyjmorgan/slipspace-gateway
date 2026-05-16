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
		AllowedEndpoints: []string{
			"openai.chat_completions",
			"anthropic.messages",
			"gemini.generate_content",
			"custom.foo",
		},
		UpstreamCredentials: map[string]string{
			"openai":    "sk-openai-upstream",
			"anthropic": "ak-anthropic-upstream",
			"gemini":    "AIza-gemini-upstream",
			"custom":    "custom-upstream",
		},
	}
	restricted := contractsconfig.Configuration{
		AllowedEndpoints: []string{"openai.models"},
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

func TestResolver_Managed_EndpointNotAllowed(t *testing.T) {
	cfg := fixtureConfig()
	cfg.APIKeys[0].Configuration = "restricted"
	cfg.SecretIndex["sk_live_enabled"].Configuration = "restricted"

	r := NewResolver(cfg)
	headers := http.Header{}
	headers.Set(HeaderAuthorization, "Bearer sk_live_enabled")

	_, err := r.Resolve(headers, "openai", "chat_completions")
	if !errors.Is(err, ErrEndpointNotAllowed) {
		t.Fatalf("want ErrEndpointNotAllowed, got %v", err)
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
}

func TestResolver_Passthrough_EndpointNotAllowed(t *testing.T) {
	r := NewResolver(fixtureConfig())
	headers := http.Header{}
	headers.Set(HeaderConfiguration, "restricted")

	_, err := r.Resolve(headers, "openai", "chat_completions")
	if !errors.Is(err, ErrEndpointNotAllowed) {
		t.Fatalf("want ErrEndpointNotAllowed, got %v", err)
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

func sliceContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
