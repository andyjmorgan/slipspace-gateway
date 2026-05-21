package config_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

const sampleProviders = `
providers:
  openai:
    base_url: http://mockllm:5555
    endpoints:
      chat_completions:
        path: /v1/chat/completions
        method: [POST]
        accepted_paths: [/v1/chat/completions, /chat/completions]
        accepts_streaming: true
        request_kind: chat
      models:
        path: /v1/models
        method: [GET]
        accepted_paths: [/v1/models, /models]
        request_kind: passthrough
  anthropic:
    base_url: http://mockllm:5555
    required_headers:
      anthropic-version: "2023-06-01"
    endpoints:
      messages:
        path: /v1/messages
        method: [POST]
        accepted_paths: [/v1/messages, /messages]
        accepts_streaming: true
        request_kind: messages
`

const sampleConfigurations = `
configurations:
  dev:
    upstream_credentials:
      openai: sk-dev-mock
    rule_names:
      - redact-emails
    resilience_name: ha
    tags:
      tier: dev
`

//nolint:gosec // fixture secret for tests, not a real credential
const sampleAPIKeys = `
api_keys:
  - secret: sk_dev_aaaaaaaaaaaaaaaaaaaa
    name: "Local dev"
    configuration: dev
    enabled: true
`

const sampleRulesLibrary = `
rules:
  - name: redact-emails
    priority: 100
    condition:
      type: provider
      operator: Equals
      expectedProvider: openai
    actions:
      - type: setHeader
        headerName: X-Sluice-Redacted
        headerAction: Set
        headerValue: emails
    behavior: continue
`

const sampleResiliencePoliciesLibrary = `
resilience_policies:
  - name: ha
    mode: failover
    timeout_seconds: 30
    targets:
      - name: openai-primary
        provider: openai
        order: 1
`

// samplePolicy is the single-file policy bundle: configurations + api_keys +
// rules library + resilience_policies library. The four fragments are
// concatenated rather than maintained as a separate literal so individual
// pieces stay independently editable.
var samplePolicy = sampleConfigurations + sampleAPIKeys + sampleRulesLibrary + sampleResiliencePoliciesLibrary

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func makeFullDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", samplePolicy)
	return dir
}

func TestLoad_HappyPath(t *testing.T) {
	dir := makeFullDir(t)

	resolved, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if _, ok := resolved.Providers["openai"]; !ok {
		t.Fatal("missing openai provider")
	}
	if got := resolved.Providers["anthropic"].RequiredHeaders["anthropic-version"]; got != "2023-06-01" {
		t.Errorf("anthropic-version = %q", got)
	}

	dev, ok := resolved.Configurations["dev"]
	if !ok {
		t.Fatal("missing dev configuration")
	}
	if len(dev.RuleNames) != 1 || dev.RuleNames[0] != "redact-emails" {
		t.Errorf("rule_names = %v", dev.RuleNames)
	}
	if dev.ResilienceName == nil || *dev.ResilienceName != "ha" {
		t.Errorf("resilience_name = %v", dev.ResilienceName)
	}

	if len(resolved.APIKeys) != 1 {
		t.Fatalf("api_keys len = %d", len(resolved.APIKeys))
	}

	if got := resolved.SecretIndex["sk_dev_aaaaaaaaaaaaaaaaaaaa"]; got == nil || got.Name != "Local dev" {
		t.Errorf("SecretIndex lookup failed: %+v", got)
	}
	if got := resolved.ConfigurationIndex["dev"]; got == nil {
		t.Error("ConfigurationIndex missing dev")
	}
	if got, ok := resolved.RouteIndex["/v1/chat/completions"]; !ok || got.Provider != "openai" || got.Endpoint != "chat_completions" {
		t.Errorf("RouteIndex /v1/chat/completions = %+v", got)
	}
	if got, ok := resolved.RouteIndex["/chat/completions"]; !ok || got.Provider != "openai" {
		t.Errorf("RouteIndex /chat/completions = %+v", got)
	}

	if got := resolved.RuleIndex["redact-emails"]; got == nil || got.Name != "redact-emails" {
		t.Errorf("RuleIndex redact-emails = %+v", got)
	}
	if got := resolved.ResilienceIndex["ha"]; got == nil || got.Mode != "failover" {
		t.Errorf("ResilienceIndex ha = %+v", got)
	}
}

