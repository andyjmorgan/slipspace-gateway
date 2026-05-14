// Package resilience defines the public schema for the gateway's resilience
// policy. The schema is parsed and validated in v1.0 but evaluation is deferred
// to v1.1; see the "Resilience Schema + Engine" design note for the long-form.
package resilience

// ResilienceMode selects which orchestration strategy applies to a Configuration.
type ResilienceMode string

// Resilience modes accepted in YAML. v1.0 parses these but only ModeNone is
// honoured at runtime; the other modes activate when v1.1 wires the
// orchestrators.
const (
	ModeNone ResilienceMode = "none"

	ModeFailover ResilienceMode = "failover"

	ModeLoadBalance ResilienceMode = "load_balance"

	ModeLoadBalanceWithFailover ResilienceMode = "load_balance_with_failover"
)

// BackoffType selects the inter-attempt delay curve for retries.
type BackoffType string

// Backoff curves accepted by RetryConfig.BackoffType.
const (
	BackoffConstant BackoffType = "constant"

	BackoffLinear BackoffType = "linear"

	BackoffExponential BackoffType = "exponential"
)

// ResilienceConfig is the resilience policy attached to a Configuration.
// Mode determines which strategy is active; the relevant sub-config must be set.
type ResilienceConfig struct {
	Mode ResilienceMode `yaml:"mode" json:"mode"`

	TimeoutSeconds int `yaml:"timeout_seconds,omitempty" json:"timeout_seconds,omitempty"`

	Targets []ResilienceTarget `yaml:"targets,omitempty" json:"targets,omitempty"`

	CircuitBreaker *CircuitBreakerConfig `yaml:"circuit_breaker,omitempty" json:"circuit_breaker,omitempty"`

	Retry *RetryConfig `yaml:"retry,omitempty" json:"retry,omitempty"`
}

// ResilienceTarget is one upstream destination the orchestrator can route to.
type ResilienceTarget struct {
	Provider string `yaml:"provider" json:"provider"`

	Order int `yaml:"order,omitempty" json:"order,omitempty"`

	Weight int `yaml:"weight,omitempty" json:"weight,omitempty"`

	TimeoutSeconds int `yaml:"timeout_seconds,omitempty" json:"timeout_seconds,omitempty"`

	ModelRewrite string `yaml:"model_rewrite,omitempty" json:"model_rewrite,omitempty"`

	FailureStatusCodes []int `yaml:"failure_status_codes,omitempty" json:"failure_status_codes,omitempty"`

	CircuitBreaker *CircuitBreakerConfig `yaml:"circuit_breaker,omitempty" json:"circuit_breaker,omitempty"`
}

// CircuitBreakerConfig configures the per-target circuit breaker state machine.
type CircuitBreakerConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled"`

	FailureThreshold int `yaml:"failure_threshold" json:"failure_threshold"`

	FailureRateThreshold float64 `yaml:"failure_rate_threshold" json:"failure_rate_threshold"`

	SamplingDurationSeconds int `yaml:"sampling_duration_seconds" json:"sampling_duration_seconds"`

	CooldownSeconds int `yaml:"cooldown_seconds" json:"cooldown_seconds"`

	HalfOpenSuccessThreshold int `yaml:"half_open_success_threshold" json:"half_open_success_threshold"`

	MinimumThroughput int `yaml:"minimum_throughput" json:"minimum_throughput"`
}

// RetryConfig configures retry attempts and inter-attempt backoff.
type RetryConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled"`

	MaxAttempts int `yaml:"max_attempts" json:"max_attempts"`

	BackoffType BackoffType `yaml:"backoff_type" json:"backoff_type"`

	DelayMilliseconds int `yaml:"delay_ms" json:"delay_milliseconds"`

	MaxDelayMs int `yaml:"max_delay_ms" json:"max_delay_milliseconds"`

	UseJitter bool `yaml:"use_jitter" json:"use_jitter"`
}
