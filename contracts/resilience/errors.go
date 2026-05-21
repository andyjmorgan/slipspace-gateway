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

// ErrEmptyTargetName is returned when a ResilienceTarget omits its Name.
// Name is the per-target telemetry label and admin display anchor; required
// at the schema level from v1.2 onward.
var ErrEmptyTargetName = errors.New("resilience: target name required")

// ErrDuplicateTargetName is returned when two targets within one
// ResilienceConfig share the same Name. Per-target metric labels and
// admin selectors assume Name uniqueness within a policy.
var ErrDuplicateTargetName = errors.New("resilience: duplicate target name within policy")

// ErrInvalidFailureStatusCode is returned when failure_status_codes contains
// a value outside the 400-599 range. The orchestrator's retry decision is
// only meaningful for client- or server-error responses.
var ErrInvalidFailureStatusCode = errors.New("resilience: failure_status_codes must be 4xx/5xx")

// ErrFailoverNeedsOrder is returned when a Mode=failover policy carries a
// target with Order <= 0. The failover selector sorts by Order ascending;
// zero/negative orders are ambiguous.
var ErrFailoverNeedsOrder = errors.New("resilience: failover target requires order > 0")

// ErrLoadBalanceNeedsWeight is returned when a Mode=load_balance policy
// carries a target with Weight == 0, or when every target has Weight == 0
// (the cumulative-sum selector would divide by zero).
var ErrLoadBalanceNeedsWeight = errors.New("resilience: load_balance target requires weight > 0")

// ErrEmptyResilienceName is returned when a ResilienceConfig carries no Name.
// Name is the canonical handle referenced from Configuration.ResilienceName and
// is required at the schema level.
var ErrEmptyResilienceName = errors.New("resilience: policy name required")

// ErrInvalidResilienceID is returned when a ResilienceConfig.ID string fails
// to parse as a UUID during in-memory construction.
var ErrInvalidResilienceID = errors.New("resilience: policy id not a valid uuid")