// TestLoad_ConfigDevFixtures exercises the real repo fixtures. The dev config
// uses prefix disambiguation: openai is the default (prefix_required=false),
// anthropic and gemini are prefixed (prefix_required=true), so the shared
// /v1/models accepted_path no longer collides.
func TestLoad_ConfigDevFixtures(t *testing.T) {
	resolved, err := config.Load(context.Background(), "../../config-dev")
	if err != nil {
		t.Fatalf("config-dev: %v", err)
	}
	if got, ok := resolved.RouteIndex["/v1/models"]; !ok || got.Provider != "openai" {
		t.Errorf("RouteIndex /v1/models = %+v, want openai.models", got)
	}
	if got, ok := resolved.RouteIndex["/anthropic/v1/models"]; !ok || got.Provider != "anthropic" {
		t.Errorf("RouteIndex /anthropic/v1/models = %+v, want anthropic.models", got)
	}
	if got, ok := resolved.RouteIndex["/gemini/v1beta/models"]; !ok || got.Provider != "gemini" {
		t.Errorf("RouteIndex /gemini/v1beta/models = %+v, want gemini.models", got)
	}
	// v1.0.6: bare /v1/messages IS expected to route (anthropic.messages
	// has prefix_optional: true). Asserted positively below.
	// v1.0.6: anthropic.messages exposes prefix_optional so the
	// native /v1/messages and /messages routes also resolve to
	// anthropic alongside the prefixed forms. The prefixed forms
	// stay routable because the provider's prefix_required is still
	// true for everything else.
	if got, ok := resolved.RouteIndex["/v1/messages"]; !ok || got.Provider != "anthropic" || got.Endpoint != "messages" {
		t.Errorf("RouteIndex /v1/messages = %+v, want anthropic.messages (prefix_optional)", got)
	}
	if got, ok := resolved.RouteIndex["/messages"]; !ok || got.Provider != "anthropic" || got.Endpoint != "messages" {
		t.Errorf("RouteIndex /messages = %+v, want anthropic.messages (prefix_optional)", got)
	}
	if got, ok := resolved.RouteIndex["/anthropic/v1/messages"]; !ok || got.Provider != "anthropic" || got.Endpoint != "messages" {
		t.Errorf("RouteIndex /anthropic/v1/messages = %+v, prefixed form must still route", got)
	}
	// v1.0.6: gemini.generate_content also exposes prefix_optional so a
	// vanilla Gemini SDK pointed at the gateway root resolves without the
	// /gemini prefix. The prefixed form stays routable, and gemini.models
	// + gemini.chat_completions remain prefix-only.
	if got, ok := resolved.RouteIndex["/v1beta/models/{model}:generateContent"]; !ok || got.Provider != "gemini" || got.Endpoint != "generate_content" {
		t.Errorf("RouteIndex /v1beta/models/{model}:generateContent = %+v, want gemini.generate_content (prefix_optional)", got)
	}
	if got, ok := resolved.RouteIndex["/gemini/v1beta/models/{model}:generateContent"]; !ok || got.Provider != "gemini" || got.Endpoint != "generate_content" {
		t.Errorf("RouteIndex /gemini/v1beta/models/{model}:generateContent = %+v, prefixed form must still route", got)
	}
	if _, ok := resolved.RouteIndex["/v1beta/models"]; ok {
		t.Errorf("RouteIndex /v1beta/models must not be claimed bare; gemini.models stays prefix-only")
	}
	// v1.0.2: OpenAI-compat chat surface on anthropic + gemini.
	if got, ok := resolved.RouteIndex["/anthropic/v1/chat/completions"]; !ok || got.Provider != "anthropic" || got.Endpoint != "chat_completions" {
		t.Errorf("RouteIndex /anthropic/v1/chat/completions = %+v, want anthropic.chat_completions", got)
	}
	if got, ok := resolved.RouteIndex["/gemini/v1beta/openai/chat/completions"]; !ok || got.Provider != "gemini" || got.Endpoint != "chat_completions" {
		t.Errorf("RouteIndex /gemini/v1beta/openai/chat/completions = %+v, want gemini.chat_completions", got)
	}
	anth := resolved.Providers["anthropic"].Endpoints["chat_completions"]
	if anth.AuthHeader != "Authorization" || anth.AuthFormat != "Bearer {key}" {
		t.Errorf("anthropic.chat_completions auth override missing: header=%q format=%q", anth.AuthHeader, anth.AuthFormat)
	}
	gem := resolved.Providers["gemini"].Endpoints["chat_completions"]
	if gem.AuthHeader != "Authorization" || gem.AuthFormat != "Bearer {key}" {
		t.Errorf("gemini.chat_completions auth override missing: header=%q format=%q", gem.AuthHeader, gem.AuthFormat)
	}
}

