package selection_test

import (
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/selection"
)

// golden is the live prod config expressed in the v2 model — the fixture the
// selection engine is validated against.
const golden = `
providers:
  openai:
    base_url: https://api.openai.com
    protocols:
      chat:      { path: /v1/chat/completions, auth: { header: Authorization, format: "Bearer {key}" } }
      responses: { path: /v1/responses,        auth: { header: Authorization, format: "Bearer {key}" } }
  anthropic:
    base_url: https://api.anthropic.com
    required_headers: { anthropic-version: "2023-06-01" }
    protocols:
      messages: { path: /v1/messages,        auth: { header: x-api-key,     format: "{key}" } }
      chat:     { path: /v1/chat/completions, auth: { header: Authorization, format: "Bearer {key}" } }
    passthrough:
      messages_batches:
        auth: { header: x-api-key, format: "{key}" }
        paths:
          - match: /v1/messages/batches
            methods: [POST, GET]
          - match: /v1/messages/batches/{id}/results
            methods: [GET]
  azure-foundry:
    base_url: https://res.cognitiveservices.azure.com
    query: { api-version: 2025-04-01-preview }
    protocols:
      responses: { path: /openai/responses, auth: { header: api-key, format: "{key}" } }
  qwen-ollama:
    base_url: http://ollama.qwen:11434
    protocols:
      chat: { path: /v1/chat/completions }
  qwen-standalone:
    base_url: http://192.168.69.21:11434
    protocols:
      chat: { path: /v1/chat/completions }

groups:
  qwen-load-balance:
    mode: load_balance
    failure_status_codes: [502, 503, 504]
    circuit_breaker: { enabled: true, failure_threshold: 3, cooldown_seconds: 60 }
    targets:
      - { provider: qwen-ollama,     alias: "qwen2.5-coder:7b" }
      - { provider: qwen-standalone, alias: "qwen-coder" }

configurations:
  production:
    credentials:
      openai: sk-openai
      anthropic: sk-anthropic
      azure-foundry: az-key
      qwen-ollama: ""
      qwen-standalone: ""
    bindings:
      - { protocol: chat,      models: ["gpt-*","o3*"],          provider: openai }
      - { protocol: responses, models: ["gpt-*","o3*"],          provider: openai }
      - { protocol: chat,      models: ["claude-*"],             provider: anthropic }
      - { protocol: chat,      models: ["qwen2.5-coder:7b"],     group: qwen-load-balance }
      - { protocol: responses, models: ["foundry-model-name"],   provider: azure-foundry, alias: gpt-5.2-chat, tags: [foundry] }
    passthrough_bindings:
      - { family: messages_batches, provider: anthropic }
`

func loadGolden(t *testing.T) contractsconfig.Model {
	t.Helper()
	var m contractsconfig.Model
	if err := yaml.Unmarshal([]byte(golden), &m); err != nil {
		t.Fatalf("load golden: %v", err)
	}
	return m
}

func TestSelect_SingleProvider(t *testing.T) {
	m := loadGolden(t)
	d, err := selection.Select("chat", "gpt-4o-mini", m.Configurations["production"], m.Providers, m.Groups)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if d.Single == nil {
		t.Fatalf("expected single target, got %+v", d)
	}
	if d.Single.Provider != "openai" || d.Single.BaseURL != "https://api.openai.com" {
		t.Errorf("provider = %+v", d.Single)
	}
	if d.Single.Path != "/v1/chat/completions" {
		t.Errorf("path = %q", d.Single.Path)
	}
	if d.Single.Auth.Format != "Bearer {key}" || d.Single.Credential != "sk-openai" {
		t.Errorf("auth/cred = %+v cred=%q", d.Single.Auth, d.Single.Credential)
	}
}

func TestSelect_CrossProviderCompatChat(t *testing.T) {
	m := loadGolden(t)
	d, err := selection.Select("chat", "claude-3-5-sonnet", m.Configurations["production"], m.Providers, m.Groups)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	// claude on the chat protocol lands on anthropic's compat-chat surface —
	// Bearer auth, not the native x-api-key.
	if d.Single == nil || d.Single.Provider != "anthropic" {
		t.Fatalf("expected anthropic, got %+v", d)
	}
	if d.Single.Auth.Header != "Authorization" || d.Single.Auth.Format != "Bearer {key}" {
		t.Errorf("compat-chat auth = %+v", d.Single.Auth)
	}
	if d.Single.RequiredHeaders["anthropic-version"] != "2023-06-01" {
		t.Errorf("required headers = %v", d.Single.RequiredHeaders)
	}
}

