package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/auth"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
)

// The credential mint site (credentialStrategy + credentialFor) is
// the single source of truth for "which upstream credential do we
// send, and with what header" — PR #13's load-bearing invariant. The
// function's own godoc declares an 8-row decision table over
// (authResult.Mode, UpstreamCredentialOverride, configured cred).
//
// Before this file, only the "managed + cred present" row was
// exercised by existing tests. The untested rows include
// credStripNoSet — the path designed to prevent the inbound
// customer Bearer from leaking to a private endpoint when no upstream
// credential is configured. A silent regression there would forward
// customer tokens to whatever the routing resolved (qwen, etc.) —
// the precise bug PR #12+#13 were built to prevent.

func ptr(s string) *string { return &s }

func TestCredentialStrategy_DecisionTable(t *testing.T) {
	t.Parallel()
	emptyStr := ""

	makeConfig := func(creds map[string]string) *contractsconfig.Configuration {
		return &contractsconfig.Configuration{UpstreamCredentials: creds}
	}

	cases := []struct {
		name     string
		auth     auth.AuthResult
		state    *rules.MutableState
		expected credStrategy
	}{
		{
			name:     "managed + no override + cred present → set from provider",
			auth:     auth.AuthResult{Mode: auth.ModeManaged, Configuration: makeConfig(map[string]string{"openai": "sk-up"})},
			state:    &rules.MutableState{Provider: "openai"},
			expected: credSetFromProvider,
		},
		{
			name:     "managed + no override + cred empty string → strip (private endpoint)",
			auth:     auth.AuthResult{Mode: auth.ModeManaged, Configuration: makeConfig(map[string]string{"qwen": ""})},
			state:    &rules.MutableState{Provider: "qwen"},
			expected: credStripNoSet,
		},
		{
			name:     "managed + no override + cred missing entirely → strip (private endpoint)",
			auth:     auth.AuthResult{Mode: auth.ModeManaged, Configuration: makeConfig(map[string]string{"openai": "sk-up"})},
			state:    &rules.MutableState{Provider: "qwen"}, // no qwen key
			expected: credStripNoSet,
		},
		{
			name:     "managed + Configuration nil → strip (no creds available)",
			auth:     auth.AuthResult{Mode: auth.ModeManaged, Configuration: nil},
			state:    &rules.MutableState{Provider: "openai"},
			expected: credStripNoSet,
		},
		{
			name:     "managed + rule override non-empty → set from provider (rule rewrite)",
			auth:     auth.AuthResult{Mode: auth.ModeManaged, Configuration: makeConfig(map[string]string{"openai": "sk-up"})},
			state:    &rules.MutableState{Provider: "openai", UpstreamCredentialOverride: ptr("sk-rule")},
			expected: credSetFromProvider,
		},
		{
			name:     "managed + override == &\"\" (UseSluiceKey sentinel) → forward inbound",
			auth:     auth.AuthResult{Mode: auth.ModeManaged, Configuration: makeConfig(map[string]string{"openai": "sk-up"})},
			state:    &rules.MutableState{Provider: "openai", UpstreamCredentialOverride: &emptyStr},
			expected: credForwardInbound,
		},
		{
			name:     "passthrough + no override → forward inbound",
			auth:     auth.AuthResult{Mode: auth.ModePassthrough},
			state:    &rules.MutableState{Provider: "anthropic"},
			expected: credForwardInbound,
		},
		{
			name:     "passthrough + rule override non-empty → set from provider (rule rewrite even in passthrough)",
			auth:     auth.AuthResult{Mode: auth.ModePassthrough},
			state:    &rules.MutableState{Provider: "anthropic", UpstreamCredentialOverride: ptr("sk-rule")},
			expected: credSetFromProvider,
		},
		{
			name:     "passthrough + override == &\"\" (UseSluiceKey, redundant) → forward inbound",
			auth:     auth.AuthResult{Mode: auth.ModePassthrough},
			state:    &rules.MutableState{Provider: "anthropic", UpstreamCredentialOverride: &emptyStr},
			expected: credForwardInbound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := credentialStrategy(tc.auth, tc.state)
			if got != tc.expected {
				t.Errorf("credentialStrategy = %v, want %v", got, tc.expected)
			}
		})
	}
}