// TestLoad_PrefixOptional_Matrix walks every combination of provider-level
// prefix_required and endpoint-level prefix_optional to lock in the emission
// rule: bare AcceptedPaths emit when (NOT prefix_required) OR (prefix_optional);
// the prefixed form emits whenever the provider has a non-empty prefix. The
// matrix is exhaustive so a future change to emitRoutes that only fixes one
// quadrant can't slip through.
func TestLoad_PrefixOptional_Matrix(t *testing.T) {
	cases := []struct {
		name           string
		prefixRequired bool
		prefixOptional bool
		wantPaths      []string
		notWantPaths   []string
	}{
		{
			name:           "not_required_optional_off_emits_both",
			prefixRequired: false,
			prefixOptional: false,
			wantPaths:      []string{"/p/native", "/native"},
		},
		{
			name:           "not_required_optional_on_still_emits_both",
			prefixRequired: false,
			prefixOptional: true,
			wantPaths:      []string{"/p/native", "/native"},
		},
		{
			name:           "required_optional_off_prefixed_only",
			prefixRequired: true,
			prefixOptional: false,
			wantPaths:      []string{"/p/native"},
			notWantPaths:   []string{"/native"},
		},
		{
			name:           "required_optional_on_emits_both",
			prefixRequired: true,
			prefixOptional: true,
			wantPaths:      []string{"/p/native", "/native"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			providers := "providers:\n" +
				"  acme:\n" +
				"    prefix: p\n"
			if tc.prefixRequired {
				providers += "    prefix_required: true\n"
			}
			providers += "    base_url: http://mockllm:5555\n" +
				"    endpoints:\n" +
				"      generate:\n" +
				"        path: /native\n" +
				"        method: [POST]\n" +
				"        accepted_paths: [/native]\n" +
				"        request_kind: chat\n"
			if tc.prefixOptional {
				providers += "        prefix_optional: true\n"
			}
			writeFile(t, dir, "providers.yaml", providers)
			writeFile(t, dir, "policy.yaml", `
configurations:
  dev: {}
api_keys:
  - secret: sk_dev_xxx
    name: dev
    configuration: dev
    enabled: true
`)

			resolved, err := config.Load(context.Background(), dir)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			for _, p := range tc.wantPaths {
				if _, ok := resolved.RouteIndex[p]; !ok {
					t.Errorf("RouteIndex missing expected path %q (have %v)", p, mapKeys(resolved.RouteIndex))
				}
			}
			for _, p := range tc.notWantPaths {
				if _, ok := resolved.RouteIndex[p]; ok {
					t.Errorf("RouteIndex contains unwanted path %q", p)
				}
			}
		})
	}
}

