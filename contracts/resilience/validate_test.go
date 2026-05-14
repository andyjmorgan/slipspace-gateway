package resilience_test

import (
	"errors"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/contracts/resilience"
)

func TestResilienceConfig_Validate(t *testing.T) {
	validTarget := resilience.ResilienceTarget{Provider: "openai"}

	tests := []struct {
		name    string
		cfg     resilience.ResilienceConfig
		wantErr error
	}{
		{
			name: "mode none ok",
			cfg:  resilience.ResilienceConfig{Mode: resilience.ModeNone},
		},
		{
			name: "empty mode treated as none",
			cfg:  resilience.ResilienceConfig{},
		},
		{
			name:    "unknown mode",
			cfg:     resilience.ResilienceConfig{Mode: "moon-orbit"},
			wantErr: resilience.ErrUnknownMode,
		},
		{
			name:    "failover without targets",
			cfg:     resilience.ResilienceConfig{Mode: resilience.ModeFailover},
			wantErr: resilience.ErrTargetsRequired,
		},
		{
			name:    "load_balance without targets",
			cfg:     resilience.ResilienceConfig{Mode: resilience.ModeLoadBalance},
			wantErr: resilience.ErrTargetsRequired,
		},
		{
			name:    "load_balance_with_failover without targets",
			cfg:     resilience.ResilienceConfig{Mode: resilience.ModeLoadBalanceWithFailover},
			wantErr: resilience.ErrTargetsRequired,
		},
		{
			name: "failover with one target",
			cfg: resilience.ResilienceConfig{
				Mode:    resilience.ModeFailover,
				Targets: []resilience.ResilienceTarget{validTarget},
			},
		},
		{
			name: "negative top-level timeout",
			cfg: resilience.ResilienceConfig{
				Mode:           resilience.ModeNone,
				TimeoutSeconds: -1,
			},
			wantErr: resilience.ErrInvalidThreshold,
		},
		{
			name: "invalid circuit breaker",
			cfg: resilience.ResilienceConfig{
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
				Mode:  resilience.ModeNone,
				Retry: &resilience.RetryConfig{Enabled: true, MaxAttempts: 0},
			},
			wantErr: resilience.ErrInvalidRetryConfig,
		},
		{
			name: "valid retry",
			cfg: resilience.ResilienceConfig{
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
				Mode: resilience.ModeFailover,
				Targets: []resilience.ResilienceTarget{
					{Provider: ""},
				},
			},
			wantErr: resilience.ErrEmptyProvider,
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
			name:    "missing provider",
			target:  resilience.ResilienceTarget{},
			wantErr: resilience.ErrEmptyProvider,
		},
		{
			name: "valid minimal",
			target: resilience.ResilienceTarget{
				Provider: "openai",
			},
		},
		{
			name: "negative order",
			target: resilience.ResilienceTarget{
				Provider: "openai",
				Order:    -1,
			},
			wantErr: resilience.ErrInvalidThreshold,
		},
		{
			name: "negative weight",
			target: resilience.ResilienceTarget{
				Provider: "openai",
				Weight:   -1,
			},
			wantErr: resilience.ErrInvalidThreshold,
		},
		{
			name: "negative timeout",
			target: resilience.ResilienceTarget{
				Provider:       "openai",
				TimeoutSeconds: -1,
			},
			wantErr: resilience.ErrInvalidThreshold,
		},
		{
			name: "bad status code",
			target: resilience.ResilienceTarget{
				Provider:           "openai",
				FailureStatusCodes: []int{0},
			},
			wantErr: resilience.ErrInvalidThreshold,
		},
		{
			name: "good status codes",
			target: resilience.ResilienceTarget{
				Provider:           "openai",
				FailureStatusCodes: []int{500, 502, 503},
			},
		},
		{
			name: "invalid nested breaker",
			target: resilience.ResilienceTarget{
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
