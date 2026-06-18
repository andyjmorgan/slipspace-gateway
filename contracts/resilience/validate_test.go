package resilience_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/andyjmorgan/slipspace-gateway/contracts/resilience"
)

func TestResilienceConfig_Validate(t *testing.T) {
	validFailoverTarget := resilience.ResilienceTarget{Name: "primary", Provider: "openai", Order: 1}
	validLoadBalanceTarget := resilience.ResilienceTarget{Name: "primary", Provider: "openai", Weight: 1}

	tests := []struct {
		name    string
		cfg     resilience.ResilienceConfig
		wantErr error
	}{
		{
			name: "mode none ok",
			cfg:  resilience.ResilienceConfig{Name: "p", Mode: resilience.ModeNone},
		},
		{
			name: "empty mode treated as none",
			cfg:  resilience.ResilienceConfig{Name: "p"},
		},
		{
			name:    "empty name",
			cfg:     resilience.ResilienceConfig{Mode: resilience.ModeNone},
			wantErr: resilience.ErrEmptyResilienceName,
		},
		{
			name: "zero uuid id",
			cfg: resilience.ResilienceConfig{
				Name: "p",
				ID:   new(uuid.UUID),
				Mode: resilience.ModeNone,
			},
			wantErr: resilience.ErrInvalidResilienceID,
		},
		{
			name:    "unknown mode",
			cfg:     resilience.ResilienceConfig{Name: "p", Mode: "moon-orbit"},
			wantErr: resilience.ErrUnknownMode,
		},
		{
			name:    "failover without targets",
			cfg:     resilience.ResilienceConfig{Name: "p", Mode: resilience.ModeFailover},
			wantErr: resilience.ErrTargetsRequired,
		},
		{
			name:    "load_balance without targets",
			cfg:     resilience.ResilienceConfig{Name: "p", Mode: resilience.ModeLoadBalance},
			wantErr: resilience.ErrTargetsRequired,
		},
		{
			name:    "load_balance_with_failover without targets",
			cfg:     resilience.ResilienceConfig{Name: "p", Mode: resilience.ModeLoadBalanceWithFailover},
			wantErr: resilience.ErrTargetsRequired,
		},
		{
			name: "failover with one valid target",
			cfg: resilience.ResilienceConfig{
				Name:    "p",
				Mode:    resilience.ModeFailover,
				Targets: []resilience.ResilienceTarget{validFailoverTarget},
			},
		},
		{
			name: "failover target missing order",
			cfg: resilience.ResilienceConfig{
				Name:    "p",
				Mode:    resilience.ModeFailover,
				Targets: []resilience.ResilienceTarget{{Name: "primary", Provider: "openai"}},
			},
			wantErr: resilience.ErrFailoverNeedsOrder,
		},
		{
			name: "load_balance with one valid target",
			cfg: resilience.ResilienceConfig{
				Name:    "p",
				Mode:    resilience.ModeLoadBalance,
				Targets: []resilience.ResilienceTarget{validLoadBalanceTarget},
			},
		},
		{
			name: "load_balance target missing weight",
			cfg: resilience.ResilienceConfig{
				Name:    "p",
				Mode:    resilience.ModeLoadBalance,
				Targets: []resilience.ResilienceTarget{{Name: "primary", Provider: "openai"}},
			},
			wantErr: resilience.ErrLoadBalanceNeedsWeight,
		},
		{
			name: "load_balance_with_failover target missing weight",
			cfg: resilience.ResilienceConfig{
				Name:    "p",
				Mode:    resilience.ModeLoadBalanceWithFailover,
				Targets: []resilience.ResilienceTarget{{Name: "primary", Provider: "openai"}},
			},
			wantErr: resilience.ErrLoadBalanceNeedsWeight,
		},
		{
			name: "duplicate target names",
			cfg: resilience.ResilienceConfig{
				Name: "p",
				Mode: resilience.ModeFailover,
				Targets: []resilience.ResilienceTarget{
					{Name: "primary", Provider: "openai", Order: 1},
					{Name: "primary", Provider: "anthropic", Order: 2},
				},
			},
			wantErr: resilience.ErrDuplicateTargetName,
		},
		{
			name: "negative top-level timeout",
			cfg: resilience.ResilienceConfig{
				Name:           "p",
				Mode:           resilience.ModeNone,
				TimeoutSeconds: -1,
			},
			wantErr: resilience.ErrInvalidThreshold,
		},
		{
			name: "negative response header timeout",
			cfg: resilience.ResilienceConfig{
				Name:                         "p",
				Mode:                         resilience.ModeNone,
				ResponseHeaderTimeoutSeconds: -5,
			},
			wantErr: resilience.ErrInvalidThreshold,
		},
		{
			name: "invalid policy-wide failure status code",
			cfg: resilience.ResilienceConfig{
				Name:               "p",
				Mode:               resilience.ModeNone,
				FailureStatusCodes: []int{200},
			},
			wantErr: resilience.ErrInvalidFailureStatusCode,
		},
		{
			name: "valid policy-wide failure status codes",
			cfg: resilience.ResilienceConfig{
				Name:               "p",
				Mode:               resilience.ModeNone,
				FailureStatusCodes: []int{500, 502, 503, 504},
			},
		},
		{
			name: "invalid circuit breaker",
			cfg: resilience.ResilienceConfig{
				Name: "p",
				Mode: resilience.ModeNone,
				CircuitBreaker: &resilience.CircuitBreakerConfig{
					FailureRateThreshold: 2,
				},
			},
			wantErr: resilience.ErrInvalidCircuitBreakerConfig,
		},
		{
			name: "valid circuit breaker",
			cfg: resilience.ResilienceConfig{
				Name: "p",
				Mode: resilience.ModeNone,
				CircuitBreaker: &resilience.CircuitBreakerConfig{
					Enabled:                  true,
					FailureThreshold:         5,
					FailureRateThreshold:     0.5,
					SamplingDurationSeconds:  30,
					CooldownSeconds:          15,
					HalfOpenSuccessThreshold: 2,
					MinimumThroughput:        10,
				},
			},
		},
		{
			name: "invalid retry",
			cfg: resilience.ResilienceConfig{
				Name:  "p",
				Mode:  resilience.ModeNone,
				Retry: &resilience.RetryConfig{Enabled: true, MaxAttempts: 0},
			},
			wantErr: resilience.ErrInvalidRetryConfig,
		},
		{
			name: "valid retry",
			cfg: resilience.ResilienceConfig{
				Name: "p",
				Mode: resilience.ModeNone,
				Retry: &resilience.RetryConfig{
					Enabled:           true,
					MaxAttempts:       3,
					BackoffType:       resilience.BackoffExponential,
					DelayMilliseconds: 100,
					MaxDelayMs:        2000,
					UseJitter:         true,
				},
			},
		},
		{
			name: "invalid nested target",
			cfg: resilience.ResilienceConfig{
				Name: "p",
				Mode: resilience.ModeFailover,
				Targets: []resilience.ResilienceTarget{
					{Name: "primary", Provider: "", Order: 1},
				},
			},
			wantErr: resilience.ErrEmptyProvider,
		},
		{
			name: "strict_weights compiles on load_balance",
			cfg: resilience.ResilienceConfig{
				Name:          "p",
				Mode:          resilience.ModeLoadBalance,
				StrictWeights: true,
				Targets:       []resilience.ResilienceTarget{validLoadBalanceTarget},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error %v, got nil", tt.wantErr)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error chain: got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestResilienceTarget_Validate(t *testing.T) {
	tests := []struct {
		name    string
		target  resilience.ResilienceTarget
		wantErr error
	}{
		{
			name:    "missing name",
			target:  resilience.ResilienceTarget{Provider: "openai"},
			wantErr: resilience.ErrEmptyTargetName,
		},
		{
			name:    "missing provider",
			target:  resilience.ResilienceTarget{Name: "primary"},
			wantErr: resilience.ErrEmptyProvider,
		},
		{
			name: "valid minimal",
			target: resilience.ResilienceTarget{
				Name:     "primary",
				Provider: "openai",
			},
		},
		{
			name: "negative order",
			target: resilience.ResilienceTarget{
				Name:     "primary",
				Provider: "openai",
				Order:    -1,
			},
			wantErr: resilience.ErrInvalidThreshold,
		},
		{
			name: "negative weight",
			target: resilience.ResilienceTarget{
				Name:     "primary",
				Provider: "openai",
				Weight:   -1,
			},
			wantErr: resilience.ErrInvalidThreshold,
		},
		{
			name: "negative timeout",
			target: resilience.ResilienceTarget{
				Name:           "primary",
				Provider:       "openai",
				TimeoutSeconds: -1,
			},
			wantErr: resilience.ErrInvalidThreshold,
		},
		{
			name: "bad status code below 400",
			target: resilience.ResilienceTarget{
				Name:               "primary",
				Provider:           "openai",
				FailureStatusCodes: []int{200},
			},
			wantErr: resilience.ErrInvalidFailureStatusCode,
		},
		{
			name: "bad status code above 599",
			target: resilience.ResilienceTarget{
				Name:               "primary",
				Provider:           "openai",
				FailureStatusCodes: []int{700},
			},
			wantErr: resilience.ErrInvalidFailureStatusCode,
		},
		{
			name: "good status codes",
			target: resilience.ResilienceTarget{
				Name:               "primary",
				Provider:           "openai",
				FailureStatusCodes: []int{500, 502, 503},
			},
		},
		{
			name: "invalid nested breaker",
			target: resilience.ResilienceTarget{
				Name:     "primary",
				Provider: "openai",
				CircuitBreaker: &resilience.CircuitBreakerConfig{
					FailureRateThreshold: -1,
				},
			},
			wantErr: resilience.ErrInvalidCircuitBreakerConfig,
		},
		{
			name: "valid nested breaker",
			target: resilience.ResilienceTarget{
				Name:     "primary",
				Provider: "openai",
				CircuitBreaker: &resilience.CircuitBreakerConfig{
					Enabled:              true,
					FailureRateThreshold: 0.5,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.target.Validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error %v, got nil", tt.wantErr)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error chain: got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestCircuitBreakerConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cb      resilience.CircuitBreakerConfig
		wantErr error
	}{
		{
			name: "disabled with zeros",
			cb:   resilience.CircuitBreakerConfig{},
		},
		{
			name:    "rate above 1",
			cb:      resilience.CircuitBreakerConfig{FailureRateThreshold: 1.5},
			wantErr: resilience.ErrInvalidCircuitBreakerConfig,
		},
		{
			name:    "rate below 0",
			cb:      resilience.CircuitBreakerConfig{FailureRateThreshold: -0.1},
			wantErr: resilience.ErrInvalidCircuitBreakerConfig,
		},
		{
			name:    "negative failure threshold",
			cb:      resilience.CircuitBreakerConfig{FailureThreshold: -1},
			wantErr: resilience.ErrInvalidCircuitBreakerConfig,
		},
		{
			name:    "negative sampling duration",
			cb:      resilience.CircuitBreakerConfig{SamplingDurationSeconds: -1},
			wantErr: resilience.ErrInvalidCircuitBreakerConfig,
		},
		{
			name:    "negative cooldown",
			cb:      resilience.CircuitBreakerConfig{CooldownSeconds: -1},
			wantErr: resilience.ErrInvalidCircuitBreakerConfig,
		},
		{
			name:    "negative half-open threshold",
			cb:      resilience.CircuitBreakerConfig{HalfOpenSuccessThreshold: -1},
			wantErr: resilience.ErrInvalidCircuitBreakerConfig,
		},
		{
			name:    "negative minimum throughput",
			cb:      resilience.CircuitBreakerConfig{MinimumThroughput: -1},
			wantErr: resilience.ErrInvalidCircuitBreakerConfig,
		},
		{
			name:    "enabled without thresholds",
			cb:      resilience.CircuitBreakerConfig{Enabled: true},
			wantErr: resilience.ErrInvalidCircuitBreakerConfig,
		},
		{
			name: "enabled with rate threshold only",
			cb: resilience.CircuitBreakerConfig{
				Enabled:              true,
				FailureRateThreshold: 0.5,
			},
		},
		{
			name: "enabled with failure threshold only",
			cb: resilience.CircuitBreakerConfig{
				Enabled:          true,
				FailureThreshold: 5,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cb.Validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error %v, got nil", tt.wantErr)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error chain: got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestRetryConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		retry   resilience.RetryConfig
		wantErr error
	}{
		{
			name:  "empty fields disabled",
			retry: resilience.RetryConfig{},
		},
		{
			name:    "unknown backoff",
			retry:   resilience.RetryConfig{BackoffType: "fibonacci"},
			wantErr: resilience.ErrUnknownBackoffType,
		},
		{
			name:    "negative max attempts",
			retry:   resilience.RetryConfig{MaxAttempts: -1},
			wantErr: resilience.ErrInvalidRetryConfig,
		},
		{
			name:    "negative delay",
			retry:   resilience.RetryConfig{DelayMilliseconds: -1},
			wantErr: resilience.ErrInvalidRetryConfig,
		},
		{
			name:    "negative max delay",
			retry:   resilience.RetryConfig{MaxDelayMs: -1},
			wantErr: resilience.ErrInvalidRetryConfig,
		},
		{
			name: "delay exceeds max delay",
			retry: resilience.RetryConfig{
				DelayMilliseconds: 5000,
				MaxDelayMs:        100,
			},
			wantErr: resilience.ErrInvalidRetryConfig,
		},
		{
			name:    "enabled without attempts",
			retry:   resilience.RetryConfig{Enabled: true},
			wantErr: resilience.ErrInvalidRetryConfig,
		},
		{
			name: "valid linear",
			retry: resilience.RetryConfig{
				Enabled:           true,
				MaxAttempts:       3,
				BackoffType:       resilience.BackoffLinear,
				DelayMilliseconds: 100,
				MaxDelayMs:        1000,
			},
		},
		{
			name: "valid constant",
			retry: resilience.RetryConfig{
				Enabled:     true,
				MaxAttempts: 2,
				BackoffType: resilience.BackoffConstant,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.retry.Validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error %v, got nil", tt.wantErr)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error chain: got %v, want %v", err, tt.wantErr)
			}
		})
	}
}
