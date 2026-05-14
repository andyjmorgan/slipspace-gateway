package config_test

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
)

const gatewayYAML = `
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
    enabled: true
    endpoint: http://collector:4317
    protocol: grpc
  logging:
    format: json
    level: info
`

func TestGatewayConfig_YAMLRoundTrip(t *testing.T) {
	var g contractsconfig.GatewayConfig
	if err := yaml.Unmarshal([]byte(gatewayYAML), &g); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if g.HTTP.Bind != "0.0.0.0:8585" {
		t.Errorf("bind: %q", g.HTTP.Bind)
	}
	if g.Shutdown.DrainTimeoutSeconds != 300 {
		t.Errorf("drain: %d", g.Shutdown.DrainTimeoutSeconds)
	}
	if !g.Reporting.Enabled {
		t.Error("reporting disabled")
	}
	if g.Reporting.NATS.PublishQueueSize != 10000 {
		t.Errorf("queue: %d", g.Reporting.NATS.PublishQueueSize)
	}
	if g.Observability.Logging.Format != "json" {
		t.Errorf("logging format: %q", g.Observability.Logging.Format)
	}

	out, err := yaml.Marshal(g)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back contractsconfig.GatewayConfig
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if back != g {
		t.Errorf("round-trip mismatch")
	}
}

func TestProvidersConfig_YAMLRoundTrip(t *testing.T) {
	body := `
openai:
  base_url: https://api.openai.com
  endpoints:
    chat_completions:
      path: /v1/chat/completions
      method: [POST]
      accepted_paths: [/v1/chat/completions]
      accepts_streaming: true
      request_kind: chat
anthropic:
  base_url: https://api.anthropic.com
  required_headers:
    anthropic-version: "2023-06-01"
  endpoints:
    messages:
      path: /v1/messages
      method: [POST]
      accepted_paths: [/v1/messages]
      accepts_streaming: true
      request_kind: messages
`
	var p contractsconfig.ProvidersConfig
	if err := yaml.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := p["openai"].Endpoints["chat_completions"].RequestKind; got != "chat" {
		t.Errorf("request_kind = %q", got)
	}
	if got := p["anthropic"].RequiredHeaders["anthropic-version"]; got != "2023-06-01" {
		t.Errorf("required header = %q", got)
	}
}

func TestConfigurationsConfig_PreservesRulesAndResilience(t *testing.T) {
	body := `
dev:
  allowed_endpoints: [openai.chat]
  rules:
    - id: r1
      priority: 1
      foo: bar
  resilience:
    mode: failover
    timeout_seconds: 30
`
	var c contractsconfig.ConfigurationsConfig
	if err := yaml.Unmarshal([]byte(body), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(c["dev"].Rules) != 1 {
		t.Fatalf("rules len: %d", len(c["dev"].Rules))
	}
	if c["dev"].Resilience == nil {
		t.Fatal("resilience missing")
	}
	out, err := yaml.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("empty output")
	}
}

func TestAPIKey_JSONRoundTrip(t *testing.T) {
	//nolint:gosec // test fixture credentials
	in := contractsconfig.APIKey{
		Secret:        "sk_dev_xxx",
		Name:          "test",
		Configuration: "dev",
		Enabled:       true,
	}
	//nolint:gosec // serializing a test fixture
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back contractsconfig.APIKey
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back != in {
		t.Errorf("round-trip: %+v != %+v", back, in)
	}
}

func TestAPIKeysConfig_ListShape(t *testing.T) {
	body := `
- secret: sk_dev_a
  name: a
  configuration: dev
  enabled: true
- secret: sk_dev_b
  name: b
  configuration: dev
  enabled: false
`
	var keys contractsconfig.APIKeysConfig
	if err := yaml.Unmarshal([]byte(body), &keys); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("len = %d", len(keys))
	}
	if keys[0].Secret != "sk_dev_a" || keys[1].Enabled {
		t.Errorf("decoded wrong: %+v", keys)
	}
}
