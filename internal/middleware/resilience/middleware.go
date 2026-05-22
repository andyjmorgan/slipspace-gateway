package resilience

import (
	"context"
	"log/slog"
	"net/http"

	contractsres "github.com/andyjmorgan/sluice-gateway/contracts/resilience"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/bodycapture"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

// PolicyLookup resolves a policy by name. Returns nil when the name
// is unknown — the middleware treats unknown as "no policy" and
// degrades to single-shot passthrough rather than failing the
// request, because the config loader has already cross-validated
// every rule-action policy reference and the only way to reach an
// unknown name at runtime is a startup-after-rewrite race operators
// are not supposed to hit.
type PolicyLookup func(name string) *contractsres.ResilienceConfig

// HTTPHandler is the v1.2 resilience orchestrator middleware. It
// sits between the rules engine and the body re-marshal step so any
// per-target body mutation (e.g. changeModelName inside Target.
// Actions) is re-encoded by BodyRemarshalHandler before the
// forwarder reads r.Body.
//
// v1.2 single-target degenerate path (this PR):
//
//   - state.PolicyRef empty → passthrough; downstream sees the same
//     state the rules engine produced.
//   - state.PolicyRef set but policy unknown or has zero targets →
//     passthrough; same as empty.
//   - state.PolicyRef set, policy has ≥1 target → clone MutableState,
//     apply the first target's Actions on the clone, replace the
//     state on context with the clone, and let the downstream
//     (BodyRemarshal → final → forwarder) proceed normally.
//
// Multi-target failover / load-balance / circuit-breaker land in
// PR-7 onward; the skeleton here proves the pipeline position is
// stable and the legacy single-shot path is regression-free.
//
// A target with no Actions is a no-op for this middleware — the
// rules engine already set the destination via its own actions, and
// the legacy scalar fields on Target (Provider, ModelRewrite) are
// read by PR-7+ when the orchestrator actually picks among multiple
// targets; PR-6 leaves them untouched.
func HTTPHandler(lookup PolicyLookup, next http.Handler) http.Handler {
	if next == nil {
		panic("resilience: HTTPHandler called with nil next handler")
	}
	if lookup == nil {
		// A nil lookup degrades to passthrough — the gateway can
		// boot without any resilience policies declared, in which
		// case no PolicyRef ever appears and the lookup is never
		// invoked anyway. Guarding here keeps the constructor's
		// contract simple.
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		state := rules.MutableStateFromContext(ctx)
		if state == nil || state.PolicyRef == "" {
			next.ServeHTTP(w, r)
			return
		}

		pol := lookup(state.PolicyRef)
		if pol == nil || len(pol.Targets) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		// Single-target degenerate path. Multi-target selection
		// lands in PR-7+.
		target := pol.Targets[0]
		if len(target.Actions) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		clone := state.Clone()
		body := typedBodyFromContext(ctx)

		log := observability.FromContext(ctx)
		log.DebugContext(ctx, "resilience: applying target actions",
			slog.String("policy", pol.Name),
			slog.String("target", target.Name),
			slog.Int("actions", len(target.Actions)),
		)

		for i, act := range target.Actions {
			if _, err := rules.ApplyAction(act, clone, body); err != nil {
				log.ErrorContext(ctx, "resilience: target action failed",
					slog.String("policy", pol.Name),
					slog.String("target", target.Name),
					slog.Int("action_index", i),
					slog.Any("error", err),
				)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
		}

		ctx = rules.WithMutableState(ctx, clone)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// typedBodyFromContext fishes the typed request body off the
// bodycapture stash. Returns nil when no typed body was captured
// (admin routes, unknown endpoints) — ApplyAction tolerates that.
func typedBodyFromContext(ctx context.Context) any {
	captured, ok := bodycapture.FromContext(ctx)
	if !ok {
		return nil
	}
	return captured.Body
}
