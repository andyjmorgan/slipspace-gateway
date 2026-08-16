package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	"github.com/andyjmorgan/slipspace-gateway/contracts/resilience"
)

const v2Providers = `
providers:
  openai:
    base_url: https://api.openai.com
    protocols:
      chat: { path: /v1/chat/completions, auth: { header: Authorization, format: "Bearer {key}" } }
  anthropic:
    base_url: https://api.anthropic.com
    protocols:
      chat: { path: /v1/chat/completions, auth: { header: Authorization, format: "Bearer {key}" } }
    passthrough:
      messages_batches:
        auth: { header: x-api-key, format: "{key}" }
        paths:
          - match: /v1/messages/batches
            methods: [POST]
  qwen-a: { base_url: http://a:11434, protocols: { chat: {} } }
  qwen-b: { base_url: http://b:11434, protocols: { chat: {} } }
groups:
  lb:
    mode: load_balance
    targets:
      - { provider: qwen-a, alias: m-a }
      - { provider: qwen-b, alias: m-b }
`

const v2Policy = `
configurations:
  production:
    credentials:
      openai: sk-o
      anthropic: sk-a
      qwen-a: ""
      qwen-b: ""
    bindings:
      - { protocol: chat, models: ["gpt-*"],     provider: openai }
      - { protocol: chat, models: ["claude-*"],  provider: anthropic }
      - { protocol: chat, models: ["qwen-coder"], group: lb }
    passthrough_bindings:
      - { family: messages_batches, provider: anthropic }
    rule_names: [tag-codex]
    tags: { tier: production }
    connector_bindings:
      - { connector: hook, sampling: 1.0 }
api_keys:
  - { secret: sk_live_x, name: prod, configuration: production, enabled: true }
rules:
  - name: tag-codex
    condition: { type: header, keyOperator: Equals, keyPattern: user-agent, valueOperator: StartsWith, valuePattern: "codex_exec/", caseInsensitive: true }
    actions: [{ type: addTag, tag: Codex }]
    behavior: continue
connectors:
  - { name: hook, type: webhook, url: https://hooks.example.com/ingest, secret_ref: "env:HOOK_SECRET", timeout_ms: 5000 }
`

func writeDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestLoad_HappyPath(t *testing.T) {
	dir := writeDir(t, map[string]string{
		"providers.yaml": v2Providers,
		"policy.yaml":    v2Policy,
	})
	r, err := Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.SecretIndex["sk_live_x"] == nil || r.SecretIndex["sk_live_x"].Configuration != "production" {
		t.Errorf("secret index = %+v", r.SecretIndex)
	}
	prod := r.ConfigurationIndex["production"]
	if prod == nil || len(prod.Bindings) != 3 {
		t.Fatalf("configuration index = %+v", prod)
	}
	if r.Groups["lb"].Targets[0].Alias != "m-a" {
		t.Errorf("group = %+v", r.Groups["lb"])
	}
	if r.Providers["anthropic"].Passthrough["messages_batches"].Paths[0].Match != "/v1/messages/batches" {
		t.Errorf("passthrough = %+v", r.Providers["anthropic"].Passthrough)
	}
	if r.RuleIndex["tag-codex"] == nil || len(r.PerConfigurationRules["production"]) != 1 {
		t.Errorf("rule index / per-config = %+v / %+v", r.RuleIndex, r.PerConfigurationRules)
	}
	if r.ConnectorIndex["hook"] == nil {
		t.Errorf("connector index = %+v", r.ConnectorIndex)
	}
}

func TestLoad_DuplicateKeyAcrossFiles(t *testing.T) {
	dir := writeDir(t, map[string]string{
		"a.yaml":      v2Providers,
		"b.yaml":      "providers:\n  x: { base_url: http://x, protocols: { chat: {} } }\n",
		"policy.yaml": v2Policy,
	})
	_, err := Load(context.Background(), dir)
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("want ErrDuplicateKey, got %v", err)
	}
}

// TestLoad_LegacyBackendsKeyRejected proves the hard cut: a pre-rename
// `backends:` block is rejected with a clear error rather than silently
// ignored. No back-compat alias.
func TestLoad_LegacyBackendsKeyRejected(t *testing.T) {
	dir := writeDir(t, map[string]string{
		"backends.yaml": "backends:\n  openai: { base_url: https://api.openai.com, protocols: { chat: {} } }\n",
		"policy.yaml":   v2Policy,
	})
	_, err := Load(context.Background(), dir)
	if !errors.Is(err, ErrLegacyProvidersKey) {
		t.Fatalf("want ErrLegacyProvidersKey, got %v", err)
	}
}