func mapKeys(m map[string]config.Route) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestLoad_DuplicateKeyWithinSingleFile(t *testing.T) {
	dir := t.TempDir()
	body := sampleProviders + "\n" + sampleProviders
	writeFile(t, dir, "providers.yaml", body)

	_, err := config.Load(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLoad_UnknownConfiguration(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", sampleConfigurations+sampleRulesLibrary+sampleResiliencePoliciesLibrary+`
api_keys:
  - secret: sk_dev_xxx
    name: bad
    configuration: nonexistent
    enabled: true
`)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrUnknownConfiguration) {
		t.Fatalf("want ErrUnknownConfiguration, got %v", err)
	}
}

func TestLoad_PathCollision(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", `
providers:
  openai:
    base_url: http://a
    endpoints:
      chat:
        path: /v1/chat/completions
        method: [POST]
        accepted_paths: [/v1/chat/completions]
        request_kind: chat
  acme:
    base_url: http://b
    endpoints:
      chat:
        path: /v1/chat/completions
        method: [POST]
        accepted_paths: [/v1/chat/completions]
        request_kind: chat
`)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev: {}
`)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrPathCollision) {
		t.Fatalf("want ErrPathCollision, got %v", err)
	}
}

// TestLoad_PrefixResolvesCollision: two providers share an accepted_path but
// one requires a prefix — the bare path belongs to the default provider and
// the prefixed form routes to the other. No collision.
func TestLoad_PrefixResolvesCollision(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", `
providers:
  openai:
    prefix: openai
    prefix_required: false
    base_url: http://a
    endpoints:
      models:
        path: /v1/models
        method: [GET]
        accepted_paths: [/v1/models]
        request_kind: passthrough
  anthropic:
    prefix: anthropic
    prefix_required: true
    base_url: http://b
    endpoints:
      models:
        path: /v1/models
        method: [GET]
        accepted_paths: [/v1/models]
        request_kind: passthrough
`)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev: {}
`)
	resolved, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, ok := resolved.RouteIndex["/v1/models"]; !ok || got.Provider != "openai" {
		t.Errorf("bare /v1/models = %+v, want openai (default with optional prefix)", got)
	}
	if got, ok := resolved.RouteIndex["/openai/v1/models"]; !ok || got.Provider != "openai" {
		t.Errorf("/openai/v1/models = %+v, want openai (optional prefix attached)", got)
	}
	if got, ok := resolved.RouteIndex["/anthropic/v1/models"]; !ok || got.Provider != "anthropic" {
		t.Errorf("/anthropic/v1/models = %+v, want anthropic (required prefix)", got)
	}
}

// TestLoad_PrefixRequiredEmpty: prefix_required=true with no prefix value
// is a configuration error — the provider would be unreachable.
func TestLoad_PrefixRequiredEmpty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", `
providers:
  broken:
    prefix_required: true
    base_url: http://x
    endpoints:
      chat:
        path: /v1/chat/completions
        method: [POST]
        accepted_paths: [/v1/chat/completions]
        request_kind: chat
`)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev: {}
`)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrPrefixRequiredEmpty) {
		t.Fatalf("want ErrPrefixRequiredEmpty, got %v", err)
	}
}

// TestLoad_TwoDefaultProvidersCollide: two providers both with
// prefix_required=false (or no prefix at all) sharing an accepted_path is
// still a collision — the bare path is ambiguous.
func TestLoad_TwoDefaultProvidersCollide(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", `
providers:
  openai:
    prefix: openai
    prefix_required: false
    base_url: http://a
    endpoints:
      models:
        path: /v1/models
        method: [GET]
        accepted_paths: [/v1/models]
        request_kind: passthrough
  anthropic:
    prefix: anthropic
    prefix_required: false
    base_url: http://b
    endpoints:
      models:
        path: /v1/models
        method: [GET]
        accepted_paths: [/v1/models]
        request_kind: passthrough
`)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev: {}
`)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrPathCollision) {
		t.Fatalf("want ErrPathCollision (two default providers share bare /v1/models), got %v", err)
	}
}

// TestLoad_AuthOverride_EndpointHappyPath: an endpoint may override the
// auth header + format; the loader accepts and round-trips both fields.
func TestLoad_AuthOverride_EndpointHappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", `
providers:
  anthropic:
    prefix: anthropic
    prefix_required: true
    base_url: http://a
    endpoints:
      chat_completions:
        path: /v1/chat/completions
        method: [POST]
        accepted_paths: [/v1/chat/completions]
        request_kind: chat
        auth_header: Authorization
        auth_format: "Bearer {key}"
