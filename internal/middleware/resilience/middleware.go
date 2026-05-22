package resilience

import (
	"context"
	"log/slog"
	"net/http"
	"sort"

	contractsres "github.com/andyjmorgan/sluice-gateway/contracts/resilience"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/bodycapture"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/proxy"
)

// PolicyLookup resolves a policy by name. Returns nil when the name
// is unknown — the middleware treats unknown as "no policy" and
// degrades to single-shot passthrough rather than failing the
// request, because the config loader has already cross-validated
// every rule-action policy reference and the only way to reach an
// unknown name at runtime is a startup-after-rewrite race operators
// are not supposed to hit.
type PolicyLookup func(name string) *contractsres.ResilienceConfig

// defaultFailureStatusCodes is the orchestrator's fall-back retry
// set when neither the policy nor the target declares one. Matches
// the 5xx-class of upstream server errors operators usually want
// retried; 4xx-class is excluded because client errors don't get
// better on retry.
var defaultFailureStatusCodes = []int{500, 502, 503, 504}

// HTTPHandler is the v1.2 resilience orchestrator middleware. It
// sits between the rules engine and the body re-marshal step so any
// per-target body mutation (e.g. changeModelName inside Target.
// Actions) is re-encoded by BodyRemarshalHandler before the
// forwarder reads r.Body.
//
// Dispatch on policy mode:
//
//   - state.PolicyRef empty / policy unknown / zero targets →
//     passthrough (single-shot, today's behaviour).
//   - ModeFailover → sort targets by Order ascending, attempt each
//     in turn. A retryable outcome (status in the policy's effective
//     failure_status_codes OR transport error) discards the response
//     and proceeds to the next target. The first non-retryable
//     outcome commits to the client. All-failed writes the last
//     attempt's status to the client (or 502 if every attempt was a
//     transport error).
//   - ModeNone / ModeLoadBalance / ModeLoadBalanceWithFailover →
//     single-target degenerate path (apply the first target's
//     Actions, forward once). load_balance modes get their proper
//     selector in PR-8.
//
// Known limitations (documented for v1.2):
//
//   - Body-mutating Target.Actions (changeModelName) across multiple
//     attempts may leak state between attempts because the typed
//     body is shared. The fix is body restoration via re-parse from
//     Captured.Raw before each attempt; deferred to a follow-up.
//   - Each attempt fires its own Observer lifecycle, so multi-attempt
//     requests generate N gateway.request events. PR-10 collapses
//     these to one event with embedded Attempts[].
//   - Policy.ResponseHeaderTimeoutSeconds is parsed but not yet
//     applied per-attempt — the global Transport.ResponseHeaderTimeout
//     applies to every attempt. Per-target Transports deferred.
func HTTPHandler(lookup PolicyLookup, next http.Handler) http.Handler {
	if next == nil {
		panic("resilience: HTTPHandler called with nil next handler")
	}
	if lookup == nil {
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

		if pol.Mode == contractsres.ModeFailover {
			runFailover(w, r, pol, state, next)
			return
		}

		// Default path (ModeNone, ModeLoadBalance pending PR-8,
		// ModeLoadBalanceWithFailover pending PR-8): single-target
		// degenerate. Apply the first target's Actions if any, then
		// forward once.
		runSingleTarget(w, r, pol, pol.Targets[0], state, next)
	})
}

