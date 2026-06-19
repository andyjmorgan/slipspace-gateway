package resilience

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sort"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/andyjmorgan/slipspace-gateway/contracts/events"
	contractsres "github.com/andyjmorgan/slipspace-gateway/contracts/resilience"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/bodycapture"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
	"github.com/andyjmorgan/slipspace-gateway/internal/proxy"
)

// Attempt-outcome strings carried in events.AttemptRecord.Outcome and
// mirrored by the gateway.resilience.attempts.total counter's outcome
// label — the wire field and the metric label share one vocabulary.
const (
	attemptOutcomeSuccess        = "success"
	attemptOutcomeFailureStatus  = "failure_status"
	attemptOutcomeTransportError = "transport_error"
	attemptOutcomeCBBlocked      = "cb_blocked"
)

// Per-request orchestrator outcome strings emitted on
// gateway.resilience.outcome.total. One value per inbound request that
// runs through a policy.
const (
	orchestratorOutcomeSuccess   = "success"
	orchestratorOutcomeAllFailed = "all_failed"
	orchestratorOutcomeAllOpen   = "all_open"
)

// randomIntN is the package-level RNG hook the weighted load-balance
// selector calls. Stubbed at test time so deterministic distribution
// assertions are possible without seeding the global RNG; defaults to
// math/rand/v2 which is concurrent-safe and seeded automatically per
// program.
var randomIntN = func(n int) int {
	if n <= 0 {
		return 0
	}
	// math/rand/v2 is fine here — load-balance selection is not a
	// security boundary; crypto/rand would be overkill and slower.
	return rand.IntN(n) //nolint:gosec // weighted selection, not a secret
}

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
//   - ModeLoadBalance / ModeLoadBalanceWithFailover → weighted-random
//     target selection per request via the cumulative-weight
//     algorithm. By default re-rolls on retryable failure from the
//     remaining-healthy pool (LBWF semantics); when the policy sets
//     StrictWeights=true the first selection wins or fails without
//     re-roll, used for canary mirroring where the under-weighted
//     target's failure rate should surface to the client.
//   - ModeNone → single-target degenerate path (apply the first
//     target's Actions, forward once).
//
// Known limitations (documented for v1.2):
//
//   - Body-mutating Target.Actions (changeModelName) across multiple
//     attempts may leak state between attempts because the typed
//     body is shared. The fix is body restoration via re-parse from
//     Captured.Raw before each attempt; deferred to a follow-up.
//   - Per-attempt OTel meters (RequestsTotal, RequestDuration, TTFB,
//     UpstreamErrors) still fire from the per-attempt reporter
//     observer. PR-10b refines the meter semantics to per-request and
//     adds the dedicated gateway.resilience.* counters.
//   - Policy.ResponseHeaderTimeoutSeconds, when set, overrides the
//     gateway-wide Transport.ResponseHeaderTimeout for every attempt
//     under the policy (see withPolicyResponseHeaderTimeout) — the
//     orchestrator stamps it on the attempt context and the forwarder
//     keys a per-timeout transport off it. Per-target overrides of the
//     header timeout are not modelled; ResilienceTarget.TimeoutSeconds
//     (a whole-attempt wall-clock bound) remains unwired.
func HTTPHandler(lookup PolicyLookup, breakers BreakerStore, meters *observability.Meters, next http.Handler) http.Handler {
	if next == nil {
		panic("resilience: HTTPHandler called with nil next handler")
	}
	if breakers == nil {
		breakers = noopBreakerStore{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		state := rules.MutableStateFromContext(ctx)
		if state == nil {
			next.ServeHTTP(w, r)
			return
		}

		// v2 path: the selection middleware synthesises the per-request
		// policy from the chosen binding and stashes it on context. v1/test
		// path: resolve it by name from the PolicyLookup library.
		pol := ResilienceConfigFromContext(ctx)
		if pol == nil {
			if lookup == nil || state.PolicyRef == "" {
				next.ServeHTTP(w, r)
				return
			}
			pol = lookup(state.PolicyRef)
		}
		if pol == nil || len(pol.Targets) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		switch pol.Mode {
		case contractsres.ModeFailover:
			runFailover(w, r, pol, state, breakers, meters, next)
			return
		case contractsres.ModeLoadBalance, contractsres.ModeLoadBalanceWithFailover:
			runLoadBalance(w, r, pol, state, breakers, meters, next)
			return
		}

		// Default path (ModeNone, "", unknown future modes):
		// single-target degenerate. Apply the first target's Actions
		// if any, then forward once.
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
	ctx := withPolicyResponseHeaderTimeout(r.Context(), pol)
	if len(target.Actions) == 0 {
		next.ServeHTTP(w, r.WithContext(ctx))
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
	breakers BreakerStore,
	meters *observability.Meters,
	next http.Handler,
) {
	ctx := r.Context()
	logger := observability.FromContext(ctx)

	orchStart := time.Now()
	abuf := NewAttemptBuffer(pol.Name)
	ctx = WithAttemptBuffer(ctx, abuf)
	ctx = withPolicyResponseHeaderTimeout(ctx, pol)
	r = r.WithContext(ctx)

	// finalStatus is the status the orchestrator surfaces to the
	// client at the end of the run — either the committed attempt's
	// status (commit path) or the orchestrator's fallback (exhausted
	// path). Captured by the deferred FireTerminal so the
	// consolidated gateway.request event carries the
	// client-observable status, not whatever the last per-attempt
	// observer happened to record.
	var finalStatus int
	var orchestratorOutcome string
	totalTargets := len(pol.Targets)
	var realAttempts int
	defer func() {
		abuf.FireTerminal(time.Since(orchStart).Milliseconds(), finalStatus)
		emitOrchestratorOutcome(ctx, meters, pol.Name, orchestratorOutcome, realAttempts)
	}()

	targets := sortedFailoverTargets(pol.Targets)

	var lastStatus int
	var lastErr error
	attempts := 0

	// Snapshot the inbound raw body once so each attempt sees a
	// fresh r.Body. ReverseProxy consumes r.Body during a single
	// attempt; without this restore the second attempt's request
	// body would be empty against the ContentLength the inbound
	// declared.
	rawBody := capturedRawBody(ctx)

	for i, target := range targets {
		cb := effectiveCircuitBreaker(pol, target)
		if !breakers.Allow(pol.Name, target.Name, cb) {
			logger.InfoContext(ctx, "resilience: circuit breaker open, skipping target",
				slog.String("policy", pol.Name),
				slog.String("target", target.Name),
				slog.Int("position", i+1),
			)
			abuf.Record(events.AttemptRecord{
				Target:    target.Name,
				StartedAt: time.Now().UTC(),
				Outcome:   attemptOutcomeCBBlocked,
			})
			emitAttemptCounter(ctx, meters, pol.Name, target.Name, attemptOutcomeCBBlocked)
			continue
		}

		clone, err := buildAttemptState(ctx, state, target)
		if err != nil {
			logger.ErrorContext(ctx, "resilience: target action failed",
				slog.String("policy", pol.Name),
				slog.String("target", target.Name),
				slog.Int("attempt", attempts+1),
				slog.Any("error", err),
			)
			finalStatus = http.StatusInternalServerError
			orchestratorOutcome = orchestratorOutcomeAllFailed
			http.Error(w, "internal error", finalStatus)
			return
		}

		if rawBody != nil {
			bodycapture.ApplyBodyBytes(r, rawBody)
		}

		retrySet := effectiveFailureStatusCodes(pol, target)
		buf := proxy.NewBufferingResponseWriter(w, retrySet)

		attemptCtx := rules.WithMutableState(ctx, clone)
		attempts++
		realAttempts = attempts
		attemptStart := time.Now()
		logger.DebugContext(ctx, "resilience: failover attempt",
			slog.String("policy", pol.Name),
			slog.String("target", target.Name),
			slog.Int("attempt", attempts),
			slog.Int("of", len(targets)),
		)

		next.ServeHTTP(buf, r.WithContext(attemptCtx))

		record := newAttemptRecord(target.Name, attemptStart, buf)
		emitAttemptDuration(ctx, meters, pol.Name, target.Name, record.DurationMs)

		if !buf.ShouldRetry() {
			breakers.RecordSuccess(pol.Name, target.Name, cb)
			record.Outcome = attemptOutcomeSuccess
			abuf.Record(record)
			emitAttemptCounter(ctx, meters, pol.Name, target.Name, attemptOutcomeSuccess)
			finalStatus = buf.StatusCode()
			orchestratorOutcome = orchestratorOutcomeSuccess
			return
		}

		breakers.RecordFailure(pol.Name, target.Name, cb)
		if record.Error != "" {
			record.Outcome = attemptOutcomeTransportError
		} else {
			record.Outcome = attemptOutcomeFailureStatus
		}
		abuf.Record(record)
		emitAttemptCounter(ctx, meters, pol.Name, target.Name, record.Outcome)

		lastStatus = buf.StatusCode()
		lastErr = buf.TransportError()
		logger.InfoContext(ctx, "resilience: failover attempt failed",
			slog.String("policy", pol.Name),
			slog.String("target", target.Name),
			slog.Int("attempt", attempts),
			slog.Int("status", lastStatus),
			slog.Any("transport_error", lastErr),
		)
	}

	// All targets exhausted (or every one was CB-blocked).
	status := lastStatus
	if status == 0 {
		status = http.StatusBadGateway
	}
	if attempts == 0 {
		// Every target was CB-blocked — service-unavailable signals
		// "no healthy provider" more honestly than 502.
		status = http.StatusServiceUnavailable
		orchestratorOutcome = orchestratorOutcomeAllOpen
	} else {
		orchestratorOutcome = orchestratorOutcomeAllFailed
	}
	finalStatus = status
	logger.ErrorContext(ctx, "resilience: failover exhausted",
		slog.String("policy", pol.Name),
		slog.Int("attempts", attempts),
		slog.Int("cb_blocked", totalTargets-attempts),
		slog.Int("final_status", status),
	)
	http.Error(w, http.StatusText(status), status)
}

// runLoadBalance picks a target via weighted-random selection from
// the policy's targets. On each iteration the selector reads from
// the remaining pool (initially every target; one less per failed
// attempt). The first non-retryable outcome commits; a retryable
// outcome shrinks the pool and re-rolls — LBWF semantics by default.
//
// When pol.StrictWeights is true the loop runs at most once: the
// first weighted-random pick wins or fails, no re-roll. Used for
// canary mirroring where suppressing the under-weighted target's
// failures would defeat the purpose.
//
// All-targets-exhausted writes the last attempt's status to the
// client (or 502 if every attempt was a transport error). Identical
// terminal handling to runFailover.
func runLoadBalance(
	w http.ResponseWriter,
	r *http.Request,
	pol *contractsres.ResilienceConfig,
	state *rules.MutableState,
	breakers BreakerStore,
	meters *observability.Meters,
	next http.Handler,
) {
	ctx := r.Context()
	logger := observability.FromContext(ctx)

	orchStart := time.Now()
	abuf := NewAttemptBuffer(pol.Name)
	ctx = WithAttemptBuffer(ctx, abuf)
	ctx = withPolicyResponseHeaderTimeout(ctx, pol)
	r = r.WithContext(ctx)

	var finalStatus int
	var orchestratorOutcome string
	var realAttempts int
	defer func() {
		abuf.FireTerminal(time.Since(orchStart).Milliseconds(), finalStatus)
		emitOrchestratorOutcome(ctx, meters, pol.Name, orchestratorOutcome, realAttempts)
	}()

	// Pre-filter the pool by CB.Allow. CB-blocked targets shrink
	// the pool silently — they never count as an attempt and don't
	// trigger strict_weights' no-re-roll path. They DO emit a
	// cb_blocked AttemptRecord so the consolidated event surfaces
	// the breaker decision the same way failover does.
	pool := make([]contractsres.ResilienceTarget, 0, len(pol.Targets))
	cbBlocked := 0
	for _, t := range pol.Targets {
		cb := effectiveCircuitBreaker(pol, t)
		if !breakers.Allow(pol.Name, t.Name, cb) {
			cbBlocked++
			abuf.Record(events.AttemptRecord{
				Target:    t.Name,
				StartedAt: time.Now().UTC(),
				Outcome:   attemptOutcomeCBBlocked,
			})
			emitAttemptCounter(ctx, meters, pol.Name, t.Name, attemptOutcomeCBBlocked)
			continue
		}
		pool = append(pool, t)
	}

	rawBody := capturedRawBody(ctx)

	var lastStatus int
	var lastErr error
	attempt := 0

	for len(pool) > 0 {
		attempt++
		// weightedSelect only returns -1 for an empty pool, which the
		// loop guard already excludes, so idx is always a valid index.
		idx := weightedSelect(pool)
		target := pool[idx]
		cb := effectiveCircuitBreaker(pol, target)

		clone, err := buildAttemptState(ctx, state, target)
		if err != nil {
			logger.ErrorContext(ctx, "resilience: target action failed",
				slog.String("policy", pol.Name),
				slog.String("target", target.Name),
				slog.Int("attempt", attempt),
				slog.Any("error", err),
			)
			finalStatus = http.StatusInternalServerError
			orchestratorOutcome = orchestratorOutcomeAllFailed
			http.Error(w, "internal error", finalStatus)
			return
		}

		if rawBody != nil {
			bodycapture.ApplyBodyBytes(r, rawBody)
		}

		retrySet := effectiveFailureStatusCodes(pol, target)
		buf := proxy.NewBufferingResponseWriter(w, retrySet)

		attemptCtx := rules.WithMutableState(ctx, clone)
		attemptStart := time.Now()
		logger.DebugContext(ctx, "resilience: load_balance attempt",
			slog.String("policy", pol.Name),
			slog.String("target", target.Name),
			slog.Int("weight", target.Weight),
			slog.Int("attempt", attempt),
			slog.Int("pool_size", len(pool)),
			slog.Bool("strict_weights", pol.StrictWeights),
		)

		next.ServeHTTP(buf, r.WithContext(attemptCtx))

		record := newAttemptRecord(target.Name, attemptStart, buf)
		emitAttemptDuration(ctx, meters, pol.Name, target.Name, record.DurationMs)
		realAttempts = attempt

		if !buf.ShouldRetry() {
			breakers.RecordSuccess(pol.Name, target.Name, cb)
			record.Outcome = attemptOutcomeSuccess
			abuf.Record(record)
			emitAttemptCounter(ctx, meters, pol.Name, target.Name, attemptOutcomeSuccess)
			finalStatus = buf.StatusCode()
			orchestratorOutcome = orchestratorOutcomeSuccess
			return
		}

		breakers.RecordFailure(pol.Name, target.Name, cb)
		if record.Error != "" {
			record.Outcome = attemptOutcomeTransportError
		} else {
			record.Outcome = attemptOutcomeFailureStatus
		}
		abuf.Record(record)
		emitAttemptCounter(ctx, meters, pol.Name, target.Name, record.Outcome)

		lastStatus = buf.StatusCode()
		lastErr = buf.TransportError()
		logger.InfoContext(ctx, "resilience: load_balance attempt failed",
			slog.String("policy", pol.Name),
			slog.String("target", target.Name),
			slog.Int("attempt", attempt),
			slog.Int("status", lastStatus),
			slog.Any("transport_error", lastErr),
		)

		if pol.StrictWeights {
			break
		}

		pool = append(pool[:idx], pool[idx+1:]...)
	}

	status := lastStatus
	if status == 0 {
		status = http.StatusBadGateway
	}
	if attempt == 0 && cbBlocked > 0 {
		// Every target was CB-blocked — fail with 503 (no healthy
		// provider), the same signal failover uses for the same case.
		status = http.StatusServiceUnavailable
		orchestratorOutcome = orchestratorOutcomeAllOpen
	} else {
		orchestratorOutcome = orchestratorOutcomeAllFailed
	}
	finalStatus = status
	logger.ErrorContext(ctx, "resilience: load_balance exhausted",
		slog.String("policy", pol.Name),
		slog.Int("attempts", attempt),
		slog.Int("cb_blocked", cbBlocked),
		slog.Int("final_status", status),
	)
	http.Error(w, http.StatusText(status), status)
}

// emitAttemptCounter bumps gateway.resilience.attempts.total with the
// resolved per-attempt outcome. Nil meters is a no-op so middleware
// constructed without telemetry (tests) doesn't have to short-circuit
// at every call site.
func emitAttemptCounter(ctx context.Context, meters *observability.Meters, policy, target, outcome string) {
	if meters == nil || meters.ResilienceAttemptsTotal == nil {
		return
	}
	meters.ResilienceAttemptsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("policy", policy),
		attribute.String("target", target),
		attribute.String("outcome", outcome),
	))
}

// emitAttemptDuration observes the per-attempt duration on
// gateway.resilience.attempt.duration. The histogram receives seconds
// (OTel histogram convention) computed from the AttemptRecord's
// millisecond DurationMs.
func emitAttemptDuration(ctx context.Context, meters *observability.Meters, policy, target string, durationMs int64) {
	if meters == nil || meters.ResilienceAttemptDuration == nil {
		return
	}
	meters.ResilienceAttemptDuration.Record(ctx, float64(durationMs)/1000.0, metric.WithAttributes(
		attribute.String("policy", policy),
		attribute.String("target", target),
	))
}

// emitOrchestratorOutcome fires both gateway.resilience.outcome.total
// (counter, one per request) and gateway.resilience.attempts_per_request
// (histogram, one observation per request). Called from runFailover /
// runLoadBalance's deferred terminal cleanup so it sees the final
// outcome string and realised attempt count regardless of which code
// path returned.
func emitOrchestratorOutcome(ctx context.Context, meters *observability.Meters, policy, outcome string, attempts int) {
	if meters == nil || policy == "" {
		return
	}
	if outcome != "" && meters.ResilienceOutcomeTotal != nil {
		meters.ResilienceOutcomeTotal.Add(ctx, 1, metric.WithAttributes(
			attribute.String("policy", policy),
			attribute.String("outcome", outcome),
		))
	}
	if meters.ResilienceAttemptsPerRequest != nil {
		meters.ResilienceAttemptsPerRequest.Record(ctx, int64(attempts), metric.WithAttributes(
			attribute.String("policy", policy),
		))
	}
}

// newAttemptRecord captures the orchestrator's view of one attempt
// after next.ServeHTTP returns. The Outcome field is left blank —
// the caller fills it once it has decided whether the attempt
// committed, failed-status, or transport-errored, because the
// outcome string is part of the resilience contract and bigger
// than what this helper should encode.
func newAttemptRecord(target string, started time.Time, buf *proxy.BufferingResponseWriter) events.AttemptRecord {
	rec := events.AttemptRecord{
		Target:     target,
		StartedAt:  started.UTC(),
		DurationMs: time.Since(started).Milliseconds(),
		StatusCode: buf.StatusCode(),
	}
	if err := buf.TransportError(); err != nil {
		rec.Error = err.Error()
	}
	return rec
}

// withPolicyResponseHeaderTimeout returns ctx carrying the policy's
// ResponseHeaderTimeoutSeconds as a forwarder time-to-first-byte override
// when the policy sets one (> 0), or ctx unchanged otherwise. A resilience
// policy uses this to fail over off a slow target faster than the
// gateway-wide default would allow; the value replaces — and is deliberately
// not floored against — the default, so a load-balance policy may set a short
// timeout on purpose. Stamped once per run (it is policy-wide, identical for
// every attempt) so the attempt contexts derived from ctx inherit it.
func withPolicyResponseHeaderTimeout(ctx context.Context, pol *contractsres.ResilienceConfig) context.Context {
	if pol.ResponseHeaderTimeoutSeconds > 0 {
		return proxy.WithResponseHeaderTimeout(ctx, time.Duration(pol.ResponseHeaderTimeoutSeconds)*time.Second)
	}
	return ctx
}

// effectiveCircuitBreaker resolves the breaker config for one target.
// Per-target CircuitBreaker (when set) overrides policy-level. nil
// from both levels means "no circuit breaker" — the in-memory store
// short-circuits Allow to true and Record* to no-ops, so the
// orchestrator doesn't have to branch on nil at every call site.
func effectiveCircuitBreaker(pol *contractsres.ResilienceConfig, target contractsres.ResilienceTarget) *contractsres.CircuitBreakerConfig {
	if target.CircuitBreaker != nil {
		return target.CircuitBreaker
	}
	return pol.CircuitBreaker
}

// weightedSelect returns the index of a target chosen by weighted-
// random selection from pool. Each target's Weight contributes to a
// cumulative-sum distribution; rand(0, total) picks the slot. A
// Weight <= 0 floors to 1 (even weighting) — the contract documented
// in docs/resilience.md — so a weight-0 target still shares selection
// with the rest of the pool rather than being excluded, and an
// all-zero-weight pool degrades to uniform random.
//
// Returns -1 only when pool is empty (nothing to select). Every
// non-empty pool yields a valid index because the floor guarantees a
// positive total.
//
// Five lines of selection plus the safety check — see the v1.2
// design note for why this is the algorithm we want over
// round-robin (no shared counter across pods, same long-run
// distribution).
func weightedSelect(pool []contractsres.ResilienceTarget) int {
	if len(pool) == 0 {
		return -1
	}
	total := 0
	for _, t := range pool {
		total += effectiveWeight(t.Weight)
	}
	roll := randomIntN(total)
	cumulative := 0
	last := len(pool) - 1
	for i := 0; i < last; i++ {
		cumulative += effectiveWeight(pool[i].Weight)
		if cumulative > roll {
			return i
		}
	}
	// The final slot absorbs the remaining roll: total is the sum of all
	// effective weights and roll < total, so any roll the earlier slots
	// don't claim belongs to the last one. No unreachable return needed.
	return last
}

// effectiveWeight floors a target weight to 1, so weight-0 (or unset/
// negative) targets contribute even weighting rather than being
// excluded from the roll. See docs/resilience.md and issue #361.
func effectiveWeight(w int) int {
	if w < 1 {
		return 1
	}
	return w
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
