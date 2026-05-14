package resilience_test

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/andyjmorgan/sluice-gateway/contracts/resilience"
)

const sampleYAML = `
mode: load_balance_with_failover
timeout_seconds: 30
targets:
  - provider: openai
    order: 1
    weight: 7
    timeout_seconds: 20
    model_rewrite: gpt-4o-mini
    failure_status_codes: [500, 502, 503]
    circuit_breaker:
      enabled: true
      failure_threshold: 5
      failure_rate_threshold: 0.5
      sampling_duration_seconds: 30
      cooldown_seconds: 15
      half_open_success_threshold: 2
      minimum_throughput: 10
  - provider: anthropic
    order: 2
    weight: 3
circuit_breaker:
  enabled: true
  failure_threshold: 10
  failure_rate_threshold: 0.6
  sampling_duration_seconds: 60
  cooldown_seconds: 30
  half_open_success_threshold: 3
  minimum_throughput: 20
retry:
  enabled: true
  max_attempts: 4
  backoff_type: exponential
  delay_ms: 200
  max_delay_ms: 5000
  use_jitter: true
`

func TestResilienceConfig_YAMLRoundTrip(t *testing.T) {
	var cfg resilience.ResilienceConfig
	if err := yaml.Unmarshal([]byte(sampleYAML), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if cfg.Mode != resilience.ModeLoadBalanceWithFailover {
		t.Errorf("mode = %q", cfg.Mode)
	}
	if cfg.TimeoutSeconds != 30 {
		t.Errorf("timeout_seconds = %d", cfg.TimeoutSeconds)
	}
	if len(cfg.Targets) != 2 {
		t.Fatalf("targets len = %d", len(cfg.Targets))
	}
	first := cfg.Targets[0]
	if first.Provider != "openai" || first.Weight != 7 || first.ModelRewrite != "gpt-4o-mini" {
		t.Errorf("unexpected first target: %+v", first)
	}
	if len(first.FailureStatusCodes) != 3 || first.FailureStatusCodes[0] != 500 {
		t.Errorf("failure status codes: %v", first.FailureStatusCodes)
	}
	if first.CircuitBreaker == nil || !first.CircuitBreaker.Enabled {
		t.Errorf("per-target circuit breaker missing or disabled")
	}
	if cfg.CircuitBreaker == nil || cfg.CircuitBreaker.HalfOpenSuccessThreshold != 3 {
		t.Errorf("top-level circuit breaker: %+v", cfg.CircuitBreaker)
	}
	if cfg.Retry == nil || cfg.Retry.BackoffType != resilience.BackoffExponential {
		t.Errorf("retry: %+v", cfg.Retry)
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	out, err := yaml.Marshal(&cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back resilience.ResilienceConfig
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if back.Mode != cfg.Mode || len(back.Targets) != len(cfg.Targets) {
		t.Errorf("round-trip diverged: %+v", back)
	}
}

func TestResilienceConfig_JSONRoundTrip(t *testing.T) {
	cfg := resilience.ResilienceConfig{
		Mode:           resilience.ModeFailover,
		TimeoutSeconds: 10,
		Targets: []resilience.ResilienceTarget{
			{Provider: "openai", Order: 1},
		},
		Retry: &resilience.RetryConfig{
			Enabled:           true,
			MaxAttempts:       2,
			BackoffType:       resilience.BackoffConstant,
			DelayMilliseconds: 100,
		},
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back resilience.ResilienceConfig
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Mode != cfg.Mode || back.TimeoutSeconds != cfg.TimeoutSeconds {
		t.Errorf("mode/timeout diverged: %+v", back)
	}
	if len(back.Targets) != 1 || back.Targets[0].Provider != "openai" {
		t.Errorf("targets diverged: %+v", back.Targets)
	}
	if back.Retry == nil || back.Retry.BackoffType != resilience.BackoffConstant {
		t.Errorf("retry diverged: %+v", back.Retry)
	}
}