func TestSelect_Group_PerTargetAlias(t *testing.T) {
	m := loadGolden(t)
	d, err := selection.Select("chat", "qwen2.5-coder:7b", m.Configurations["production"], m.Providers, m.Groups)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if d.Group == nil {
		t.Fatalf("expected group, got %+v", d)
	}
	if len(d.Group.Targets) != 2 {
		t.Fatalf("targets = %d", len(d.Group.Targets))
	}
	if d.Group.Targets[0].Alias != "qwen2.5-coder:7b" || d.Group.Targets[1].Alias != "qwen-coder" {
		t.Errorf("per-target aliases = %q / %q", d.Group.Targets[0].Alias, d.Group.Targets[1].Alias)
	}
	if d.Group.Targets[1].BaseURL != "http://192.168.69.21:11434" {
		t.Errorf("standalone base = %q", d.Group.Targets[1].BaseURL)
	}
	if d.Group.CircuitBreaker == nil || !d.Group.CircuitBreaker.Enabled {
		t.Errorf("group cb = %+v", d.Group.CircuitBreaker)
	}
}

func TestSelect_AzureAliasQueryTags(t *testing.T) {
	m := loadGolden(t)
	d, err := selection.Select("responses", "foundry-model-name", m.Configurations["production"], m.Providers, m.Groups)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if d.Single == nil || d.Single.Provider != "azure-foundry" {
		t.Fatalf("expected azure-foundry, got %+v", d)
	}
	if d.Single.Alias != "gpt-5.2-chat" {
		t.Errorf("alias = %q", d.Single.Alias)
	}
	if d.Single.Query["api-version"] != "2025-04-01-preview" {
		t.Errorf("query = %v", d.Single.Query)
	}
	if d.Single.Auth.Header != "api-key" {
		t.Errorf("azure auth header = %q", d.Single.Auth.Header)
	}
	if len(d.Tags) != 1 || d.Tags[0] != "foundry" {
		t.Errorf("tags = %v", d.Tags)
	}
}

func TestSelect_NoBindingIs404(t *testing.T) {
	m := loadGolden(t)
	_, err := selection.Select("chat", "nomatch-internal", m.Configurations["production"], m.Providers, m.Groups)
	if !errors.Is(err, selection.ErrNoBinding) {
		t.Fatalf("want ErrNoBinding, got %v", err)
	}
	// responses-only-style: a model bound on chat is not served on responses.
	_, err = selection.Select("responses", "claude-3-5-sonnet", m.Configurations["production"], m.Providers, m.Groups)
	if !errors.Is(err, selection.ErrNoBinding) {
		t.Fatalf("cross-protocol miss: want ErrNoBinding, got %v", err)
	}
}

func TestSelect_NoCredProviderResolves(t *testing.T) {
	m := loadGolden(t)
	d, err := selection.Select("chat", "qwen2.5-coder:7b", m.Configurations["production"], m.Providers, m.Groups)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	// qwen providers carry an explicit empty credential ("") — resolves to a
	// no-credential target, not an error.
	if d.Group.Targets[0].Credential != "" {
		t.Errorf("expected empty credential, got %q", d.Group.Targets[0].Credential)
	}
}

func TestResolveTarget(t *testing.T) {
	m := loadGolden(t)
	cfg := m.Configurations["production"]
	// Per-attempt re-resolution of a group provider with its alias.
	tgt, err := selection.ResolveTarget("chat", "qwen-standalone", "qwen-coder", cfg, m.Providers)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tgt.BaseURL != "http://192.168.69.21:11434" || tgt.Alias != "qwen-coder" {
		t.Errorf("resolved = %+v", tgt)
	}
	if _, err := selection.ResolveTarget("chat", "ghost", "", cfg, m.Providers); err == nil {
		t.Fatal("unknown provider: want error")
	}
}