func TestLoad_EmptyAndParseErrors(t *testing.T) {
	if _, err := Load(context.Background(), t.TempDir()); !errors.Is(err, ErrEmptyDirectory) {
		t.Fatalf("empty dir: want ErrEmptyDirectory, got %v", err)
	}
	// Malformed YAML must wrap ErrParse, not merely be non-nil:
	// cmd/cli's reason classifier branches on errors.Is(err, ErrParse)
	// to report "parse_error", and asserting only err != nil let that
	// branch sit unreachable.
	dir := writeDir(t, map[string]string{"bad.yaml": "providers: {{{not yaml"})
	_, err := Load(context.Background(), dir)
	if err == nil {
		t.Fatal("parse error: want error, got nil")
	}
	if !errors.Is(err, ErrParse) {
		t.Fatalf("parse error: want wrapped ErrParse, got %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Load(cancelled, t.TempDir()); err == nil {
		t.Fatal("cancelled ctx: want error, got nil")
	}
}

func TestLoad_AdminTelemetryAndValidateError(t *testing.T) {
	// admin + telemetry blocks merge from a third file.
	dir := writeDir(t, map[string]string{
		"providers.yaml": v2Providers,
		"policy.yaml":    v2Policy,
		"admin.yaml":     "admin:\n  enabled: true\n  bind_addr: \"127.0.0.1:8099\"\n  password: pw\ntelemetry:\n  capture_content: false\n",
	})
	r, err := Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.Admin == nil || !r.Admin.Enabled {
		t.Errorf("admin = %+v", r.Admin)
	}

	// A config that fails Validate surfaces the error through Load.
	bad := writeDir(t, map[string]string{
		"c.yaml": "configurations:\n  p:\n    bindings:\n      - { protocol: chat, provider: ghost }\n",
	})
	if _, err := Load(context.Background(), bad); err == nil {
		t.Fatal("validate error: want error, got nil")
	}
}

// validBase returns a minimal valid v2 config the failure-case mutators tweak.
func validBase() *ResolvedConfig {
	return &ResolvedConfig{
		Providers: contractsconfig.ProvidersConfig{
			"openai": {BaseURL: "http://o", Protocols: map[string]contractsconfig.ProviderProtocol{"chat": {Path: "/c"}}},
			"resp":   {BaseURL: "http://r", Protocols: map[string]contractsconfig.ProviderProtocol{"responses": {Path: "/r"}}},
		},
		Groups: contractsconfig.GroupsConfig{
			"lb": {Mode: resilience.ModeLoadBalance, Targets: []contractsconfig.Target{{Provider: "openai"}}},
		},
		Configurations: map[string]contractsconfig.Configuration{
			"prod": {
				Credentials: map[string]string{"openai": "k", "resp": "k"},
				Bindings:    []contractsconfig.Binding{{Protocol: "chat", Models: []string{"gpt-*"}, Provider: "openai"}},
			},
		},
		APIKeys: contractsconfig.APIKeysConfig{{Secret: "s", Name: "n", Configuration: "prod", Enabled: true}},
	}
}

func TestValidate_Failures(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ResolvedConfig)
		want   string
	}{
		{"no configurations", func(r *ResolvedConfig) { r.Configurations = nil }, "at least one configuration"},
		{"provider no base_url", func(r *ResolvedConfig) {
			r.Providers["openai"] = contractsconfig.Provider{Protocols: map[string]contractsconfig.ProviderProtocol{"chat": {}}}
		}, "base_url is required"},
		{"provider unknown protocol", func(r *ResolvedConfig) {
			r.Providers["openai"] = contractsconfig.Provider{BaseURL: "http://o", Protocols: map[string]contractsconfig.ProviderProtocol{"weird": {}}}
		}, "unknown protocol"},
		{"provider no protocols", func(r *ResolvedConfig) {
			r.Providers["openai"] = contractsconfig.Provider{BaseURL: "http://o"}
		}, "no protocols or passthrough"},
		{"auth format without header", func(r *ResolvedConfig) {
			r.Providers["openai"] = contractsconfig.Provider{BaseURL: "http://o", Protocols: map[string]contractsconfig.ProviderProtocol{"chat": {Auth: &contractsconfig.ProviderAuth{Format: "Bearer {key}"}}}}
		}, "auth_format requires"},
		{"auth format bad placeholder", func(r *ResolvedConfig) {
			r.Providers["openai"] = contractsconfig.Provider{BaseURL: "http://o", Protocols: map[string]contractsconfig.ProviderProtocol{"chat": {Auth: &contractsconfig.ProviderAuth{Header: "H", Format: "no-placeholder"}}}}
		}, "{key} exactly once"},
		{"group no targets", func(r *ResolvedConfig) {
			r.Groups["lb"] = contractsconfig.Group{Mode: resilience.ModeLoadBalance}
		}, "no targets"},
		{"group unknown provider", func(r *ResolvedConfig) {
			r.Groups["lb"] = contractsconfig.Group{Mode: resilience.ModeLoadBalance, Targets: []contractsconfig.Target{{Provider: "ghost"}}}
		}, "unknown provider"},
		{"credentials unknown provider", func(r *ResolvedConfig) {
			c := r.Configurations["prod"]
			c.Credentials = map[string]string{"ghost": "k"}
			r.Configurations["prod"] = c
		}, "credentials reference unknown provider"},
		{"binding both provider and group", func(r *ResolvedConfig) {
			c := r.Configurations["prod"]
			c.Bindings = []contractsconfig.Binding{{Protocol: "chat", Provider: "openai", Group: "lb"}}
			r.Configurations["prod"] = c
		}, "exactly one of provider or group"},
		{"binding neither", func(r *ResolvedConfig) {
			c := r.Configurations["prod"]
			c.Bindings = []contractsconfig.Binding{{Protocol: "chat"}}
			r.Configurations["prod"] = c
		}, "exactly one of provider or group"},
		{"binding unknown protocol", func(r *ResolvedConfig) {
			c := r.Configurations["prod"]
			c.Bindings = []contractsconfig.Binding{{Protocol: "weird", Provider: "openai"}}
			r.Configurations["prod"] = c
		}, "unknown protocol"},
		{"binding unknown provider", func(r *ResolvedConfig) {
			c := r.Configurations["prod"]
			c.Bindings = []contractsconfig.Binding{{Protocol: "chat", Provider: "ghost"}}
			r.Configurations["prod"] = c
		}, "unknown provider"},
		{"binding provider wrong protocol", func(r *ResolvedConfig) {
			c := r.Configurations["prod"]
			c.Bindings = []contractsconfig.Binding{{Protocol: "responses", Provider: "openai"}}
			r.Configurations["prod"] = c
		}, "does not serve protocol"},
		{"binding unknown group", func(r *ResolvedConfig) {
			c := r.Configurations["prod"]
			c.Bindings = []contractsconfig.Binding{{Protocol: "chat", Group: "ghost"}}
			r.Configurations["prod"] = c
		}, "unknown group"},
		{"group not protocol-preserving", func(r *ResolvedConfig) {
			r.Groups["lb"] = contractsconfig.Group{Mode: resilience.ModeLoadBalance, Targets: []contractsconfig.Target{{Provider: "resp"}}}
			c := r.Configurations["prod"]
			c.Bindings = []contractsconfig.Binding{{Protocol: "chat", Group: "lb"}}
			r.Configurations["prod"] = c
		}, "protocol-preserving"},
		{"bad model pattern", func(r *ResolvedConfig) {
			c := r.Configurations["prod"]
			c.Bindings = []contractsconfig.Binding{{Protocol: "chat", Models: []string{"gpt-*-mini"}, Provider: "openai"}}
			r.Configurations["prod"] = c
		}, "trailing '*'"},
		{"duplicate exact model", func(r *ResolvedConfig) {
			c := r.Configurations["prod"]
			c.Bindings = []contractsconfig.Binding{
				{Protocol: "chat", Models: []string{"gpt-4o"}, Provider: "openai"},
				{Protocol: "chat", Models: []string{"gpt-4o"}, Provider: "openai"},
			}
			r.Configurations["prod"] = c
		}, "already bound"},
		{"two catch-alls", func(r *ResolvedConfig) {
			c := r.Configurations["prod"]
			c.Bindings = []contractsconfig.Binding{
				{Protocol: "chat", Provider: "openai"},
				{Protocol: "chat", Provider: "openai"},
			}
			r.Configurations["prod"] = c
		}, "two catch-all"},
		{"passthrough unknown provider", func(r *ResolvedConfig) {
			c := r.Configurations["prod"]
			c.PassthroughBindings = []contractsconfig.PassthroughBinding{{Family: "f", Provider: "ghost"}}
			r.Configurations["prod"] = c
		}, "unknown provider"},
		{"passthrough unknown family", func(r *ResolvedConfig) {
			c := r.Configurations["prod"]
			c.PassthroughBindings = []contractsconfig.PassthroughBinding{{Family: "ghost", Provider: "openai"}}
			r.Configurations["prod"] = c
		}, "no passthrough family"},
		{"unknown rule name", func(r *ResolvedConfig) {
			c := r.Configurations["prod"]
			c.RuleNames = []string{"ghost"}
			r.Configurations["prod"] = c
		}, "references rule"},
		{"unknown connector", func(r *ResolvedConfig) {
			c := r.Configurations["prod"]
			c.ConnectorBindings = []contractsconfig.ConnectorBinding{{Connector: "ghost"}}
			r.Configurations["prod"] = c
		}, "references"},
		{"api key unknown config", func(r *ResolvedConfig) {
			r.APIKeys = contractsconfig.APIKeysConfig{{Secret: "s", Name: "n", Configuration: "ghost"}}
		}, "references"},
		{"api key empty secret", func(r *ResolvedConfig) {
			r.APIKeys = contractsconfig.APIKeysConfig{{Secret: "", Name: "n", Configuration: "prod"}}
		}, "secret is required"},
		{"duplicate secret", func(r *ResolvedConfig) {
			r.APIKeys = contractsconfig.APIKeysConfig{
				{Secret: "dup", Name: "a", Configuration: "prod"},
				{Secret: "dup", Name: "b", Configuration: "prod"},
			}
		}, "duplicate secret"},
		{"passthrough no paths", func(r *ResolvedConfig) {
			r.Providers["pt"] = contractsconfig.Provider{BaseURL: "http://pt", Passthrough: map[string]contractsconfig.PassthroughFamily{"f": {}}}
		}, "no paths"},
		{"passthrough path no match", func(r *ResolvedConfig) {
			r.Providers["pt"] = contractsconfig.Provider{BaseURL: "http://pt", Passthrough: map[string]contractsconfig.PassthroughFamily{
				"f": {Paths: []contractsconfig.PassthroughPath{{Methods: []string{"GET"}}}}}}
		}, "match is required"},
		{"passthrough path no methods", func(r *ResolvedConfig) {
			r.Providers["pt"] = contractsconfig.Provider{BaseURL: "http://pt", Passthrough: map[string]contractsconfig.PassthroughFamily{
				"f": {Paths: []contractsconfig.PassthroughPath{{Match: "/x"}}}}}
		}, "methods is required"},
		{"passthrough auth bad", func(r *ResolvedConfig) {
			r.Providers["pt"] = contractsconfig.Provider{BaseURL: "http://pt", Passthrough: map[string]contractsconfig.PassthroughFamily{
				"f": {Auth: &contractsconfig.ProviderAuth{Format: "x"}, Paths: []contractsconfig.PassthroughPath{{Match: "/x", Methods: []string{"GET"}}}}}}
		}, "auth_format requires"},
		{"group target empty provider", func(r *ResolvedConfig) {
			r.Groups["lb"] = contractsconfig.Group{Mode: resilience.ModeLoadBalance, Targets: []contractsconfig.Target{{Provider: ""}}}
		}, "provider is required"},
		{"connector validate error", func(r *ResolvedConfig) {
			r.Connectors = contractsconfig.ConnectorsConfig{{Name: "c", Type: "webhook"}}
		}, "url is required"},
		{"duplicate connector name", func(r *ResolvedConfig) {
			c := contractsconfig.Connector{Name: "c", Type: "webhook", URL: "https://hooks.example.com/i", SecretRef: "env:S", TimeoutMS: 5000}
			r.Connectors = contractsconfig.ConnectorsConfig{c, c}
		}, "defined more than once"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := validBase()
			tc.mutate(r)
			err := r.Validate()
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}