`)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev: {}
`)
	resolved, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := resolved.Providers["anthropic"].Endpoints["chat_completions"]
	if got.AuthHeader != "Authorization" || got.AuthFormat != "Bearer {key}" {
		t.Fatalf("auth override not preserved: header=%q format=%q", got.AuthHeader, got.AuthFormat)
	}
}

// TestLoad_AuthOverride_ProviderHappyPath: provider-level override is also
// accepted and round-trips.
func TestLoad_AuthOverride_ProviderHappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", `
providers:
  acme:
    prefix: acme
    prefix_required: true
    base_url: http://a
    auth_header: X-Acme-Token
    auth_format: "Token {key}"
    endpoints:
      chat:
        path: /v1/chat
        method: [POST]
        accepted_paths: [/v1/chat]
        request_kind: chat
`)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev: {}
`)
	resolved, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := resolved.Providers["acme"]
	if got.AuthHeader != "X-Acme-Token" || got.AuthFormat != "Token {key}" {
		t.Fatalf("provider auth override not preserved: header=%q format=%q", got.AuthHeader, got.AuthFormat)
	}
}

// TestLoad_AuthOverride_FormatWithoutHeaderRejected: auth_format alone is
// silently ignored at runtime, so the loader rejects it.
func TestLoad_AuthOverride_FormatWithoutHeaderRejected(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{
			name: "endpoint-level",
			yaml: `
providers:
  acme:
    prefix: acme
    prefix_required: true
    base_url: http://a
    endpoints:
      chat:
        path: /v1/chat
        method: [POST]
        accepted_paths: [/v1/chat]
        request_kind: chat
        auth_format: "Bearer {key}"
`,
		},
		{
			name: "provider-level",
			yaml: `
providers:
  acme:
    prefix: acme
    prefix_required: true
    base_url: http://a
    auth_format: "Bearer {key}"
    endpoints:
      chat:
        path: /v1/chat
        method: [POST]
        accepted_paths: [/v1/chat]
        request_kind: chat
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "providers.yaml", tc.yaml)
			writeFile(t, dir, "policy.yaml", `
configurations:
  dev: {}
`)
			_, err := config.Load(context.Background(), dir)
			if !errors.Is(err, config.ErrAuthFormatWithoutHeader) {
				t.Fatalf("want ErrAuthFormatWithoutHeader, got %v", err)
			}
		})
	}
}

// TestLoad_AuthOverride_InvalidFormatRejected: auth_format must carry
// exactly one {key} placeholder.
func TestLoad_AuthOverride_InvalidFormatRejected(t *testing.T) {
	cases := []struct {
		name   string
		format string
	}{
		{"missing placeholder", "Bearer no-placeholder"},
		{"double placeholder", "Bearer {key}{key}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "providers.yaml", `
providers:
  acme:
    prefix: acme
    prefix_required: true
    base_url: http://a
    endpoints:
      chat:
        path: /v1/chat
        method: [POST]
        accepted_paths: [/v1/chat]
        request_kind: chat
        auth_header: Authorization
        auth_format: "`+tc.format+`"
`)
			writeFile(t, dir, "policy.yaml", `
configurations:
  dev: {}
`)
			_, err := config.Load(context.Background(), dir)
			if !errors.Is(err, config.ErrInvalidAuthFormat) {
				t.Fatalf("want ErrInvalidAuthFormat, got %v", err)
			}
		})
	}
}

// TestLoad_AuthOverride_HeaderWithoutFormatAllowed: header alone is valid —
// the destination builder falls back to raw {key} substitution.
func TestLoad_AuthOverride_HeaderWithoutFormatAllowed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", `
providers:
  acme:
    prefix: acme
    prefix_required: true
    base_url: http://a
    endpoints:
      chat:
        path: /v1/chat
        method: [POST]
        accepted_paths: [/v1/chat]
        request_kind: chat
        auth_header: X-Acme-Token
`)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev: {}
`)
	if _, err := config.Load(context.Background(), dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestLoad_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrEmptyDirectory) {
		t.Fatalf("want ErrEmptyDirectory, got %v", err)
	}
}

func TestLoad_BadYAML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "policy.yaml", "::: not yaml :::\n  - [unclosed")
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrParse) {
		t.Fatalf("want ErrParse, got %v", err)
	}
}

