// Package resilience defines the public schema for the gateway's resilience
// policy. The schema is parsed and validated in v1.0 but evaluation is deferred
// to v1.2; see the "Resilience Schema + Engine" design note for the long-form.
package resilience

import (
	"github.com/google/uuid"

	"github.com/andyjmorgan/sluice-gateway/contracts/rules"
)

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

	// StrictWeights, when true on a Mode=load_balance policy, disables the
	// implicit failover-on-error re-roll. The first weighted selection
	// fails fast — used for canary mirroring where the operator wants the
	// underweight target's failure rate to surface in customer-facing
	// error metrics instead of being silently absorbed. Off by default.
	StrictWeights bool `yaml:"strict_weights,omitempty" json:"strict_weights,omitempty"`

	// ResponseHeaderTimeoutSeconds bounds the per-attempt "time to first
	// byte" — i.e., how long the orchestrator waits for the upstream to
	// emit the HTTP status line before treating the attempt as a timeout.
	// Maps to http.Transport.ResponseHeaderTimeout, not a context deadline,
	// so post-commit streaming responses are not capped by this value.
	//
	// When set (> 0) it replaces the gateway-wide default
	// (SLUICE_UPSTREAM_RESPONSE_HEADER_TIMEOUT_SECONDS, 120s) for every
	// attempt under this policy. It is deliberately not floored: a
	// failover / load-balance policy typically wants a *shorter* budget
	// than the single-shot default so it can abandon a slow target and
	// try a healthy one quickly. Zero leaves the gateway-wide default in
	// force.
	ResponseHeaderTimeoutSeconds int `yaml:"response_header_timeout_seconds,omitempty" json:"response_header_timeout_seconds,omitempty"`

	// FailureStatusCodes is the policy-wide retry-trigger set. Per-target
	// FailureStatusCodes on ResilienceTarget overrides this list for the
	// target it lives on. Empty falls back to "5xx is a failure" at the
	// orchestrator (v1.2).
	FailureStatusCodes []int `yaml:"failure_status_codes,omitempty" json:"failure_status_codes,omitempty"`
}

// ResilienceTarget is one upstream destination the orchestrator can route to.
type ResilienceTarget struct {
	// Name is the per-target telemetry label and admin display anchor.
	// Required from v1.2 onward; must be unique within the parent
	// ResilienceConfig.Targets list.
	Name string `yaml:"name" json:"name"`

	// Provider is the provider name (from providers.yaml) the orchestrator
	// dispatches to when this target is selected.
	Provider string `yaml:"provider" json:"provider"`

	// Order is the failover priority for ModeFailover; lower values are
	// tried first. Required to be > 0 when the parent policy is in
	// ModeFailover.
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

	// Actions is the polymorphic destination-mutation bundle the
	// orchestrator (v1.2) applies on each attempt against this target.
	// Reuses the rules engine's Action vocabulary — a target's actions
	// are exactly the same shape a rule may carry, so the orchestrator
	// dispatches through the existing applyAction machinery.
	//
	// Coexists with the legacy scalar fields (Provider, ModelRewrite,
	// FailureStatusCodes). When both are present, Actions wins for the
	// fields it covers; the v1.0 schema is preserved so existing YAML
	// keeps working untouched.
	Actions []rules.Action `yaml:"actions,omitempty" json:"actions,omitempty"`
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
