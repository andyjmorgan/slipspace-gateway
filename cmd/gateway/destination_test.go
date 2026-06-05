package main

import (
	"testing"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/auth"
	"github.com/andyjmorgan/sluice-gateway/internal/selection"
)

// TestBuildDestination_CredentialModes covers the three credential branches
// of the v2 single mint site: passthrough forwards the inbound Authorization,
// managed with a credential sets the provider's auth header and strips the
// others, and managed with an empty credential strips everything.
func TestBuildDestination_CredentialModes(t *testing.T) {
	target := selection.Target{ //nolint:gosec // synthetic test fixture, not a real credential
		Provider:   "anthropic",
		BaseURL:    "https://api.anthropic.com",
		Path:       "/v1/messages",
		Auth:       &contractsconfig.ProviderAuth{Header: "x-api-key", Format: "{key}"},
		Credential: "sk-upstream-anthropic",
	}

	t.Run("passthrough forwards inbound authorization", func(t *testing.T) {
		dest, err := buildDestination(target, nil, auth.ModePassthrough, nil, "Bearer client-token")
		if err != nil {
			t.Fatalf("buildDestination: %v", err)
		}
		if got := dest.OutgoingHeaders.Get("Authorization"); got != "Bearer client-token" {
			t.Errorf("Authorization = %q, want forwarded inbound", got)
		}
		if got := dest.OutgoingHeaders.Get("x-api-key"); got != "" {
			t.Errorf("x-api-key = %q, want unset in passthrough", got)
		}
	})

	t.Run("managed sets provider auth header and drops the others", func(t *testing.T) {
		dest, err := buildDestination(target, nil, auth.ModeManaged, nil, "Bearer client-token")
		if err != nil {
			t.Fatalf("buildDestination: %v", err)
		}
		if got := dest.OutgoingHeaders.Get("x-api-key"); got != "sk-upstream-anthropic" {
			t.Errorf("x-api-key = %q, want minted credential", got)
		}
		// Authorization must be on the drop list so the inbound Bearer never
		// leaks to a provider that authenticates via x-api-key.
		if !contains(dest.DropHeaders, "Authorization") {
			t.Errorf("DropHeaders = %v, want Authorization dropped", dest.DropHeaders)
		}
	})

	t.Run("managed empty credential strips all credential headers", func(t *testing.T) {
		noCred := target
		noCred.Credential = ""
		dest, err := buildDestination(noCred, nil, auth.ModeManaged, nil, "Bearer client-token")
		if err != nil {
			t.Fatalf("buildDestination: %v", err)
		}
		for _, h := range credentialHeaderNames {
			if !contains(dest.DropHeaders, h) {
				t.Errorf("DropHeaders = %v, want %q dropped", dest.DropHeaders, h)
			}
			if v := dest.OutgoingHeaders.Get(h); v != "" {
				t.Errorf("%s = %q, want unset for no-credential provider", h, v)
			}
		}
	})
}

// TestBuildDestination_PathAndQuery verifies path-param substitution (Gemini
// {model}) and that the provider's default query is applied.
func TestBuildDestination_PathAndQuery(t *testing.T) {
	target := selection.Target{
		Provider:   "gemini",
		BaseURL:    "https://generativelanguage.googleapis.com",
		Path:       "/v1beta/models/{model}:{op}",
		Auth:       &contractsconfig.ProviderAuth{Header: "x-goog-api-key", Format: "{key}"},
		Query:      map[string]string{"alt": "sse"},
		Credential: "gm-key",
	}
	params := map[string]string{"model": "gemini-2.5-pro", "op": "streamGenerateContent"}

	dest, err := buildDestination(target, params, auth.ModeManaged, nil, "")
	if err != nil {
		t.Fatalf("buildDestination: %v", err)
	}
	if dest.UpstreamURL.Path != "/v1beta/models/gemini-2.5-pro:streamGenerateContent" {
		t.Errorf("path = %q, want substituted", dest.UpstreamURL.Path)
	}
	if dest.UpstreamURL.Query().Get("alt") != "sse" {
		t.Errorf("query alt = %q, want sse", dest.UpstreamURL.Query().Get("alt"))
	}
	if got := dest.OutgoingHeaders.Get("x-goog-api-key"); got != "gm-key" {
		t.Errorf("x-goog-api-key = %q, want gm-key", got)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