func TestLoad_UnexpectedFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", sampleConfigurations+sampleRulesLibrary+sampleResiliencePoliciesLibrary)
	writeFile(t, dir, "random.yaml", "mystery:\n  foo: bar\n")
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrUnexpectedConfigFile) {
		t.Fatalf("want ErrUnexpectedConfigFile, got %v", err)
	}
}

func TestLoad_PolicyKeyInProvidersFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders+sampleRulesLibrary)
	writeFile(t, dir, "policy.yaml", sampleConfigurations)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrWrongFileForKey) {
		t.Fatalf("want ErrWrongFileForKey, got %v", err)
	}
}

func TestLoad_NoConfigurations(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrNoConfigurations) {
		t.Fatalf("want ErrNoConfigurations, got %v", err)
	}
}

func TestLoad_MissingDirectory(t *testing.T) {
	_, err := config.Load(context.Background(), "/nonexistent/path/zzzzz")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLoad_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := config.Load(ctx, t.TempDir())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestLoad_NonMappingTopLevel(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "policy.yaml", "- just\n- a\n- list\n")
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrParse) {
		t.Fatalf("want ErrParse, got %v", err)
	}
}

func TestLoad_IgnoresSubdirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "subdir/inside.yaml", sampleConfigurations)
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", sampleConfigurations+sampleRulesLibrary+sampleResiliencePoliciesLibrary)

	resolved, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := resolved.Configurations["dev"]; !ok {
		t.Error("expected dev from policy.yaml")
	}
}

func TestLoad_RejectsNonYAMLFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", samplePolicy)
	writeFile(t, dir, "README.md", "hello")
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrUnexpectedConfigFile) {
		t.Fatalf("want ErrUnexpectedConfigFile for README.md, got %v", err)
	}
}

func TestRoute_ZeroValue(t *testing.T) {
	var r config.Route
	if r.Provider != "" || r.Endpoint != "" {
		t.Error("zero-value Route should be empty")
	}
}

func TestResolvedConfig_ValidateDirect(t *testing.T) {
	rc := &config.ResolvedConfig{
		Configurations: contractsconfig.ConfigurationsConfig{
			"dev": {},
		},
		Providers: contractsconfig.ProvidersConfig{
			"openai": {
				BaseURL: "http://x",
				Endpoints: map[string]contractsconfig.Endpoint{
					"chat": {Path: "/v1/chat", AcceptedPaths: []string{"/v1/chat"}},
				},
			},
		},
	}
	if err := rc.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestResolvedConfig_ValidateEmpty(t *testing.T) {
	rc := &config.ResolvedConfig{}
	err := rc.Validate()
	if !errors.Is(err, config.ErrNoConfigurations) {
		t.Errorf("want ErrNoConfigurations, got %v", err)
	}
}

func TestLoad_NonScalarTopLevelKeyErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "policy.yaml", "? [a, b]\n: foo\n")
	_, err := config.Load(context.Background(), dir)
	if err == nil || !strings.Contains(err.Error(), "config:") {
		t.Fatalf("expected config error, got %v", err)
	}
}

func TestLoad_DecodeErrors(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		yaml     string
	}{
		{"providers_not_a_map", "providers.yaml", "providers: \"not a map\"\n"},
		{"configurations_not_a_map", "policy.yaml", "configurations: 42\n"},
		{"api_keys_not_a_list", "policy.yaml", "api_keys: \"not a list\"\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, tc.filename, tc.yaml)
			_, err := config.Load(context.Background(), dir)
			if !errors.Is(err, config.ErrParse) {
				t.Fatalf("want ErrParse, got %v", err)
			}
		})
	}
}

// TestLoad_EmptyProvidersFile: an empty providers.yaml is valid — the
// loader produces a resolved config with an empty RouteIndex. The
// gateway will 404 every request at runtime, but that is a routing
// concern, not a load-time error.
func TestLoad_EmptyProvidersFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", "")
	writeFile(t, dir, "policy.yaml", samplePolicy)
	resolved, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(resolved.RouteIndex) != 0 {
		t.Errorf("RouteIndex should be empty with no providers, got %v", resolved.RouteIndex)
	}
}

