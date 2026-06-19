package main

import (
	"net/http"
	"testing"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/auth"
	"github.com/andyjmorgan/slipspace-gateway/internal/selection"
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
		dest, err := buildDestination(target, nil, auth.ModePassthrough, nil, "Bearer client-token", nil)
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
		dest, err := buildDestination(target, nil, auth.ModeManaged, nil, "Bearer client-token", nil)
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
		dest, err := buildDestination(noCred, nil, auth.ModeManaged, nil, "Bearer client-token", nil)
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

	dest, err := buildDestination(target, params, auth.ModeManaged, nil, "", nil)
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

// TestBuildDestination_ChangeApiKeyOverride covers the wired changeApiKey
// action: a literal APIKey override is minted with the post-rule provider's
// header format (openai → Bearer Authorization, anthropic → x-api-key, gemini →
// x-goog-api-key), and the UseSlipSpaceKey sentinel (empty-string override)
// forwards the inbound Authorization verbatim without stripping it.
func TestBuildDestination_ChangeApiKeyOverride(t *testing.T) {
	override := "sk-override" //nolint:gosec // synthetic test fixture, not a real credential

	t.Run("literal override mints per provider header format", func(t *testing.T) {
		cases := []struct {
			provider   string
			wantHeader string
			wantValue  string
		}{
			{"openai", "Authorization", "Bearer sk-override"},
			{"anthropic", "x-api-key", "sk-override"},
			{"gemini", "x-goog-api-key", "sk-override"},
		}
		for _, tc := range cases {
			t.Run(tc.provider, func(t *testing.T) {
				// Auth nil exercises the per-provider default format table
				// (auth.UpstreamCredentialHeader) — the contract's "post-rule
				// provider's header format".
				target := selection.Target{ //nolint:gosec // synthetic test fixture
					Provider:   tc.provider,
					BaseURL:    "https://example.invalid",
					Path:       "/v1/x",
					Credential: "sk-managed-default",
				}
				dest, err := buildDestination(target, nil, auth.ModeManaged, nil, "Bearer client-token", &override)
				if err != nil {
					t.Fatalf("buildDestination: %v", err)
				}
				if got := dest.OutgoingHeaders.Get(tc.wantHeader); got != tc.wantValue {
					t.Errorf("%s = %q, want %q (override minted with provider format)", tc.wantHeader, got, tc.wantValue)
				}
				// No other credential header carries a credential, and the
				// inbound bearer never leaks. (Compared canonically because the
				// minted header name canonicalises in http.Header.)
				want := http.CanonicalHeaderKey(tc.wantHeader)
				for _, h := range credentialHeaderNames {
					if http.CanonicalHeaderKey(h) == want {
						continue
					}
					if v := dest.OutgoingHeaders.Get(h); v != "" {
						t.Errorf("%s = %q, want unset", h, v)
					}
				}
			})
		}
	})

	t.Run("literal override honours endpoint auth convention", func(t *testing.T) {
		// An OpenAI-compat surface on anthropic carries an explicit Auth
		// convention; the override substitutes into that format, not the
		// provider-name default.
		target := selection.Target{ //nolint:gosec // synthetic test fixture
			Provider:   "anthropic",
			BaseURL:    "https://api.anthropic.com",
			Path:       "/openai/v1/chat/completions",
			Auth:       &contractsconfig.ProviderAuth{Header: "Authorization", Format: "Bearer {key}"},
			Credential: "sk-managed-default",
		}
		dest, err := buildDestination(target, nil, auth.ModeManaged, nil, "", &override)
		if err != nil {
			t.Fatalf("buildDestination: %v", err)
		}
		if got := dest.OutgoingHeaders.Get("Authorization"); got != "Bearer sk-override" {
			t.Errorf("Authorization = %q, want Bearer sk-override", got)
		}
	})

	t.Run("UseSlipSpaceKey sentinel forwards inbound authorization", func(t *testing.T) {
		empty := ""
		target := selection.Target{ //nolint:gosec // synthetic test fixture
			Provider:   "anthropic",
			BaseURL:    "https://api.anthropic.com",
			Path:       "/v1/messages",
			Auth:       &contractsconfig.ProviderAuth{Header: "x-api-key", Format: "{key}"},
			Credential: "sk-managed-default",
		}
		dest, err := buildDestination(target, nil, auth.ModeManaged, nil, "Bearer client-token", &empty)
		if err != nil {
			t.Fatalf("buildDestination: %v", err)
		}
		if got := dest.OutgoingHeaders.Get("Authorization"); got != "Bearer client-token" {
			t.Errorf("Authorization = %q, want inbound bearer forwarded", got)
		}
		// The managed credential must NOT be minted, and nothing is stripped.
		if got := dest.OutgoingHeaders.Get("x-api-key"); got != "" {
			t.Errorf("x-api-key = %q, want unset under UseSlipSpaceKey", got)
		}
		if contains(dest.DropHeaders, "Authorization") {
			t.Errorf("DropHeaders = %v, want Authorization NOT stripped", dest.DropHeaders)
		}
	})
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
