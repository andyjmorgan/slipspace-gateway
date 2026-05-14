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
    rules:
      - id: redact-emails
        priority: 100
        actions:
          - type: redactPattern
            pattern: "[\\w.]+@[\\w.]+"
            replacement: "[redacted]"
        behavior: continue
    resilience:
      mode: failover
      timeout_seconds: 30
      targets:
        - { provider: anthropic, weight: 1, order: 0 }
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
	writeFile(t, dir, "configurations.yaml", sampleConfigurations)
	writeFile(t, dir, "api_keys.yaml", sampleAPIKeys)
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
	if len(dev.Rules) != 1 {
		t.Errorf("expected 1 raw rule, got %d", len(dev.Rules))
	}
	if dev.Resilience == nil {
		t.Error("expected resilience preserved")
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
}

// TestLoad_ConfigDevFixtures exercises the real repo fixtures up to the
// validation step. They intentionally include `/v1/models` claimed by both
// openai.models and anthropic.models — which the path-collision rule rejects —
// so we assert ErrPathCollision rather than success. If the fixtures are ever
// disambiguated, flip this assertion to a happy-path check.
func TestLoad_ConfigDevFixtures(t *testing.T) {
	_, err := config.Load(context.Background(), "../../config-dev")
	if !errors.Is(err, config.ErrPathCollision) {
		t.Fatalf("config-dev: want ErrPathCollision (fixtures share /v1/models across providers), got %v", err)
	}
}

func TestLoad_DuplicateTopLevelKeyAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", sampleGateway)
	writeFile(t, dir, "b.yaml", sampleGateway)
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "configurations.yaml", sampleConfigurations)

	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrDuplicateTopLevelKey) {
		t.Fatalf("want ErrDuplicateTopLevelKey, got %v", err)
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
	writeFile(t, dir, "configurations.yaml", sampleConfigurations)
	writeFile(t, dir, "api_keys.yaml", `
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
	writeFile(t, dir, "configurations.yaml", `
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
	writeFile(t, dir, "configurations.yaml", `
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
	writeFile(t, dir, "configurations.yaml", `
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
	writeFile(t, dir, "configurations.yaml", `
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

func TestLoad_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrEmptyDirectory) {
		t.Fatalf("want ErrEmptyDirectory, got %v", err)
	}
}

func TestLoad_BadYAML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "bad.yaml", "::: not yaml :::\n  - [unclosed")
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrParse) {
		t.Fatalf("want ErrParse, got %v", err)
	}
}

func TestLoad_UnknownTopLevelKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "mystery.yaml", "mystery:\n  foo: bar\n")
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrUnknownTopLevelKey) {
		t.Fatalf("want ErrUnknownTopLevelKey, got %v", err)
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
	writeFile(t, dir, "configurations.yaml", sampleConfigurations)
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
	writeFile(t, dir, "configurations.yaml", sampleConfigurations)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrInvalidBind) {
		t.Fatalf("want ErrInvalidBind, got %v", err)
	}
}

func TestLoad_NonMappingTopLevel(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "bad.yaml", "- just\n- a\n- list\n")
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrParse) {
		t.Fatalf("want ErrParse, got %v", err)
	}
}

func TestLoad_YmlExtensionAlsoAccepted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yml", sampleProviders)
	writeFile(t, dir, "configurations.yaml", sampleConfigurations)
	resolved, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := resolved.Providers["openai"]; !ok {
		t.Error(".yml extension not accepted")
	}
}

func TestLoad_IgnoresNonYAMLFilesAndSubdirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "hello")
	writeFile(t, dir, ".hidden", "ignored")
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "subdir/inside.yaml", sampleConfigurations)
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "configurations.yaml", sampleConfigurations)

	resolved, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := resolved.Configurations["dev"]; !ok {
		t.Error("expected dev from configurations.yaml")
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
	writeFile(t, dir, "configurations.yaml", sampleConfigurations)
	if _, err := config.Load(context.Background(), dir); err != nil {
		t.Fatalf("empty bind should be allowed (no gateway block at all): %v", err)
	}
}

func TestLoad_AllowedEndpointsTrimmedNamesAreUnchanged(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "configurations.yaml", `
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
	writeFile(t, dir, "weird.yaml", "? [a, b]\n: foo\n")
	_, err := config.Load(context.Background(), dir)
	if err == nil || !strings.Contains(err.Error(), "config:") {
		t.Fatalf("expected config error, got %v", err)
	}
}

func TestLoad_DecodeErrors(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{"gateway_not_a_map", "gateway: \"not a map, a string\"\n"},
		{"providers_not_a_map", "providers: \"not a map\"\n"},
		{"configurations_not_a_map", "configurations: 42\n"},
		{"api_keys_not_a_list", "api_keys: \"not a list\"\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "bad.yaml", tc.yaml)
			_, err := config.Load(context.Background(), dir)
			if !errors.Is(err, config.ErrParse) {
				t.Fatalf("want ErrParse, got %v", err)
			}
		})
	}
}

func TestLoad_EmptyYAMLFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "empty.yaml", "")
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "configurations.yaml", sampleConfigurations)
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
	writeFile(t, dir, "configurations.yaml", sampleConfigurations)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrInvalidBind) {
		t.Fatalf("want ErrInvalidBind, got %v", err)
	}
}
