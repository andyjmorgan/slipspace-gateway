package auth

import (
	"errors"
	"net/http"
	"reflect"
	"sort"
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
	if got := ar.SetHeaders.Get(HeaderAuthorization); got != "Bearer sk-openai-upstream" {
		t.Fatalf("Authorization set header = %q want Bearer sk-openai-upstream", got)
	}
	if !sliceContains(ar.DropHeaders, HeaderConfiguration) {
		t.Fatalf("X-Sluice-Configuration should be dropped, got %v", ar.DropHeaders)
	}
	if sliceContains(ar.DropHeaders, HeaderAuthorization) {
		t.Fatalf("Authorization should not be dropped for openai (it is replaced via SetHeaders)")
	}
	if ar.APIKey == nil || ar.APIKey.Name != "enabled-key" {
		t.Fatalf("APIKey not set correctly")
	}
	if ar.AuthHeader != HeaderAuthorization {
		t.Fatalf("AuthHeader = %q want %q", ar.AuthHeader, HeaderAuthorization)
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
	if got := ar.SetHeaders.Get(headerAnthropicAPIKey); got != "ak-anthropic-upstream" {
		t.Fatalf("x-api-key = %q want ak-anthropic-upstream", got)
	}
	if !sliceContains(ar.DropHeaders, HeaderAuthorization) {
		t.Fatalf("Authorization must be dropped for anthropic, got %v", ar.DropHeaders)
	}
	if !sliceContains(ar.DropHeaders, HeaderConfiguration) {
		t.Fatalf("X-Sluice-Configuration must be dropped, got %v", ar.DropHeaders)
	}
	if ar.AuthHeader != headerAnthropicAPIKey {
		t.Fatalf("AuthHeader = %q want %q", ar.AuthHeader, headerAnthropicAPIKey)
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
	if got := ar.SetHeaders.Get(headerGeminiAPIKey); got != "AIza-gemini-upstream" {
		t.Fatalf("x-goog-api-key = %q want AIza-gemini-upstream", got)
	}
	if !sliceContains(ar.DropHeaders, HeaderAuthorization) {
		t.Fatalf("Authorization must be dropped for gemini, got %v", ar.DropHeaders)
	}
	if ar.AuthHeader != headerGeminiAPIKey {
		t.Fatalf("AuthHeader = %q want %q", ar.AuthHeader, headerGeminiAPIKey)
	}
}

func TestResolver_Managed_UnknownProvider(t *testing.T) {
	r := NewResolver(fixtureConfig())
	headers := http.Header{}
	headers.Set(HeaderAuthorization, "Bearer sk_live_enabled")

	ar, err := r.Resolve(headers, "custom", "foo")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if got := ar.SetHeaders.Get(HeaderAuthorization); got != "Bearer custom-upstream" {
		t.Fatalf("Authorization = %q want Bearer custom-upstream", got)
	}
	if ar.AuthHeader != HeaderAuthorization {
		t.Fatalf("AuthHeader = %q want %q for unknown provider fallback", ar.AuthHeader, HeaderAuthorization)
	}
}

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
	if len(ar.DropHeaders) != 0 {
		t.Fatalf("DropHeaders must be empty in passthrough, got %v", ar.DropHeaders)
	}
	if len(ar.SetHeaders) != 0 {
		t.Fatalf("SetHeaders must be empty in passthrough, got %v", ar.SetHeaders)
	}
	if ar.ConfigurationName != "prod" {
		t.Fatalf("ConfigurationName = %q want prod", ar.ConfigurationName)
	}
	if ar.AuthHeader != "" {
		t.Fatalf("AuthHeader must be empty in passthrough, got %q", ar.AuthHeader)
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

func TestAuthSwap_DropHeadersSorted(t *testing.T) {
	tests := []struct {
		provider       string
		wantSet        string
		wantValue      string
		wantDrop       []string
		wantAuthHeader string
	}{
		{providerOpenAI, HeaderAuthorization, "Bearer cred", []string{HeaderConfiguration}, HeaderAuthorization},
		{providerAnthropic, headerAnthropicAPIKey, "cred", []string{HeaderAuthorization, HeaderConfiguration}, headerAnthropicAPIKey},
		{providerGemini, headerGeminiAPIKey, "cred", []string{HeaderAuthorization, HeaderConfiguration}, headerGeminiAPIKey},
		{"unknown-provider", HeaderAuthorization, "Bearer cred", []string{HeaderConfiguration}, HeaderAuthorization},
	}
	for _, tc := range tests {
		t.Run(tc.provider, func(t *testing.T) {
			set, drop, authHeader := authSwap(tc.provider, "cred")
			if got := set.Get(tc.wantSet); got != tc.wantValue {
				t.Fatalf("%s = %q want %q", tc.wantSet, got, tc.wantValue)
			}
			gotDrop := append([]string(nil), drop...)
			sort.Strings(gotDrop)
			want := append([]string(nil), tc.wantDrop...)
			sort.Strings(want)
			if !reflect.DeepEqual(gotDrop, want) {
				t.Fatalf("drop = %v want %v", gotDrop, want)
			}
			if authHeader != tc.wantAuthHeader {
				t.Fatalf("authHeader = %q want %q", authHeader, tc.wantAuthHeader)
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
