package resilience

import (
	"fmt"

	"github.com/google/uuid"
)

// Validate enforces mode-specific invariants on a ResilienceConfig. An empty
// Mode is treated as ModeNone downstream and validates as such. Name is
// required at the schema level; ID is nullable but must not be the zero UUID
// when set (a defensive check for in-memory construction paths — the YAML and
// JSON unmarshal paths already reject malformed UUID strings).
//
// Mode-specific shape checks beyond "targets required":
//   - ModeFailover: every target must carry Order > 0 (the selector sorts
//     ascending; zero is ambiguous and would collide on tie).
//   - ModeLoadBalance / ModeLoadBalanceWithFailover: every target must
//     carry Weight > 0, and at least one target must be positive (the
//     cumulative-sum selector divides by total weight).
//
// Policy-wide failure_status_codes are validated to 400-599.
func (c *ResilienceConfig) Validate() error {
	if c.Name == "" {
		return ErrEmptyResilienceName
	}
	if c.ID != nil && *c.ID == uuid.Nil {
		return fmt.Errorf("resilience policy %q: %w", c.Name, ErrInvalidResilienceID)
	}
	switch c.Mode {
	case ModeNone, "":
	case ModeFailover, ModeLoadBalance, ModeLoadBalanceWithFailover:
		if len(c.Targets) == 0 {
			return fmt.Errorf("resilience: mode %q: %w", c.Mode, ErrTargetsRequired)
		}
	default:
		return fmt.Errorf("resilience: mode %q: %w", c.Mode, ErrUnknownMode)
	}

	if c.TimeoutSeconds < 0 {
		return fmt.Errorf("resilience: timeout_seconds %d: %w", c.TimeoutSeconds, ErrInvalidThreshold)
	}
	if c.ResponseHeaderTimeoutSeconds < 0 {
		return fmt.Errorf("resilience: response_header_timeout_seconds %d: %w", c.ResponseHeaderTimeoutSeconds, ErrInvalidThreshold)
	}
	for i, code := range c.FailureStatusCodes {
		if code < 400 || code > 599 {
			return fmt.Errorf("resilience: failure_status_codes[%d] %d: %w", i, code, ErrInvalidFailureStatusCode)
		}
	}

	if c.CircuitBreaker != nil {
		if err := c.CircuitBreaker.Validate(); err != nil {
			return fmt.Errorf("resilience: %w", err)
		}
	}
	if c.Retry != nil {
		if err := c.Retry.Validate(); err != nil {
			return fmt.Errorf("resilience: %w", err)
		}
	}

	names := make(map[string]int, len(c.Targets))
	for i, t := range c.Targets {
		if err := t.Validate(); err != nil {
			return fmt.Errorf("resilience: targets[%d]: %w", i, err)
		}
		if prev, ok := names[t.Name]; ok {
			return fmt.Errorf("resilience: targets[%d] and targets[%d] name=%q: %w", prev, i, t.Name, ErrDuplicateTargetName)
		}
		names[t.Name] = i
	}

	if err := validateModeShape(c); err != nil {
		return err
	}
	return nil
}

// validateModeShape enforces the per-mode Order / Weight invariants that
// can only be checked once every individual target has passed its own
// Validate (so we know the basics — non-empty name, non-negative numbers
// — already hold).
func validateModeShape(c *ResilienceConfig) error {
	switch c.Mode {
	case ModeFailover:
		for i, t := range c.Targets {
			if t.Order <= 0 {
				return fmt.Errorf("resilience: targets[%d] %q: %w", i, t.Name, ErrFailoverNeedsOrder)
			}
		}
	case ModeLoadBalance, ModeLoadBalanceWithFailover:
		total := 0
		for i, t := range c.Targets {
			if t.Weight <= 0 {
				return fmt.Errorf("resilience: targets[%d] %q: %w", i, t.Name, ErrLoadBalanceNeedsWeight)
			}
			total += t.Weight
		}
		if total == 0 {
			return fmt.Errorf("resilience: %w", ErrLoadBalanceNeedsWeight)
		}
	}
	return nil
}

