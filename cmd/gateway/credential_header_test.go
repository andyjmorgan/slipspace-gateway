package main

import (
	"testing"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/selection"
)

// TestCredentialHeaderFor exercises the v2 single credential mint site: the
// resolved target's per-protocol auth convention wins, falling back to the
// per-provider-name default when the target declares none.
func TestCredentialHeaderFor(t *testing.T) {
	const cred = "sk-secret-1234"
	cases := []struct {
		name      string
		target    selection.Target
		wantName  string
		wantValue string
	}{
		{
			name:      "nil auth falls back to openai default (Bearer)",
			target:    selection.Target{Provider: "openai", Credential: cred},
			wantName:  "Authorization",
			wantValue: "Bearer " + cred,
		},
		{
			name:      "nil auth falls back to anthropic default (x-api-key)",
			target:    selection.Target{Provider: "anthropic", Credential: cred},
			wantName:  "x-api-key",
			wantValue: cred,
		},
		{
			name:      "nil auth falls back to gemini default (x-goog-api-key)",
			target:    selection.Target{Provider: "gemini", Credential: cred},
			wantName:  "x-goog-api-key",
			wantValue: cred,
		},
		{
			name: "explicit header, no format → raw credential",
			target: selection.Target{
				Provider:   "azure-foundry",
				Credential: cred,
				Auth:       &contractsconfig.ProviderAuth{Header: "api-key"},
			},
			wantName:  "api-key",
			wantValue: cred,
		},
		{
			name: "explicit header + format → placeholder substituted",
			target: selection.Target{
				Provider:   "anthropic",
				Credential: cred,
				Auth:       &contractsconfig.ProviderAuth{Header: "Authorization", Format: "Bearer {key}"},
			},
			wantName:  "Authorization",
			wantValue: "Bearer " + cred,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotValue := credentialHeaderFor(tc.target, tc.target.Credential)
			if gotName != tc.wantName {
				t.Errorf("header name = %q, want %q", gotName, tc.wantName)
			}
			if gotValue != tc.wantValue {
				t.Errorf("header value = %q, want %q", gotValue, tc.wantValue)
			}
		})
	}
}
