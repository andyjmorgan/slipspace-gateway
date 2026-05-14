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

const sampleGateway = `
gateway:
  http:
    bind: 0.0.0.0:8585
  shutdown:
    drain_timeout_seconds: 300
  reporting:
    enabled: true
    nats:
      url: nats://nats:4222
      stream: GATEWAY_EVENTS
      bucket: GATEWAY_EVENT_STASH
      stash_threshold_bytes: 786432
      stream_max_age_minutes: 60
      bucket_ttl_minutes: 75
      publish_queue_size: 10000
  observability:
    prometheus:
      enabled: true
      bind: 0.0.0.0:9090
    otlp:
      enabled: false
      endpoint: http://otel-collector:4317
      protocol: grpc
    logging:
      format: json
      level: debug
`

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
    allowed_endpoints:
      - openai.chat_completions
      - openai.models
      - anthropic.messages
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
      - provider: openai
        order: 0
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
	writeFile(t, dir, "gateway.yaml", sampleGateway)
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
	if resolved.Gateway.HTTP.Bind != "0.0.0.0:8585" {
		t.Errorf("gateway.http.bind = %q", resolved.Gateway.HTTP.Bind)
	}
	if !resolved.Gateway.Reporting.Enabled {
		t.Error("reporting should be enabled")
	}
	if resolved.Gateway.Reporting.NATS.StashThresholdBytes != 786432 {
		t.Errorf("stash_threshold_bytes = %d", resolved.Gateway.Reporting.NATS.StashThresholdBytes)
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
	if len(dev.AllowedEndpoints) != 3 {
		t.Errorf("allowed_endpoints = %v", dev.AllowedEndpoints)
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
	if _, ok := resolved.RouteIndex["/v1/messages"]; ok {
		t.Errorf("anthropic has prefix_required=true; bare /v1/messages must not be in the route index")
	}
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
	writeFile(t, dir, "gateway.yaml", sampleGateway)
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

func TestLoad_AllowedEndpointMissingProvider(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev:
    allowed_endpoints:
      - bedrock.invoke
`)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrEndpointNotInProvider) {
		t.Fatalf("want ErrEndpointNotInProvider, got %v", err)
	}
}

func TestLoad_AllowedEndpointMissingEndpoint(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev:
    allowed_endpoints:
      - openai.embeddings
`)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrEndpointNotInProvider) {
		t.Fatalf("want ErrEndpointNotInProvider, got %v", err)
	}
}

func TestLoad_MalformedAllowedEndpoint(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev:
    allowed_endpoints:
      - openai_chat
`)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrMalformedAllowedEndpoint) {
		t.Fatalf("want ErrMalformedAllowedEndpoint, got %v", err)
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
  dev:
    allowed_endpoints:
      - openai.chat
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
  dev:
    allowed_endpoints:
      - openai.models
      - anthropic.models
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
  dev:
    allowed_endpoints:
      - broken.chat
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
  dev:
    allowed_endpoints:
      - openai.models
`)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrPathCollision) {
		t.Fatalf("want ErrPathCollision (two default providers share bare /v1/models), got %v", err)
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

func TestLoad_GatewayKeyInPolicyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", sampleConfigurations+`
gateway:
  http:
    bind: 0.0.0.0:8585
`)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrWrongFileForKey) {
		t.Fatalf("want ErrWrongFileForKey, got %v", err)
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

func TestLoad_InvalidBind(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "gateway.yaml", `
gateway:
  http:
    bind: "not a host port"
`)
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", samplePolicy)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrInvalidBind) {
		t.Fatalf("want ErrInvalidBind, got %v", err)
	}
}

func TestLoad_InvalidBindPortNotNumeric(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "gateway.yaml", `
gateway:
  http:
    bind: "0.0.0.0:abc"
`)
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", samplePolicy)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrInvalidBind) {
		t.Fatalf("want ErrInvalidBind, got %v", err)
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
			"dev": {AllowedEndpoints: []string{"openai.chat"}},
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

func TestLoad_BindEmpty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", samplePolicy)
	if _, err := config.Load(context.Background(), dir); err != nil {
		t.Fatalf("empty bind should be allowed (no gateway block at all): %v", err)
	}
}

func TestLoad_AllowedEndpointsTrimmedNamesAreUnchanged(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev:
    allowed_endpoints:
      - " openai.chat_completions"
`)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrEndpointNotInProvider) {
		t.Fatalf("expected unknown-endpoint error for whitespace-prefixed name, got %v", err)
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
		{"gateway_not_a_map", "gateway.yaml", "gateway: \"not a map, a string\"\n"},
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

func TestLoad_EmptyYAMLFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "gateway.yaml", "")
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", samplePolicy)
	resolved, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := resolved.Providers["openai"]; !ok {
		t.Error("openai missing")
	}
}

func TestLoad_BindMissingPort(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "gateway.yaml", `
gateway:
  http:
    bind: ":"
`)
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", samplePolicy)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrInvalidBind) {
		t.Fatalf("want ErrInvalidBind, got %v", err)
	}
}

// libraryConfigurations references entries from the top-level rules library
// and resilience_policies library by name.
const libraryConfigurations = `
configurations:
  dev:
    allowed_endpoints:
      - openai.chat_completions
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
      - provider: anthropic
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
	if got := resolved.RuleIndex["block-pii"]; got == nil || got.Priority != 200 {
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

func TestLoad_LibraryUnknownRuleName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev:
    allowed_endpoints: [openai.chat_completions]
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
    allowed_endpoints: [openai.chat_completions]
    resilience_name: ghost
`+libraryRulesAndPolicies)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrUnknownResilienceName) {
		t.Fatalf("want ErrUnknownResilienceName, got %v", err)
	}
}

func TestLoad_LibraryDuplicateRuleName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", `
configurations:
  dev:
    allowed_endpoints: [openai.chat_completions]

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
  dev:
    allowed_endpoints: [openai.chat_completions]

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
  dev:
    allowed_endpoints: [openai.chat_completions]

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
  dev:
    allowed_endpoints: [openai.chat_completions]

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