// Validate enforces the per-target invariants. Name and Provider are
// required; numeric fields must be non-negative; failure_status_codes
// are restricted to 4xx/5xx; nested CircuitBreaker must itself validate.
//
// Mode-specific Order/Weight requirements (failover needs Order > 0,
// load_balance needs Weight > 0) are enforced at the parent
// ResilienceConfig level so the rule applies in context.
func (t *ResilienceTarget) Validate() error {
	if t.Name == "" {
		return ErrEmptyTargetName
	}
	if t.Provider == "" {
		return ErrEmptyProvider
	}
	if t.Order < 0 {
		return fmt.Errorf("order %d: %w", t.Order, ErrInvalidThreshold)
	}
	if t.Weight < 0 {
		return fmt.Errorf("weight %d: %w", t.Weight, ErrInvalidThreshold)
	}
	if t.TimeoutSeconds < 0 {
		return fmt.Errorf("timeout_seconds %d: %w", t.TimeoutSeconds, ErrInvalidThreshold)
	}
	for i, code := range t.FailureStatusCodes {
		if code < 400 || code > 599 {
			return fmt.Errorf("failure_status_codes[%d] %d: %w", i, code, ErrInvalidFailureStatusCode)
		}
	}
	if t.CircuitBreaker != nil {
		if err := t.CircuitBreaker.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Validate enforces numeric ranges on CircuitBreakerConfig. A disabled breaker
// is permitted to carry zero values; an enabled breaker requires sane bounds.
func (c *CircuitBreakerConfig) Validate() error {
	if c.FailureRateThreshold < 0 || c.FailureRateThreshold > 1 {
		return fmt.Errorf("%w: failure_rate_threshold %f out of [0,1]", ErrInvalidCircuitBreakerConfig, c.FailureRateThreshold)
	}
	if c.FailureThreshold < 0 {
		return fmt.Errorf("%w: failure_threshold %d", ErrInvalidCircuitBreakerConfig, c.FailureThreshold)
	}
	if c.SamplingDurationSeconds < 0 {
		return fmt.Errorf("%w: sampling_duration_seconds %d", ErrInvalidCircuitBreakerConfig, c.SamplingDurationSeconds)
	}
	if c.CooldownSeconds < 0 {
		return fmt.Errorf("%w: cooldown_seconds %d", ErrInvalidCircuitBreakerConfig, c.CooldownSeconds)
	}
	if c.HalfOpenSuccessThreshold < 0 {
		return fmt.Errorf("%w: half_open_success_threshold %d", ErrInvalidCircuitBreakerConfig, c.HalfOpenSuccessThreshold)
	}
	if c.MinimumThroughput < 0 {
		return fmt.Errorf("%w: minimum_throughput %d", ErrInvalidCircuitBreakerConfig, c.MinimumThroughput)
	}
	if !c.Enabled {
		return nil
	}
	if c.FailureThreshold == 0 && c.FailureRateThreshold == 0 {
		return fmt.Errorf("%w: enabled breaker needs failure_threshold or failure_rate_threshold", ErrInvalidCircuitBreakerConfig)
	}
	return nil
}

// Validate enforces numeric ranges and known backoff types on RetryConfig.
func (r *RetryConfig) Validate() error {
	switch r.BackoffType {
	case "", BackoffConstant, BackoffLinear, BackoffExponential:
	default:
		return fmt.Errorf("%w: backoff %q", ErrUnknownBackoffType, r.BackoffType)
	}
	if r.MaxAttempts < 0 {
		return fmt.Errorf("%w: max_attempts %d", ErrInvalidRetryConfig, r.MaxAttempts)
	}
	if r.DelayMilliseconds < 0 {
		return fmt.Errorf("%w: delay_ms %d", ErrInvalidRetryConfig, r.DelayMilliseconds)
	}
	if r.MaxDelayMs < 0 {
		return fmt.Errorf("%w: max_delay_ms %d", ErrInvalidRetryConfig, r.MaxDelayMs)
	}
	if r.MaxDelayMs != 0 && r.DelayMilliseconds > r.MaxDelayMs {
		return fmt.Errorf("%w: delay_ms %d exceeds max_delay_ms %d", ErrInvalidRetryConfig, r.DelayMilliseconds, r.MaxDelayMs)
	}
	if r.Enabled && r.MaxAttempts == 0 {
		return fmt.Errorf("%w: enabled retry needs max_attempts > 0", ErrInvalidRetryConfig)
	}
	return nil
}
