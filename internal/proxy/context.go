package proxy

import (
	"context"
	"time"
)

// ctxKey is the unexported context-key type for proxy-scoped per-request
// values, avoiding collisions with keys from other packages.
type ctxKey int

const responseHeaderTimeoutKey ctxKey = iota

// WithResponseHeaderTimeout returns a context carrying a per-request override
// of the transport's ResponseHeaderTimeout (time-to-first-byte cap). The
// resilience orchestrator sets this so an attempt under a load-balance /
// failover policy can use a shorter budget than the gateway-wide default,
// giving up on a slow target fast enough to try a healthy one. A non-positive
// d is ignored (the default applies). The override replaces — never floors —
// the default, so policies may deliberately set a short timeout.
func WithResponseHeaderTimeout(ctx context.Context, d time.Duration) context.Context {
	if d <= 0 {
		return ctx
	}
	return context.WithValue(ctx, responseHeaderTimeoutKey, d)
}

// ResponseHeaderTimeoutFromContext returns the per-request ResponseHeaderTimeout
// override set by WithResponseHeaderTimeout, and whether one was present.
func ResponseHeaderTimeoutFromContext(ctx context.Context) (time.Duration, bool) {
	d, ok := ctx.Value(responseHeaderTimeoutKey).(time.Duration)
	return d, ok
}
