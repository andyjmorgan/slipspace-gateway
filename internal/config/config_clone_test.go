package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	contractsres "github.com/andyjmorgan/slipspace-gateway/contracts/resilience"
	rulescontract "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
)

func v2Fixture() *ResolvedConfig {
	r := &ResolvedConfig{
		Providers: contractsconfig.ProvidersConfig{
			"openai": {
				BaseURL:         "https://api.openai.com",
				RequiredHeaders: map[string]string{"x-org": "acme"},
				Query:           map[string]string{"api-version": "2025-01"},
				Protocols: map[string]contractsconfig.ProviderProtocol{
					"chat": {Path: "/v1/chat/completions", Auth: &contractsconfig.ProviderAuth{Header: "Authorization", Format: "Bearer {key}"}},
				},
				Passthrough: map[string]contractsconfig.PassthroughFamily{
					"batches": {
						Auth:  &contractsconfig.ProviderAuth{Header: "x-api-key", Format: "{key}"},
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
				Targets:            []contractsconfig.Target{{Provider: "openai", Query: map[string]string{"q": "1"}}},
			},
		},
		Configurations: map[string]contractsconfig.Configuration{
			"dev": {
				Credentials:         map[string]string{"openai": "sk-dev"},
				Bindings:            []contractsconfig.Binding{{Protocol: "chat", Models: []string{"gpt-*"}, Provider: "openai", Tags: []string{"t"}}},
				PassthroughBindings: []contractsconfig.PassthroughBinding{{Family: "batches", Provider: "openai"}},
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

func TestResolvedConfig_CloneIndependence(t *testing.T) {
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
	// Provider deep copy: mutating clone's protocol auth must not touch orig.
	be := clone.Providers["openai"]
	be.Protocols["chat"].Auth.Header = "X-Changed"
	if orig.Providers["openai"].Protocols["chat"].Auth.Header != "Authorization" {
		t.Errorf("orig provider auth mutated")
	}
}

// TestValidate_RetiredEndpointCondition asserts the rename guard: a rule whose
// condition decoded to the retired "endpoint" discriminator (an inert
// UnknownCondition) fails validation loud rather than silently evaluating
// false — both as a top-level condition and nested inside a RuleGroup.
func TestValidate_RetiredEndpointCondition(t *testing.T) {
	t.Run("top-level", func(t *testing.T) {
		r := v2Fixture()
		r.Rules[0].Condition = &rulescontract.UnknownCondition{Type: "endpoint"}
		if err := r.RevalidateAndIndex(); !errors.Is(err, ErrRetiredEndpointCondition) {
			t.Fatalf("err = %v, want ErrRetiredEndpointCondition", err)
		}
	})

	t.Run("nested-in-group", func(t *testing.T) {
		r := v2Fixture()
		r.Rules[0].Condition = &rulescontract.RuleGroup{
			Type:            "group",
			LogicalOperator: rulescontract.LogicalAnd,
			Children: []rulescontract.Condition{
				&rulescontract.ProviderCondition{Type: "provider", Operator: rulescontract.EnumEquals, ExpectedProvider: "openai"},
				&rulescontract.UnknownCondition{Type: "endpoint"},
			},
		}
		if err := r.RevalidateAndIndex(); !errors.Is(err, ErrRetiredEndpointCondition) {
			t.Fatalf("err = %v, want ErrRetiredEndpointCondition", err)
		}
	})

	t.Run("unrelated-unknown-condition-passes", func(t *testing.T) {
		r := v2Fixture()
		r.Rules[0].Condition = &rulescontract.RuleGroup{
			Type:            "group",
			LogicalOperator: rulescontract.LogicalAnd,
			Children: []rulescontract.Condition{
				&rulescontract.UnknownCondition{Type: "somethingNew"},
			},
		}
		if err := r.RevalidateAndIndex(); errors.Is(err, ErrRetiredEndpointCondition) {
			t.Fatalf("unrelated UnknownCondition wrongly tripped the endpoint guard")
		}
	})
}

func TestResolvedConfig_CloneNil(t *testing.T) {
	var r *ResolvedConfig
	if r.Clone() != nil {
		t.Errorf("nil.Clone() should be nil")
	}
}

func TestResolvedConfig_RevalidateAndIndex(t *testing.T) {
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
	cfg.Bindings = []contractsconfig.Binding{{Protocol: "chat", Models: []string{"gpt-*"}, Provider: "ghost"}}
	bad.Configurations["dev"] = cfg
	if err := bad.RevalidateAndIndex(); err == nil {
		t.Errorf("expected validation error for unknown provider")
	}
}

func TestWritePolicyYAML_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	// providers + groups live in their own file; the writer only owns policy.yaml.
	r := v2Fixture()
	writeFile(t, dir, "providers.yaml", providersYAMLForRoundTrip)
	if err := WritePolicyYAML(dir, r); err != nil {
		t.Fatalf("WritePolicyYAML: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "policy.yaml")); err != nil {
		t.Fatalf("policy.yaml not written: %v", err)
	}

	reloaded, err := Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load round-trip: %v", err)
	}
	if _, ok := reloaded.RuleIndex["r1"]; !ok {
		t.Errorf("rule r1 lost in round-trip")
	}
	if _, ok := reloaded.Configurations["dev"]; !ok {
		t.Errorf("configuration dev lost in round-trip")
	}
}

func TestWritePolicyYAML_Guards(t *testing.T) {
	if err := WritePolicyYAML("", v2Fixture()); err == nil {
		t.Errorf("empty dir should error")
	}
	if err := WritePolicyYAML(t.TempDir(), nil); err == nil {
		t.Errorf("nil resolved should error")
	}
}

func TestListConfigFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "policy.yaml", "configurations: {}\n")
	writeFile(t, dir, "providers.yaml", "providers: {}\n")
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

const providersYAMLForRoundTrip = `providers:
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

func TestLoad_MultiFileMergeAndErrors(t *testing.T) {
	// Empty / missing dir.
	if _, err := Load(context.Background(), filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Errorf("missing dir should error")
	}
	if _, err := Load(context.Background(), t.TempDir()); err == nil {
		t.Errorf("empty dir should error")
	}

	// Duplicate top-level block across two files.
	dup := t.TempDir()
	writeFile(t, dup, "a.yaml", "providers: {openai: {base_url: http://x, protocols: {chat: {path: /v1/chat/completions}}}}\n")
	writeFile(t, dup, "b.yaml", "providers: {anthropic: {base_url: http://y, protocols: {messages: {path: /v1/messages}}}}\n")
	if _, err := Load(context.Background(), dup); err == nil {
		t.Errorf("duplicate providers block should error")
	}

	// Full multi-file load: providers + policy + admin + telemetry + connectors.
	full := t.TempDir()
	writeFile(t, full, "providers.yaml", providersYAMLForRoundTrip)
	writeFile(t, full, "policy.yaml", `configurations:
  dev:
    credentials: { openai: sk-dev }
    bindings:
      - { protocol: chat, models: ["gpt-*"], provider: openai }
    connector_bindings:
      - { connector: artifacts, sampling: 1.0 }
api_keys:
  - { secret: sk_dev_x, name: k, configuration: dev, enabled: true }
connectors:
  - { name: artifacts, type: webhook, url: http://sink, secret_ref: "env:WH", timeout_ms: 5000 }
`)
	writeFile(t, full, "admin.yaml", "admin:\n  enabled: true\n  password: pw\ntelemetry:\n  content_capture: {}\n")
	r, err := Load(context.Background(), full)
	if err != nil {
		t.Fatalf("multi-file Load: %v", err)
	}
	if r.Admin == nil || !r.Admin.Enabled {
		t.Errorf("admin block not merged")
	}
	if _, ok := r.ConnectorIndex["artifacts"]; !ok {
		t.Errorf("connector index not built")
	}
}