// runSingleTarget applies one target's actions and forwards exactly
// once. Mirrors the PR-6 behaviour; preserved here as the default
// path for any mode the orchestrator does not yet specialise.
func runSingleTarget(
	w http.ResponseWriter,
	r *http.Request,
	pol *contractsres.ResilienceConfig,
	target contractsres.ResilienceTarget,
	state *rules.MutableState,
	next http.Handler,
) {
	ctx := r.Context()
	if len(target.Actions) == 0 {
		next.ServeHTTP(w, r)
		return
	}
	clone, err := buildAttemptState(ctx, state, target)
	if err != nil {
		logger := observability.FromContext(ctx)
		logger.ErrorContext(ctx, "resilience: target action failed",
			slog.String("policy", pol.Name),
			slog.String("target", target.Name),
			slog.Any("error", err),
		)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	ctx = rules.WithMutableState(ctx, clone)
	next.ServeHTTP(w, r.WithContext(ctx))
}

// runFailover iterates the policy's targets in Order ascending,
// retrying on each retryable outcome until one commits or the list
// is exhausted.
//
// Per attempt:
//
//  1. Snapshot baseline state once (the state the rules engine
//     produced before this middleware ran). Each attempt clones
//     baseline and applies its target's Actions — no two attempts
//     stack their mutations.
//  2. Wrap w in a fresh BufferingResponseWriter scoped to the
//     attempt's effective failure_status_codes (per-target overrides
//     policy-level, falls back to default 5xx).
//  3. Call next.ServeHTTP(buf, ...). The downstream BodyRemarshal +
//     forwarder writes to buf. On retry-set status or transport
//     error, buf swallows the response; on any other outcome it
//     passes through and Committed flips true.
//  4. If buf.ShouldRetry() and more targets remain, try the next.
//     Otherwise the response has already reached the client (commit
//     path) or the orchestrator writes a fallback status (all
//     attempts retryable but list exhausted).
func runFailover(
	w http.ResponseWriter,
	r *http.Request,
	pol *contractsres.ResilienceConfig,
	state *rules.MutableState,
	next http.Handler,
) {
	ctx := r.Context()
	logger := observability.FromContext(ctx)

	targets := sortedFailoverTargets(pol.Targets)

	var lastStatus int
	var lastErr error

	// Snapshot the inbound raw body once so each attempt sees a
	// fresh r.Body. ReverseProxy consumes r.Body during a single
	// attempt; without this restore the second attempt's request
	// body would be empty against the ContentLength the inbound
	// declared.
	rawBody := capturedRawBody(ctx)

	for i, target := range targets {
		clone, err := buildAttemptState(ctx, state, target)
		if err != nil {
			logger.ErrorContext(ctx, "resilience: target action failed",
				slog.String("policy", pol.Name),
				slog.String("target", target.Name),
				slog.Int("attempt", i+1),
				slog.Any("error", err),
			)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		if rawBody != nil {
			// Restore r.Body before each attempt. BodyRemarshal
			// downstream will re-encode from the typed body if
			// state.BodyMutated is true; otherwise the raw bytes
			// reach the forwarder verbatim.
			bodycapture.ApplyBodyBytes(r, rawBody)
		}

		retrySet := effectiveFailureStatusCodes(pol, target)
		buf := proxy.NewBufferingResponseWriter(w, retrySet)

		attemptCtx := rules.WithMutableState(ctx, clone)
		logger.DebugContext(ctx, "resilience: failover attempt",
			slog.String("policy", pol.Name),
			slog.String("target", target.Name),
			slog.Int("attempt", i+1),
			slog.Int("of", len(targets)),
		)

		next.ServeHTTP(buf, r.WithContext(attemptCtx))

		if !buf.ShouldRetry() {
			// Committed (or no retry hint) — the response is either
			// already on the wire or there is nothing to retry with.
			return
		}

		lastStatus = buf.StatusCode()
		lastErr = buf.TransportError()
		logger.InfoContext(ctx, "resilience: failover attempt failed",
			slog.String("policy", pol.Name),
			slog.String("target", target.Name),
			slog.Int("attempt", i+1),
			slog.Int("status", lastStatus),
			slog.Any("transport_error", lastErr),
		)
	}

	// All targets exhausted without a commit. Write the last
	// attempt's status code, or 502 if every attempt was a transport
	// error with no status ever observed.
	status := lastStatus
	if status == 0 {
		status = http.StatusBadGateway
	}
	logger.ErrorContext(ctx, "resilience: failover exhausted",
		slog.String("policy", pol.Name),
		slog.Int("attempts", len(targets)),
		slog.Int("final_status", status),
	)
	http.Error(w, http.StatusText(status), status)
}

// buildAttemptState clones the baseline state and applies the
// target's Actions onto the clone. Returns the clone on success or
// (nil, err) when any action's ApplyAction returns an error — the
// orchestrator surfaces those as 500 to the client.
func buildAttemptState(
	ctx context.Context,
	baseline *rules.MutableState,
	target contractsres.ResilienceTarget,
) (*rules.MutableState, error) {
	clone := baseline.Clone()
	if len(target.Actions) == 0 {
		return clone, nil
	}
	body := typedBodyFromContext(ctx)
	for _, act := range target.Actions {
		if _, err := rules.ApplyAction(act, clone, body); err != nil {
			return nil, err
		}
	}
	return clone, nil
}

// sortedFailoverTargets returns a copy of targets sorted by Order
// ascending. Ties (same Order value) preserve declaration order via
// sort.SliceStable so behaviour is deterministic for operators who
// share an Order on purpose (no judgement here, just predictability).
func sortedFailoverTargets(targets []contractsres.ResilienceTarget) []contractsres.ResilienceTarget {
	out := make([]contractsres.ResilienceTarget, len(targets))
	copy(out, targets)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

// effectiveFailureStatusCodes is the retry set the orchestrator
// applies to a single attempt. Resolution order:
//
//  1. Per-target FailureStatusCodes (the target's override).
//  2. Policy-level FailureStatusCodes.
//  3. defaultFailureStatusCodes (5xx-class).
//
// Empty list at every level falls through to the default so an
// operator cannot accidentally produce a "no status ever retries"
// configuration without explicitly meaning to.
func effectiveFailureStatusCodes(pol *contractsres.ResilienceConfig, target contractsres.ResilienceTarget) []int {
	if len(target.FailureStatusCodes) > 0 {
		return target.FailureStatusCodes
	}
	if len(pol.FailureStatusCodes) > 0 {
		return pol.FailureStatusCodes
	}
	return defaultFailureStatusCodes
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

// capturedRawBody returns the inbound request's raw bytes as
// bodycapture stashed them, or nil when bodycapture did not run
// (admin routes, unknown endpoints) — in which case the orchestrator
// has nothing to restore and any retry will fail with the same body-
// already-consumed error the first attempt produced.
func capturedRawBody(ctx context.Context) []byte {
	captured, ok := bodycapture.FromContext(ctx)
	if !ok {
		return nil
	}
	return captured.Raw
}