// TestCredentialFor exercises the value-returning side of the mint
// decision: which credential string does the destination builder pick
// up once the strategy has decided?
func TestCredentialFor(t *testing.T) {
	t.Parallel()
	emptyStr := ""

	cases := []struct {
		name string
		auth auth.AuthResult
		st   *rules.MutableState
		want string
	}{
		{
			name: "override non-empty wins (rule rewrite)",
			auth: auth.AuthResult{Mode: auth.ModeManaged, Configuration: &contractsconfig.Configuration{
				UpstreamCredentials: map[string]string{"openai": "sk-config"},
			}},
			st:   &rules.MutableState{Provider: "openai", UpstreamCredentialOverride: ptr("sk-rule")},
			want: "sk-rule",
		},
		{
			name: "override == &\"\" returns empty (sentinel) — destination builder will forward inbound",
			auth: auth.AuthResult{Mode: auth.ModeManaged, Configuration: &contractsconfig.Configuration{
				UpstreamCredentials: map[string]string{"openai": "sk-config"},
			}},
			st:   &rules.MutableState{Provider: "openai", UpstreamCredentialOverride: &emptyStr},
			want: "",
		},
		{
			name: "no override + Configuration present → reads UpstreamCredentials[Provider]",
			auth: auth.AuthResult{Mode: auth.ModeManaged, Configuration: &contractsconfig.Configuration{
				UpstreamCredentials: map[string]string{"anthropic": "sk-ant-config"},
			}},
			st:   &rules.MutableState{Provider: "anthropic"},
			want: "sk-ant-config",
		},
		{
			name: "no override + Configuration nil → empty string",
			auth: auth.AuthResult{Mode: auth.ModeManaged, Configuration: nil},
			st:   &rules.MutableState{Provider: "openai"},
			want: "",
		},
		{
			name: "no override + Configuration present but key missing → empty string",
			auth: auth.AuthResult{Mode: auth.ModeManaged, Configuration: &contractsconfig.Configuration{
				UpstreamCredentials: map[string]string{"openai": "sk-config"},
			}},
			st:   &rules.MutableState{Provider: "qwen"},
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := credentialFor(tc.auth, tc.st); got != tc.want {
				t.Errorf("credentialFor = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBuildDestination_CascadeOverrides asserts that the PR #21
// cascade's two strongest write paths actually ride the wire:
//
//  1. ChangeUrl (state.UpstreamURL non-nil) → outbound dial hits the
//     overridden URL, NOT the endpoint template.
//  2. SetHeader (state.OutgoingHeaders) → both credential-name
//     collisions (rule wins over the credential decision) AND
//     non-credential keys (appended verbatim) ride upstream.
//
// Without this test, the cascade has condition-side coverage (does
// the rule fire) but no assertion that its mutations actually
// produce a different outgoing request.
func TestBuildDestination_CascadeOverrides(t *testing.T) {
	t.Parallel()
	provider := contractsconfig.Provider{
		BaseURL: "http://upstream",
		Endpoints: map[string]contractsconfig.Endpoint{
			"chat_completions": {Path: "/v1/chat/completions", Method: []string{http.MethodPost}},
		},
	}
	endpoint := provider.Endpoints["chat_completions"]
	cfg := &contractsconfig.Configuration{UpstreamCredentials: map[string]string{"openai": "sk-config"}}
	authResult := auth.AuthResult{Mode: auth.ModeManaged, Configuration: cfg, Provider: "openai", Endpoint: "chat_completions"}

	t.Run("endpoint.Path empty mirrors state.MatchedPath", func(t *testing.T) {
		// Gemini folds :generateContent and :streamGenerateContent into
		// one endpoint by listing both in accepted_paths and omitting
		// path. The destination builder must mirror whichever inbound
		// accepted_path matched to upstream so the routing variant is
		// preserved end-to-end.
		mirrorProvider := contractsconfig.Provider{
			BaseURL: "http://upstream",
			Endpoints: map[string]contractsconfig.Endpoint{
				"generate_content": {Method: []string{http.MethodPost}}, // Path intentionally empty
			},
		}
		mirrorEndpoint := mirrorProvider.Endpoints["generate_content"]
		mirrorAuth := auth.AuthResult{Mode: auth.ModeManaged, Configuration: &contractsconfig.Configuration{}, Provider: "gemini", Endpoint: "generate_content"}

		state := &rules.MutableState{
			Provider:        "gemini",
			Endpoint:        "generate_content",
			MatchedPath:     "/v1beta/models/{model}:streamGenerateContent",
			PathParams:      map[string]string{"model": "gemini-1.5-flash"},
			OutgoingHeaders: http.Header{},
		}
		req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-1.5-flash:streamGenerateContent", nil)

		dest, err := buildDestination(mirrorProvider, mirrorEndpoint, state, mirrorAuth, req)
		if err != nil {
			t.Fatalf("buildDestination: %v", err)
		}
		if got, want := dest.UpstreamURL.Path, "/v1beta/models/gemini-1.5-flash:streamGenerateContent"; got != want {
			t.Errorf("UpstreamURL.Path = %q, want %q (mirrored from MatchedPath)", got, want)
		}
	})

	t.Run("endpoint.Path set wins over state.MatchedPath", func(t *testing.T) {
		// When the operator declares an explicit Path, the matched
		// accepted_path must NOT bleed into the upstream destination —
		// the template is the source of truth.
		state := &rules.MutableState{
			Provider:        "openai",
			Endpoint:        "chat_completions",
			MatchedPath:     "/some/other/path", // should be ignored
			OutgoingHeaders: http.Header{},
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

		dest, err := buildDestination(provider, endpoint, state, authResult, req)
		if err != nil {
			t.Fatalf("buildDestination: %v", err)
		}
		if got, want := dest.UpstreamURL.Path, "/v1/chat/completions"; got != want {
			t.Errorf("UpstreamURL.Path = %q, want %q (endpoint.Path wins)", got, want)
		}
	})

	t.Run("ChangeUrl wins over endpoint template", func(t *testing.T) {
		overrideURL, _ := url.Parse("http://other-upstream.local/redirect/path")
		state := &rules.MutableState{
			Provider:        "openai",
			Endpoint:        "chat_completions",
			UpstreamURL:     overrideURL,
			OutgoingHeaders: http.Header{},
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

		dest, err := buildDestination(provider, endpoint, state, authResult, req)
		if err != nil {
			t.Fatalf("buildDestination: %v", err)
		}
		if got := dest.UpstreamURL.String(); got != "http://other-upstream.local/redirect/path" {
			t.Errorf("UpstreamURL = %q, want rule override", got)
		}
	})

	t.Run("Rule SetHeader on credential-name overlays the credential decision", func(t *testing.T) {
		state := &rules.MutableState{
			Provider: "openai",
			Endpoint: "chat_completions",
			OutgoingHeaders: http.Header{
				auth.HeaderAuthorization: []string{"Bearer sk-rule-override"},
			},
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

		dest, err := buildDestination(provider, endpoint, state, authResult, req)
		if err != nil {
			t.Fatalf("buildDestination: %v", err)
		}
		// Credential decision would put "Bearer sk-config"; rule write
		// must win.
		if got := dest.OutgoingHeaders.Get(auth.HeaderAuthorization); got != "Bearer sk-rule-override" {
			t.Errorf("Authorization = %q, want rule-override value (Set replaces, not appends)", got)
		}
	})

	t.Run("Rule SetHeader on non-credential keys passes through", func(t *testing.T) {
		state := &rules.MutableState{
			Provider: "openai",
			Endpoint: "chat_completions",
			OutgoingHeaders: http.Header{
				"X-Custom-Marker": []string{"premium"},
				"X-Trace-Id":      []string{"abc-123"},
			},
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

		dest, err := buildDestination(provider, endpoint, state, authResult, req)
		if err != nil {
			t.Fatalf("buildDestination: %v", err)
		}
		if got := dest.OutgoingHeaders.Get("X-Custom-Marker"); got != "premium" {
			t.Errorf("X-Custom-Marker = %q, want premium", got)
		}
		if got := dest.OutgoingHeaders.Get("X-Trace-Id"); got != "abc-123" {
			t.Errorf("X-Trace-Id = %q, want abc-123", got)
		}
		// Credential header still set from the credential decision —
		// rule didn't touch it.
		if got := dest.OutgoingHeaders.Get(auth.HeaderAuthorization); got != "Bearer sk-config" {
			t.Errorf("Authorization = %q, want Bearer sk-config (credential decision intact)", got)
		}
	})

	t.Run("QueryAdditions overlay on the upstream URL", func(t *testing.T) {
		state := &rules.MutableState{
			Provider:        "openai",
			Endpoint:        "chat_completions",
			OutgoingHeaders: http.Header{},
			QueryAdditions:  []rules.QueryAddition{{Key: "trace", Value: "on"}, {Key: "cache", Value: "off"}},
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

		dest, err := buildDestination(provider, endpoint, state, authResult, req)
		if err != nil {
			t.Fatalf("buildDestination: %v", err)
		}
		q := dest.UpstreamURL.Query()
		if q.Get("trace") != "on" || q.Get("cache") != "off" {
			t.Errorf("query = %v, want trace=on + cache=off", q)
		}
	})

	t.Run("credForwardInbound re-injects the inbound Authorization", func(t *testing.T) {
		// Passthrough mode means the credential decision is
		// credForwardInbound; the forwarder strips inbound
		// Authorization unconditionally, so buildDestination must
		// re-inject the inbound value via OutgoingHeaders.
		ptState := &rules.MutableState{Provider: "anthropic", Endpoint: "chat_completions", OutgoingHeaders: http.Header{}}
		ptAuth := auth.AuthResult{Mode: auth.ModePassthrough}
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.Header.Set(auth.HeaderAuthorization, "Bearer customer-byok-token")

		dest, err := buildDestination(provider, endpoint, ptState, ptAuth, req)
		if err != nil {
			t.Fatalf("buildDestination: %v", err)
		}
		if got := dest.OutgoingHeaders.Get(auth.HeaderAuthorization); got != "Bearer customer-byok-token" {
			t.Errorf("Authorization = %q, want re-injected inbound", got)
		}
	})

	t.Run("credStripNoSet drops every credential header without setting one", func(t *testing.T) {
		state := &rules.MutableState{Provider: "qwen", Endpoint: "chat_completions", OutgoingHeaders: http.Header{}}
		// no Configuration → credStripNoSet
		auth0 := auth.AuthResult{Mode: auth.ModeManaged, Configuration: nil}
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.Header.Set(auth.HeaderAuthorization, "Bearer sk-customer-inbound")

		dest, err := buildDestination(provider, endpoint, state, auth0, req)
		if err != nil {
			t.Fatalf("buildDestination: %v", err)
		}
		// No Authorization set on outgoing — the credential decision
		// produced no Set; the inbound Authorization is in the
		// DropHeaders list so the forwarder strips it before send.
		if got := dest.OutgoingHeaders.Get(auth.HeaderAuthorization); got != "" {
			t.Errorf("Authorization = %q, want empty on credStripNoSet path", got)
		}
		// Every credential header name should be in DropHeaders.
		dropSet := map[string]bool{}
		for _, h := range dest.DropHeaders {
			dropSet[h] = true
		}
		for _, h := range credentialHeaderNames {
			if !dropSet[h] {
				t.Errorf("DropHeaders missing %q (credential leak risk on private endpoint)", h)
			}
		}
	})
}
