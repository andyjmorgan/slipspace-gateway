package main

import (
	"testing"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
)

// TestResolveCredentialHeader covers the override fallback chain in the
// destination builder's credential mint site. The order is:
// endpoint override → provider override → per-provider default in
// auth.UpstreamCredentialHeader.
func TestResolveCredentialHeader(t *testing.T) {
	const cred = "sk-secret" //nolint:gosec // test fixture

	cases := []struct {
		name       string
		provider   contractsconfig.Provider
		endpoint   contractsconfig.Endpoint
		providerID string
		wantName   string
		wantValue  string
	}{
		{
			name:       "openai default → Bearer",
			providerID: "openai",
			wantName:   "Authorization",
			wantValue:  "Bearer " + cred,
		},
		{
			name:       "anthropic default → x-api-key",
			providerID: "anthropic",
			wantName:   "x-api-key",
			wantValue:  cred,
		},
		{
			name:       "gemini default → x-goog-api-key",
			providerID: "gemini",
			wantName:   "x-goog-api-key",
			wantValue:  cred,
		},
		{
			name:       "unknown provider default → Bearer",
			providerID: "qwen",
			wantName:   "Authorization",
			wantValue:  "Bearer " + cred,
		},
		{
			name: "endpoint override wins over provider default",
			endpoint: contractsconfig.Endpoint{
				AuthHeader: "Authorization",
				AuthFormat: "Bearer {key}",
			},
			providerID: "anthropic",
			wantName:   "Authorization",
			wantValue:  "Bearer " + cred,
		},
		{
			name: "provider override wins when endpoint empty",
			provider: contractsconfig.Provider{
				AuthHeader: "X-Acme-Token",
				AuthFormat: "Token {key}",
			},
			providerID: "acme",
			wantName:   "X-Acme-Token",
			wantValue:  "Token " + cred,
		},
		{
			name: "endpoint override beats provider override",
			provider: contractsconfig.Provider{
				AuthHeader: "X-Provider-Auth",
				AuthFormat: "Provider {key}",
			},
			endpoint: contractsconfig.Endpoint{
				AuthHeader: "X-Endpoint-Auth",
				AuthFormat: "Endpoint {key}",
			},
			providerID: "acme",
			wantName:   "X-Endpoint-Auth",
			wantValue:  "Endpoint " + cred,
		},
		{
			name: "endpoint header without format → raw credential",
			endpoint: contractsconfig.Endpoint{
				AuthHeader: "X-Custom-Key",
			},
			providerID: "anthropic",
			wantName:   "X-Custom-Key",
			wantValue:  cred,
		},
		{
			name: "provider header without format → raw credential",
			provider: contractsconfig.Provider{
				AuthHeader: "X-Provider-Key",
			},
			providerID: "anthropic",
			wantName:   "X-Provider-Key",
			wantValue:  cred,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotValue := resolveCredentialHeader(tc.provider, tc.endpoint, tc.providerID, cred)
			if gotName != tc.wantName {
				t.Errorf("header name = %q, want %q", gotName, tc.wantName)
			}
			if gotValue != tc.wantValue {
				t.Errorf("header value = %q, want %q", gotValue, tc.wantValue)
			}
		})
	}
}

// TestResolveCredentialHeader_FormatPlaceholderReplaced confirms the
// {key} placeholder is the only thing substituted — surrounding literal
// text round-trips verbatim.
func TestResolveCredentialHeader_FormatPlaceholderReplaced(t *testing.T) {
	provider := contractsconfig.Provider{}
	endpoint := contractsconfig.Endpoint{
		AuthHeader: "X-Wrapped",
		AuthFormat: "prefix-{key}-suffix",
	}
	const cred = "abc123" //nolint:gosec // test fixture
	name, value := resolveCredentialHeader(provider, endpoint, "any", cred)
	if name != "X-Wrapped" {
		t.Fatalf("name = %q, want X-Wrapped", name)
	}
	if value != "prefix-abc123-suffix" {
		t.Fatalf("value = %q, want prefix-abc123-suffix", value)
	}
}
