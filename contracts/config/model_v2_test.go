package config_test

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/contracts/resilience"
)

func TestModelV2_YAMLRoundTrip(t *testing.T) {
	body := `
backends:
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

groups:
  qwen-load-balance:
    mode: load_balance
    failure_status_codes: [502, 503, 504]
    circuit_breaker: { enabled: true, failure_threshold: 3, cooldown_seconds: 60 }
    targets:
      - { backend: qwen-ollama,     alias: "qwen2.5-coder:7b" }
      - { backend: qwen-standalone, alias: "qwen-coder" }

configurations:
  production:
    credentials:
      openai: ref:openai
      anthropic: ref:anthropic
      qwen-ollama: ""
    bindings:
      - { protocol: chat,      models: ["gpt-*","o3*"], backend: openai }
      - { protocol: chat,      models: ["claude-*"],    backend: anthropic }
      - { protocol: chat,      models: ["qwen2.5-coder:7b"], group: qwen-load-balance }
      - { protocol: responses, models: ["foundry-model-name"], backend: azure-foundry, alias: gpt-5.2-chat, tags: [foundry] }
    passthrough_bindings:
      - { family: messages_batches, backend: anthropic }
    rule_names: [force-openai-streaming-usage]
    tags: { tier: production }
`
	var m contractsconfig.ModelV2
	if err := yaml.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Backend connection + per-protocol auth (the OpenAI-compat invariant: same
	// backend, different credential conventions per protocol).
	anth := m.Backends["anthropic"]
	if anth.RequiredHeaders["anthropic-version"] != "2023-06-01" {
		t.Errorf("anthropic required header = %v", anth.RequiredHeaders)
	}
	if anth.Protocols["messages"].Auth.Header != "x-api-key" {
		t.Errorf("messages auth header = %q", anth.Protocols["messages"].Auth.Header)
	}
	if anth.Protocols["chat"].Auth.Format != "Bearer {key}" {
		t.Errorf("compat chat auth format = %q", anth.Protocols["chat"].Auth.Format)
	}

	// Passthrough family: path patterns + methods, opaque.
	batches := anth.Passthrough["messages_batches"]
	if len(batches.Paths) != 2 || batches.Paths[0].Match != "/v1/messages/batches" {
		t.Errorf("passthrough paths = %+v", batches.Paths)
	}
	if batches.Paths[0].Methods[1] != "GET" {
		t.Errorf("passthrough methods = %v", batches.Paths[0].Methods)
	}

	// Backend-level default query (Azure api-version, was an appendQueryString rule).
	if m.Backends["azure-foundry"].Query["api-version"] != "2025-04-01-preview" {
		t.Errorf("azure query = %v", m.Backends["azure-foundry"].Query)
	}

	// Group: cross-backend targets with per-target alias.
	g := m.Groups["qwen-load-balance"]
	if g.Mode != resilience.ModeLoadBalance {
		t.Errorf("group mode = %q", g.Mode)
	}
	if len(g.Targets) != 2 || g.Targets[1].Alias != "qwen-coder" {
		t.Errorf("group targets = %+v", g.Targets)
	}
	if g.CircuitBreaker == nil || !g.CircuitBreaker.Enabled {
		t.Errorf("group circuit breaker = %+v", g.CircuitBreaker)
	}

	// Configuration bindings: single-backend, group, alias+tags.
	prod := m.Configurations["production"]
	if prod.Credentials["openai"] != "ref:openai" || prod.Credentials["qwen-ollama"] != "" {
		t.Errorf("credentials = %v", prod.Credentials)
	}
	if len(prod.Bindings) != 4 {
		t.Fatalf("bindings = %d, want 4", len(prod.Bindings))
	}
	if prod.Bindings[2].Group != "qwen-load-balance" {
		t.Errorf("binding[2] group = %q", prod.Bindings[2].Group)
	}
	foundry := prod.Bindings[3]
	if foundry.Backend != "azure-foundry" || foundry.Alias != "gpt-5.2-chat" || foundry.Tags[0] != "foundry" {
		t.Errorf("foundry binding = %+v", foundry)
	}
	if prod.PassthroughBindings[0].Family != "messages_batches" {
		t.Errorf("passthrough binding = %+v", prod.PassthroughBindings)
	}

	// Round-trips back to YAML without error.
	if _, err := yaml.Marshal(m); err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
}

func TestModelV2_JSONRoundTrip(t *testing.T) {
	in := contractsconfig.ModelV2{
		Backends: contractsconfig.BackendsConfig{
			"openai": {
				BaseURL: "https://api.openai.com",
				Query:   map[string]string{"api-version": "x"},
				Protocols: map[string]contractsconfig.BackendProtocol{
					"chat": {Path: "/v1/chat/completions", Auth: &contractsconfig.BackendAuth{Header: "Authorization", Format: "Bearer {key}"}},
				},
				Passthrough: map[string]contractsconfig.PassthroughFamily{
					"batches": {Paths: []contractsconfig.PassthroughPath{{Match: "/v1/batches", Methods: []string{"POST"}}}},
				},
			},
		},
		Groups: contractsconfig.GroupsConfig{
			"lb": {
				Mode:               resilience.ModeFailover,
				FailureStatusCodes: []int{503},
				CircuitBreaker:     &resilience.CircuitBreakerConfig{Enabled: true, FailureThreshold: 3},
				Targets:            []contractsconfig.Target{{Backend: "openai", Alias: "gpt-4o", Path: "/openai/responses"}},
			},
		},
		Configurations: map[string]contractsconfig.ConfigurationV2{
			"prod": {
				Credentials:         map[string]string{"openai": "ref:openai"},
				Bindings:            []contractsconfig.Binding{{Protocol: contractsconfig.ProtocolChat, Models: []string{"gpt-*"}, Backend: "openai", Tags: []string{"t"}}},
				PassthroughBindings: []contractsconfig.PassthroughBinding{{Family: "batches", Backend: "openai"}},
				RuleNames:           []string{"r"},
				Tags:                map[string]string{"tier": "prod"},
			},
		},
		APIKeys: contractsconfig.APIKeysConfig{{Secret: "sk", Name: "k", Configuration: "prod", Enabled: true}},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out contractsconfig.ModelV2
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Backends["openai"].Protocols["chat"].Auth.Format != "Bearer {key}" {
		t.Errorf("round-trip lost protocol auth: %+v", out.Backends["openai"])
	}
	if out.Groups["lb"].Targets[0].Alias != "gpt-4o" {
		t.Errorf("round-trip lost target alias: %+v", out.Groups["lb"].Targets)
	}
	if out.Configurations["prod"].Bindings[0].Backend != "openai" {
		t.Errorf("round-trip lost binding: %+v", out.Configurations["prod"].Bindings)
	}
}
