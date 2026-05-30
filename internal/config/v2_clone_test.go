package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	contractsres "github.com/andyjmorgan/sluice-gateway/contracts/resilience"
	rulescontract "github.com/andyjmorgan/sluice-gateway/contracts/rules"
)

func v2Fixture() *ResolvedConfigV2 {
	r := &ResolvedConfigV2{
		Backends: contractsconfig.BackendsConfig{
			"openai": {
				BaseURL:         "https://api.openai.com",
				RequiredHeaders: map[string]string{"x-org": "acme"},
				Query:           map[string]string{"api-version": "2025-01"},
				Protocols: map[string]contractsconfig.BackendProtocol{
					"chat": {Path: "/v1/chat/completions", Auth: &contractsconfig.BackendAuth{Header: "Authorization", Format: "Bearer {key}"}},
				},
				Passthrough: map[string]contractsconfig.PassthroughFamily{
					"batches": {
						Auth:  &contractsconfig.BackendAuth{Header: "x-api-key", Format: "{key}"},
						Paths: []contractsconfig.PassthroughPath{{Match: "/v1/batches", Methods: []string{"POST"}}},
					},
				},
			},
		},
		Groups: contractsconfig.GroupsConfig{
			"ha": {
				Mode:               contractsres.ModeFailover,
				FailureStatusCodes: []int{503},
				CircuitBreaker:     &contractsres.CircuitBreakerConfig{Enabled: true, FailureThreshold: 3, SamplingDurationSeconds: 30, CooldownSeconds: 60, HalfOpenSuccessThreshold: 2},
				Targets:            []contractsconfig.Target{{Backend: "openai", Query: map[string]string{"q": "1"}}},
			},
		},
		Configurations: map[string]contractsconfig.ConfigurationV2{
			"dev": {
				Credentials:         map[string]string{"openai": "sk-dev"},
				Bindings:            []contractsconfig.Binding{{Protocol: "chat", Models: []string{"gpt-*"}, Backend: "openai", Tags: []string{"t"}}},
				PassthroughBindings: []contractsconfig.PassthroughBinding{{Family: "batches", Backend: "openai"}},
				RuleNames:           []string{"r1"},
				Tags:                map[string]string{"tier": "dev"},
				ConnectorBindings:   []contractsconfig.ConnectorBinding{{Connector: "artifacts", Sampling: 1.0}},
			},
		},
		Connectors: contractsconfig.ConnectorsConfig{
			{Name: "artifacts", Type: "webhook", URL: "http://sink", SecretRef: "env:WH", TimeoutMS: 5000},
		},
		APIKeys: contractsconfig.APIKeysConfig{
			{Secret: "sk_dev_1", Name: "k1", Configuration: "dev", Enabled: true}, //nolint:gosec // test fixture
		},
		Rules: []rulescontract.RuleContract{
			{
				Name:      "r1",
				Condition: &rulescontract.ProviderCondition{Type: "provider", Operator: rulescontract.EnumEquals, ExpectedProvider: "openai"},
				Actions:   []rulescontract.Action{&rulescontract.AddTagAction{Type: "addTag", Tag: "x"}},
				Behavior:  rulescontract.BehaviorContinue,
			},
		},
	}
	r.buildIndexes()
	return r
}

func TestResolvedConfigV2_CloneIndependence(t *testing.T) {
	orig := v2Fixture()
	clone := orig.Clone()

	// Mutating the clone's rules + a configuration must not bleed into orig.
	clone.Rules[0].Name = "renamed"
	devClone := clone.Configurations["dev"]
	devClone.RuleNames[0] = "renamed"
	devClone.Credentials["openai"] = "sk-changed"

	if orig.Rules[0].Name != "r1" {
		t.Errorf("orig rule mutated: %q", orig.Rules[0].Name)
	}
	if orig.Configurations["dev"].RuleNames[0] != "r1" {
		t.Errorf("orig RuleNames mutated: %v", orig.Configurations["dev"].RuleNames)
	}
	if orig.Configurations["dev"].Credentials["openai"] != "sk-dev" {
		t.Errorf("orig credentials mutated: %q", orig.Configurations["dev"].Credentials["openai"])
	}
	// Backend deep copy: mutating clone's protocol auth must not touch orig.
	be := clone.Backends["openai"]
	be.Protocols["chat"].Auth.Header = "X-Changed"
	if orig.Backends["openai"].Protocols["chat"].Auth.Header != "Authorization" {
		t.Errorf("orig backend auth mutated")
	}
}

func TestResolvedConfigV2_CloneNil(t *testing.T) {
	var r *ResolvedConfigV2
	if r.Clone() != nil {
		t.Errorf("nil.Clone() should be nil")
	}
}

