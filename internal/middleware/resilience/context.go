package resilience

import (
	"context"

	contractsres "github.com/andyjmorgan/slipspace-gateway/contracts/resilience"
)

type resilienceConfigKey struct{}

// WithResilienceConfig stashes a per-request resilience config on ctx. The v2
// selection middleware synthesises a ResilienceConfig from the chosen binding
// (a single-target ModeNone policy, or a multi-target group) and stashes it
// here; the orchestrator reads it in preference to the name-keyed PolicyLookup.
// This is how the v2 model reuses the v1.2 orchestrator unchanged: routing is
// config data, so there is no named policy library to look the policy up in —
// the binding carries it.
func WithResilienceConfig(ctx context.Context, cfg *contractsres.ResilienceConfig) context.Context {
	return context.WithValue(ctx, resilienceConfigKey{}, cfg)
}

// ResilienceConfigFromContext returns the per-request resilience config the
// selection middleware stashed, or nil when none was set (v1 paths and tests
// that drive the orchestrator via PolicyLookup).
func ResilienceConfigFromContext(ctx context.Context) *contractsres.ResilienceConfig {
	if ctx == nil {
		return nil
	}
	cfg, _ := ctx.Value(resilienceConfigKey{}).(*contractsres.ResilienceConfig)
	return cfg
}