// libraryConfigurations references entries from the top-level rules library
// and resilience_policies library by name. upstream_credentials must
// cover every provider referenced by the bound resilience policy's
// targets; that cross-check is enforced at config-load time.
const libraryConfigurations = `
configurations:
  dev:
    upstream_credentials:
      anthropic: sk-anthropic-dev
    rule_names:
      - redact-emails
      - block-pii
    resilience_name: high-availability
`

const libraryRulesAndPolicies = `
rules:
  - name: redact-emails
    priority: 100
    condition:
      type: provider
      operator: Equals
      expectedProvider: openai
    actions:
      - type: changeProvider
        newProvider: anthropic
  - name: block-pii
    priority: 200
    condition:
      type: provider
      operator: Equals
      expectedProvider: openai
    actions:
      - type: returnStatusCode
        statusCode: 403
        bodyType: text
        body: blocked

resilience_policies:
  - name: high-availability
    mode: failover
    timeout_seconds: 30
    targets:
      - name: anthropic-only
        provider: anthropic
        order: 1
`

func TestLoad_LibraryHappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", libraryConfigurations+libraryRulesAndPolicies)

	resolved, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(resolved.Rules) != 2 {
		t.Fatalf("Rules len = %d", len(resolved.Rules))
	}
	if got := resolved.RuleIndex["redact-emails"]; got == nil || got.Name != "redact-emails" {
		t.Errorf("RuleIndex redact-emails = %+v", got)
	}
	if got := resolved.RuleIndex["block-pii"]; got == nil || got.Name != "block-pii" {
		t.Errorf("RuleIndex block-pii = %+v", got)
	}
	if len(resolved.ResiliencePolicies) != 1 {
		t.Fatalf("ResiliencePolicies len = %d", len(resolved.ResiliencePolicies))
	}
	if got := resolved.ResilienceIndex["high-availability"]; got == nil || got.Mode != "failover" {
		t.Errorf("ResilienceIndex high-availability = %+v", got)
	}
	dev := resolved.Configurations["dev"]
	if len(dev.RuleNames) != 2 || dev.RuleNames[0] != "redact-emails" {
		t.Errorf("dev.RuleNames = %v", dev.RuleNames)
	}
	if dev.ResilienceName == nil || *dev.ResilienceName != "high-availability" {
		t.Errorf("dev.ResilienceName = %v", dev.ResilienceName)
	}
}

// TestLoad_PerConfigurationRules_ListOrder confirms PerConfigurationRules
// preserves the rule_names list order verbatim. The YAML list is the only
// source of evaluation order — rule definition order in the `rules:`
// library is irrelevant to evaluation. The data-plane reads this slice
// directly and must not re-order on the hot path.
func TestLoad_PerConfigurationRules_ListOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev:
    rule_names:
      - alpha
      - charlie
      - bravo

rules:
  # Library definition order intentionally differs from the
  # rule_names attachment order — neither this block's order nor
  # any other implicit sort should affect evaluation order.
  - name: bravo
    condition: {type: provider, operator: Equals, expectedProvider: openai}
    actions: [{type: setHeader, headerName: X-Test, headerAction: Set, headerValue: bravo}]
  - name: alpha
    condition: {type: provider, operator: Equals, expectedProvider: openai}
    actions: [{type: setHeader, headerName: X-Test, headerAction: Set, headerValue: alpha}]
  - name: charlie
    condition: {type: provider, operator: Equals, expectedProvider: openai}
    actions: [{type: setHeader, headerName: X-Test, headerAction: Set, headerValue: charlie}]
`)

	resolved, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := resolved.PerConfigurationRules["dev"]
	if len(got) != 3 {
		t.Fatalf("PerConfigurationRules[dev] len = %d", len(got))
	}
	want := []string{"alpha", "charlie", "bravo"}
	for i, w := range want {
		if got[i].Name != w {
			t.Errorf("PerConfigurationRules[dev][%d] = %q, want %q (rule_names list order must be preserved)", i, got[i].Name, w)
		}
	}
}

// TestLoad_PerConfigurationRules_EmptyRuleNames keeps the absence-of-rules
// behaviour explicit: configurations with no rule_names get no entry in
// PerConfigurationRules.
func TestLoad_PerConfigurationRules_EmptyRuleNames(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", `
configurations:
  bare: {}