func TestResolvedConfigV2_RevalidateAndIndex(t *testing.T) {
	r := v2Fixture()
	clone := r.Clone()
	if err := clone.RevalidateAndIndex(); err != nil {
		t.Fatalf("RevalidateAndIndex: %v", err)
	}
	if _, ok := clone.RuleIndex["r1"]; !ok {
		t.Errorf("RuleIndex not rebuilt")
	}
	if _, ok := clone.SecretIndex["sk_dev_1"]; !ok {
		t.Errorf("SecretIndex not rebuilt")
	}

	// A clone mutated into an invalid state must fail revalidation.
	bad := r.Clone()
	cfg := bad.Configurations["dev"]
	cfg.Bindings = []contractsconfig.Binding{{Protocol: "chat", Models: []string{"gpt-*"}, Backend: "ghost"}}
	bad.Configurations["dev"] = cfg
	if err := bad.RevalidateAndIndex(); err == nil {
		t.Errorf("expected validation error for unknown backend")
	}
}

func TestWritePolicyYAMLV2_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	// backends + groups live in their own file; the writer only owns policy.yaml.
	r := v2Fixture()
	writeFile(t, dir, "backends.yaml", backendsYAMLForRoundTrip)
	if err := WritePolicyYAMLV2(dir, r); err != nil {
		t.Fatalf("WritePolicyYAMLV2: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "policy.yaml")); err != nil {
		t.Fatalf("policy.yaml not written: %v", err)
	}

	reloaded, err := LoadV2(context.Background(), dir)
	if err != nil {
		t.Fatalf("LoadV2 round-trip: %v", err)
	}
	if _, ok := reloaded.RuleIndex["r1"]; !ok {
		t.Errorf("rule r1 lost in round-trip")
	}
	if _, ok := reloaded.Configurations["dev"]; !ok {
		t.Errorf("configuration dev lost in round-trip")
	}
}

func TestWritePolicyYAMLV2_Guards(t *testing.T) {
	if err := WritePolicyYAMLV2("", v2Fixture()); err == nil {
		t.Errorf("empty dir should error")
	}
	if err := WritePolicyYAMLV2(t.TempDir(), nil); err == nil {
		t.Errorf("nil resolved should error")
	}
}

func TestListConfigFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "policy.yaml", "configurations: {}\n")
	writeFile(t, dir, "backends.yaml", "backends: {}\n")
	writeFile(t, dir, "notes.txt", "ignored")
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}

	files, err := ListConfigFiles(dir)
	if err != nil {
		t.Fatalf("ListConfigFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %v, want 2 yaml files", files)
	}
	if _, ok := files["notes.txt"]; ok {
		t.Errorf("non-yaml file should be skipped")
	}

	if _, err := ListConfigFiles(filepath.Join(dir, "does-not-exist")); err == nil {
		t.Errorf("missing dir should error")
	}
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

const backendsYAMLForRoundTrip = `backends:
  openai:
    base_url: https://api.openai.com
    protocols:
      chat:
        path: /v1/chat/completions
        auth: { header: Authorization, format: "Bearer {key}" }
    passthrough:
      batches:
        auth: { header: x-api-key, format: "{key}" }
        paths:
          - { match: /v1/batches, methods: [POST] }
`

func TestLoadV2_MultiFileMergeAndErrors(t *testing.T) {
	// Empty / missing dir.
	if _, err := LoadV2(context.Background(), filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Errorf("missing dir should error")
	}
	if _, err := LoadV2(context.Background(), t.TempDir()); err == nil {
		t.Errorf("empty dir should error")
	}

	// Duplicate top-level block across two files.
	dup := t.TempDir()
	writeFile(t, dup, "a.yaml", "backends: {openai: {base_url: http://x, protocols: {chat: {path: /v1/chat/completions}}}}\n")
	writeFile(t, dup, "b.yaml", "backends: {anthropic: {base_url: http://y, protocols: {messages: {path: /v1/messages}}}}\n")
	if _, err := LoadV2(context.Background(), dup); err == nil {
		t.Errorf("duplicate backends block should error")
	}

	// Full multi-file load: backends + policy + admin + telemetry + connectors.
	full := t.TempDir()
	writeFile(t, full, "backends.yaml", backendsYAMLForRoundTrip)
	writeFile(t, full, "policy.yaml", `configurations:
  dev:
    credentials: { openai: sk-dev }
    bindings:
      - { protocol: chat, models: ["gpt-*"], backend: openai }
    connector_bindings:
      - { connector: artifacts, sampling: 1.0 }
api_keys:
  - { secret: sk_dev_x, name: k, configuration: dev, enabled: true }
connectors:
  - { name: artifacts, type: webhook, url: http://sink, secret_ref: "env:WH", timeout_ms: 5000 }
`)
	writeFile(t, full, "admin.yaml", "admin:\n  enabled: true\n  password: pw\ntelemetry:\n  content_capture: {}\n")
	r, err := LoadV2(context.Background(), full)
	if err != nil {
		t.Fatalf("multi-file LoadV2: %v", err)
	}
	if r.Admin == nil || !r.Admin.Enabled {
		t.Errorf("admin block not merged")
	}
	if _, ok := r.ConnectorIndex["artifacts"]; !ok {
		t.Errorf("connector index not built")
	}
}