func TestMatchPassthrough_ExactAndParams(t *testing.T) {
	m := loadGolden(t)
	cfg := m.Configurations["production"]

	pm, err := selection.MatchPassthrough("POST", "/v1/messages/batches", cfg, m.Providers)
	if err != nil {
		t.Fatalf("passthrough: %v", err)
	}
	if pm.Provider != "anthropic" || pm.Family != "messages_batches" {
		t.Errorf("match = %+v", pm)
	}
	if pm.Auth.Header != "x-api-key" || pm.Credential != "sk-anthropic" {
		t.Errorf("auth/cred = %+v %q", pm.Auth, pm.Credential)
	}

	pm, err = selection.MatchPassthrough("GET", "/v1/messages/batches/batch_123/results", cfg, m.Providers)
	if err != nil {
		t.Fatalf("passthrough params: %v", err)
	}
	if pm.Params["id"] != "batch_123" {
		t.Errorf("captured params = %v", pm.Params)
	}
}

func TestSelect_ResolutionErrors(t *testing.T) {
	providers := contractsconfig.ProvidersConfig{
		"only-responses": {
			BaseURL:   "http://x",
			Protocols: map[string]contractsconfig.ProviderProtocol{"responses": {Path: "/r"}},
		},
	}
	cases := []struct {
		name string
		cfg  contractsconfig.Configuration
		grp  contractsconfig.GroupsConfig
		want string
	}{
		{
			name: "unknown group",
			cfg:  contractsconfig.Configuration{Bindings: []contractsconfig.Binding{{Protocol: "chat", Group: "ghost"}}},
			want: "unknown group",
		},
		{
			name: "unknown provider",
			cfg:  contractsconfig.Configuration{Bindings: []contractsconfig.Binding{{Protocol: "chat", Provider: "ghost"}}},
			want: "unknown provider",
		},
		{
			name: "provider does not serve protocol",
			cfg: contractsconfig.Configuration{
				Credentials: map[string]string{"only-responses": "k"},
				Bindings:    []contractsconfig.Binding{{Protocol: "chat", Provider: "only-responses"}},
			},
			want: "does not serve protocol",
		},
		{
			name: "no credential entry",
			cfg:  contractsconfig.Configuration{Bindings: []contractsconfig.Binding{{Protocol: "responses", Provider: "only-responses"}}},
			want: "no credential entry",
		},
		{
			name: "group target unknown provider",
			cfg:  contractsconfig.Configuration{Bindings: []contractsconfig.Binding{{Protocol: "chat", Group: "g"}}},
			grp:  contractsconfig.GroupsConfig{"g": {Targets: []contractsconfig.Target{{Provider: "ghost"}}}},
			want: "unknown provider",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := selection.Select("chat", "m", tc.cfg, providers, tc.grp)
			if tc.name == "no credential entry" {
				_, err = selection.Select("responses", "m", tc.cfg, providers, tc.grp)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestMatchPassthrough_ResolutionErrors(t *testing.T) {
	providers := contractsconfig.ProvidersConfig{
		"anthropic": {BaseURL: "http://x", Passthrough: map[string]contractsconfig.PassthroughFamily{
			"batches": {Paths: []contractsconfig.PassthroughPath{{Match: "/b", Methods: []string{"GET"}}}},
		}},
	}
	cfgUnknownProvider := contractsconfig.Configuration{PassthroughBindings: []contractsconfig.PassthroughBinding{{Family: "batches", Provider: "ghost"}}}
	if _, err := selection.MatchPassthrough("GET", "/b", cfgUnknownProvider, providers); err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("unknown provider: %v", err)
	}
	cfgUnknownFamily := contractsconfig.Configuration{PassthroughBindings: []contractsconfig.PassthroughBinding{{Family: "ghost", Provider: "anthropic"}}}
	if _, err := selection.MatchPassthrough("GET", "/b", cfgUnknownFamily, providers); err == nil || !strings.Contains(err.Error(), "no passthrough family") {
		t.Fatalf("unknown family: %v", err)
	}
}

func TestMatchPassthrough_MethodAndMiss(t *testing.T) {
	m := loadGolden(t)
	cfg := m.Configurations["production"]

	// results path is GET-only.
	_, err := selection.MatchPassthrough("POST", "/v1/messages/batches/b1/results", cfg, m.Providers)
	if !errors.Is(err, selection.ErrMethodNotAllowed) {
		t.Fatalf("want ErrMethodNotAllowed, got %v", err)
	}
	// unknown path.
	_, err = selection.MatchPassthrough("GET", "/v1/files", cfg, m.Providers)
	if !errors.Is(err, selection.ErrNoPassthrough) {
		t.Fatalf("want ErrNoPassthrough, got %v", err)
	}
}
