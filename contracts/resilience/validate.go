package resilience

import "fmt"

// Validate enforces mode-specific invariants on a ResilienceConfig. An empty
// Mode is treated as ModeNone downstream and validates as such.
func (c *ResilienceConfig) Validate() error {
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
	for i, t := range c.Targets {
		if err := t.Validate(); err != nil {
			return fmt.Errorf("resilience: targets[%d]: %w", i, err)
		}
	}
	return nil
}

// Validate enforces the per-target invariants. Provider is required; numeric
// fields must be non-negative; nested CircuitBreaker must itself validate.
func (t *ResilienceTarget) Validate() error {
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
		if code < 100 || code > 599 {
			return fmt.Errorf("failure_status_codes[%d] %d: %w", i, code, ErrInvalidThreshold)
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
