package resilience

import "errors"

// ErrUnknownMode is returned when ResilienceConfig.Mode is not one of the
// values declared in this package.
var ErrUnknownMode = errors.New("resilience: unknown mode")

// ErrUnknownBackoffType is returned when RetryConfig.BackoffType is not one of
// the BackoffX constants in this package.
var ErrUnknownBackoffType = errors.New("resilience: unknown backoff type")

// ErrTargetsRequired is returned when a mode that needs targets is configured
// without at least one entry in Targets.
var ErrTargetsRequired = errors.New("resilience: targets required for mode")

// ErrInvalidThreshold is returned when a numeric threshold falls outside its
// permitted range (e.g., FailureRateThreshold outside [0, 1]).
var ErrInvalidThreshold = errors.New("resilience: invalid threshold")

// ErrInvalidCircuitBreakerConfig is returned when CircuitBreakerConfig fails
// its own sub-validation.
var ErrInvalidCircuitBreakerConfig = errors.New("resilience: invalid circuit breaker config")

// ErrInvalidRetryConfig is returned when RetryConfig fails its own
// sub-validation.
var ErrInvalidRetryConfig = errors.New("resilience: invalid retry config")

// ErrEmptyProvider is returned when a ResilienceTarget omits its provider name.
var ErrEmptyProvider = errors.New("resilience: target provider required")

// ErrEmptyResilienceName is returned when a ResilienceConfig carries no Name.
// Name is the canonical handle referenced from Configuration.ResilienceName and
// is required at the schema level.
var ErrEmptyResilienceName = errors.New("resilience: policy name required")

// ErrInvalidResilienceID is returned when a ResilienceConfig.ID string fails
// to parse as a UUID during in-memory construction.
var ErrInvalidResilienceID = errors.New("resilience: policy id not a valid uuid")
