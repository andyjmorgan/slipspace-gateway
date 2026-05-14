// Package resilience defines the public schema for the gateway's resilience
// policy. The schema is parsed and validated in v1.0 but evaluation is deferred
// to v1.1; see the "Resilience Schema + Engine" design note for the long-form.
package resilience

import "github.com/google/uuid"

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
	// Name is required; the human anchor used by logs, dashboards, and
	// Configuration.ResilienceName references.
	Name string `yaml:"name" json:"name"`

	// ID is optional; populated by the control plane when minted via the
	// management API. Empty in operator-authored static config — the gateway
	// emits a stable telemetry handle via Name in that case.
	ID *uuid.UUID `yaml:"id,omitempty" json:"id,omitempty"`

	// Mode selects the orchestration strategy. See the ModeX constants.
	Mode ResilienceMode `yaml:"mode" json:"mode"`

	// TimeoutSeconds bounds the wall-clock duration of a single orchestrated
	// attempt. Zero means no overall timeout.
	TimeoutSeconds int `yaml:"timeout_seconds,omitempty" json:"timeout_seconds,omitempty"`

	// Targets is the list of upstream destinations the orchestrator may
	// route to. Required for every Mode other than ModeNone.
	Targets []ResilienceTarget `yaml:"targets,omitempty" json:"targets,omitempty"`

	// CircuitBreaker is an optional policy-wide circuit breaker. Per-target
	// breakers on ResilienceTarget take precedence.
	CircuitBreaker *CircuitBreakerConfig `yaml:"circuit_breaker,omitempty" json:"circuit_breaker,omitempty"`

	// Retry configures retry attempts and backoff. Nil disables retries.
	Retry *RetryConfig `yaml:"retry,omitempty" json:"retry,omitempty"`
}

// ResilienceTarget is one upstream destination the orchestrator can route to.
type ResilienceTarget struct {
	// Provider is the provider name (from providers.yaml) the orchestrator
	// dispatches to when this target is selected.
	Provider string `yaml:"provider" json:"provider"`

	// Order is the failover priority for ModeFailover and
	// ModeLoadBalanceWithFailover; lower values are tried first.
	Order int `yaml:"order,omitempty" json:"order,omitempty"`

	// Weight is the relative weight for ModeLoadBalance and
	// ModeLoadBalanceWithFailover.
	Weight int `yaml:"weight,omitempty" json:"weight,omitempty"`

	// TimeoutSeconds bounds a single attempt against this target. Overrides
	// the parent ResilienceConfig.TimeoutSeconds for this target only.
	TimeoutSeconds int `yaml:"timeout_seconds,omitempty" json:"timeout_seconds,omitempty"`

	// ModelRewrite, when non-empty, rewrites the request body's model field
	// to this value when this target is selected. Enables cross-provider
	// failover with provider-specific model names.
	ModelRewrite string `yaml:"model_rewrite,omitempty" json:"model_rewrite,omitempty"`

	// FailureStatusCodes is the explicit list of upstream HTTP status codes
	// treated as a failure for retry/circuit-breaker accounting. Empty
	// defaults to "5xx is a failure".
	FailureStatusCodes []int `yaml:"failure_status_codes,omitempty" json:"failure_status_codes,omitempty"`

	// CircuitBreaker is an optional per-target circuit breaker; when set it
	// overrides ResilienceConfig.CircuitBreaker for this target.
	CircuitBreaker *CircuitBreakerConfig `yaml:"circuit_breaker,omitempty" json:"circuit_breaker,omitempty"`
}

// CircuitBreakerConfig configures the per-target circuit breaker state machine.
type CircuitBreakerConfig struct {
	// Enabled toggles the breaker. A disabled breaker permits every request
	// regardless of recent failures.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// FailureThreshold is the absolute failure count within the sampling
	// window that trips the breaker.
	FailureThreshold int `yaml:"failure_threshold" json:"failure_threshold"`

	// FailureRateThreshold is the failure ratio in [0, 1] within the
	// sampling window that trips the breaker. Considered alongside
	// FailureThreshold; either may trip.
	FailureRateThreshold float64 `yaml:"failure_rate_threshold" json:"failure_rate_threshold"`

	// SamplingDurationSeconds is the rolling window over which failures and
	// the failure rate are computed.
	SamplingDurationSeconds int `yaml:"sampling_duration_seconds" json:"sampling_duration_seconds"`

	// CooldownSeconds is how long the breaker stays Open before transitioning
	// to HalfOpen to probe the upstream.
	CooldownSeconds int `yaml:"cooldown_seconds" json:"cooldown_seconds"`

	// HalfOpenSuccessThreshold is the number of consecutive HalfOpen
	// successes required to transition back to Closed.
	HalfOpenSuccessThreshold int `yaml:"half_open_success_threshold" json:"half_open_success_threshold"`

	// MinimumThroughput is the minimum sample count before the rate
	// threshold can trip — prevents tripping on a couple of cold-start
	// failures.
	MinimumThroughput int `yaml:"minimum_throughput" json:"minimum_throughput"`
}

// RetryConfig configures retry attempts and inter-attempt backoff.
type RetryConfig struct {
	// Enabled toggles retries. With Enabled false the orchestrator makes a
	// single attempt regardless of other fields.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// MaxAttempts is the total attempt budget including the initial call.
	// Must be > 0 when Enabled is true.
	MaxAttempts int `yaml:"max_attempts" json:"max_attempts"`

	// BackoffType selects the inter-attempt delay curve. See the BackoffX
	// constants.
	BackoffType BackoffType `yaml:"backoff_type" json:"backoff_type"`

	// DelayMilliseconds is the base delay between attempts; the BackoffType
	// curve scales subsequent delays.
	DelayMilliseconds int `yaml:"delay_ms" json:"delay_milliseconds"`

	// MaxDelayMs caps the per-attempt delay so exponential backoff cannot
	// stretch beyond a reasonable bound. Zero means uncapped.
	MaxDelayMs int `yaml:"max_delay_ms" json:"max_delay_milliseconds"`

	// UseJitter adds random jitter to each delay so synchronised clients
	// don't retry in lockstep ("thundering herd").
	UseJitter bool `yaml:"use_jitter" json:"use_jitter"`
}