`)

	resolved, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, ok := resolved.PerConfigurationRules["bare"]; ok {
		t.Errorf("PerConfigurationRules[bare] should be absent, got %v", got)
	}
}

func TestLoad_LibraryUnknownRuleName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev:
    rule_names: [ghost]
`+libraryRulesAndPolicies)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrUnknownRuleName) {
		t.Fatalf("want ErrUnknownRuleName, got %v", err)
	}
}

func TestLoad_LibraryUnknownResilienceName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev:
    resilience_name: ghost
`+libraryRulesAndPolicies)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrUnknownResilienceName) {
		t.Fatalf("want ErrUnknownResilienceName, got %v", err)
	}
}

func TestLoad_RuleUseResiliencePolicy_UnknownName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev: {}

rules:
  - name: pick-policy
    priority: 1
    condition: {type: provider, operator: Equals, expectedProvider: openai}
    actions:
      - type: useResiliencePolicy
        policyName: never-declared
`)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrUnknownResilienceName) {
		t.Fatalf("want ErrUnknownResilienceName, got %v", err)
	}
}

func TestLoad_RuleUseResiliencePolicy_KnownName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev: {}

rules:
  - name: pick-policy
    priority: 1
    condition: {type: provider, operator: Equals, expectedProvider: openai}
    actions:
      - type: useResiliencePolicy
        policyName: ha

resilience_policies:
  - name: ha
    mode: none
`)
	if _, err := config.Load(context.Background(), dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestLoad_RuleUseResiliencePolicy_EmptyPolicyNameIsClear(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev: {}

rules:
  - name: clear-policy
    priority: 1
    condition: {type: provider, operator: Equals, expectedProvider: openai}
    actions:
      - type: useResiliencePolicy
        policyName: ""
`)
	if _, err := config.Load(context.Background(), dir); err != nil {
		t.Fatalf("empty PolicyName should be a valid clear-action, got %v", err)
	}
}

func TestLoad_LibraryDuplicateRuleName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev: {}

rules:
  - name: dup
    priority: 1
    condition: {type: provider, operator: Equals, expectedProvider: openai}
    actions: [{type: changeProvider, newProvider: anthropic}]
  - name: dup
    priority: 2
    condition: {type: provider, operator: Equals, expectedProvider: openai}
    actions: [{type: changeProvider, newProvider: anthropic}]
`)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrDuplicateRuleName) {
		t.Fatalf("want ErrDuplicateRuleName, got %v", err)
	}
}

func TestLoad_LibraryDuplicateRuleID(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev: {}

rules:
  - id: 550e8400-e29b-41d4-a716-446655440000
    name: a
    priority: 1
    condition: {type: provider, operator: Equals, expectedProvider: openai}
    actions: [{type: changeProvider, newProvider: anthropic}]
  - id: 550e8400-e29b-41d4-a716-446655440000
    name: b
    priority: 2
    condition: {type: provider, operator: Equals, expectedProvider: openai}
    actions: [{type: changeProvider, newProvider: anthropic}]
`)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrDuplicateRuleID) {
		t.Fatalf("want ErrDuplicateRuleID, got %v", err)
	}
}

func TestLoad_LibraryDuplicateResilienceName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev: {}

resilience_policies:
  - name: dup
    mode: none
  - name: dup
    mode: none
`)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrDuplicateResilienceName) {
		t.Fatalf("want ErrDuplicateResilienceName, got %v", err)
	}
}

func TestLoad_LibraryDuplicateResilienceID(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev: {}

resilience_policies:
  - id: 550e8400-e29b-41d4-a716-446655440000
    name: a
    mode: none
  - id: 550e8400-e29b-41d4-a716-446655440000
    name: b
    mode: none
`)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrDuplicateResilienceID) {
		t.Fatalf("want ErrDuplicateResilienceID, got %v", err)
	}
}
