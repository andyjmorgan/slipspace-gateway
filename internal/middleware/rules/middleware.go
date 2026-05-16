package rules

import (
	"context"
	"net/http"
	"strings"
	"time"

	contractsrules "github.com/andyjmorgan/sluice-gateway/contracts/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/auth"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/bodycapture"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/proxy"
)

// MatchFromContextFunc returns the routed (provider, endpoint, path
// params) the routing middleware stashed on the request context.
// Injected via the constructor so this package does not import
// internal/routing (avoids a potential cycle and lets tests stub
// without a real router).
type MatchFromContextFunc func(ctx context.Context) (provider string, endpoint string, pathParams map[string]string, ok bool)

// HTTPHandler runs the rule engine between bodycapture and the
// downstream final handler.
//
// For each request it:
//
//  1. Installs a MatchBuffer on context for the evaluator to push
//     match records into.
//  2. Builds GatewayContext from the routed match + AuthResult +
//     Captured body.
//  3. Builds MutableState from the routed (provider, endpoint, path
//     params) and inbound headers, then runs Evaluate against the
//     Configuration's rule slice.
//  4. Stashes the resulting MutableState on context so the final
//     handler can build the destination from the post-rule state.
//
// Empty rule set (no Configuration match, or a Configuration with no
// rule_names) is byte-identical to the v1.0 path: the evaluator
// returns immediately and the unchanged state flows downstream.
func HTTPHandler(eval *Evaluator, matchFrom MatchFromContextFunc, observerFactory proxy.ObserverFactory, next http.Handler) http.Handler {
	if eval == nil {
		panic("rules: HTTPHandler called with nil evaluator")
	}
	if matchFrom == nil {
		panic("rules: HTTPHandler called with nil matchFrom")
	}
	if next == nil {
		panic("rules: HTTPHandler called with nil next handler")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		logger := observability.FromContext(ctx)

		provider, endpoint, params, ok := matchFrom(ctx)
		if !ok {
			logger.ErrorContext(ctx, "rules: no route on context")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		ar, _ := auth.FromContext(ctx)
		captured, _ := bodycapture.FromContext(ctx)

		ctx, _ = WithMatchBuffer(ctx)

		state := NewMutableState(provider, endpoint, params, r.Header)
		gc := GatewayContext{
			Provider:          provider,
			Endpoint:          endpoint,
			Model:             extractInboundModel(captured, params),
			Headers:           r.Header,
			ConfigurationName: ar.ConfigurationName,
			Body:              captured.Body,
		}

		result, err := eval.Evaluate(ctx, gc, state, captured.Body)
		if err != nil {
			// Engine errors today are surfaced via per-match
			// ErrorMessage + the gateway.rule.errors.total counter;
			// Evaluate itself returning err is reserved for future
			// fatal cases. Log and continue so a single broken rule
			// can't take the whole request path down.
			logger.WarnContext(ctx, "rules: evaluate", "err", err.Error())
		}

		ctx = WithMutableState(ctx, state)

		if result.Outcome.Terminate && result.Outcome.Response != nil {
			ctx = withSyntheticOutcome(ctx, result)
			ctx = observability.WithRequestLabels(ctx, observability.RequestLabels{
				Provider: state.Provider,
				Endpoint: state.Endpoint,
				Model:    extractInboundModel(captured, state.PathParams),
			})
			driveSyntheticLifecycle(ctx, w, result, observerFactory)
			return
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// driveSyntheticLifecycle writes the synthetic response and runs
// the per-request reporter observer lifecycle inline — the
// forwarder never sees this request, but the gateway.request event
// must still fire with the rule-derived status code so dashboards
// and billing see a uniform stream regardless of synthetic vs
// upstream-served responses.
//
// The factory is invoked with an empty Destination because the rule
// short-circuited before the destination was finalised; the
// observer's metric labels come from request-level context labels
// the cmd/gateway handler sets before invoking the chain, so the
// missing destination is not load-bearing.
func driveSyntheticLifecycle(ctx context.Context, w http.ResponseWriter, result Result, factory proxy.ObserverFactory) {
	resp := result.Outcome.Response

	start := time.Now()
	var observer proxy.Observer
	if factory != nil {
		observer = factory(ctx, proxy.Destination{})
		observer.OnRequestStart(ctx, proxy.Destination{})
	}

	headers := http.Header{}
	headers.Set("Content-Type", contentTypeFor(resp.BodyType))
	if result.SourceRule != nil {
		headers.Set("X-Sluice-Synthetic", "rule:"+result.SourceRule.Name)
	}
	if id := observability.CorrelationIDFromContext(ctx); id != "" {
		headers.Set("X-Sluice-Correlation-Id", id)
	}

	if observer != nil {
		observer.OnResponseHeaders(ctx, resp.StatusCode, headers, false)
	}

	for k, v := range headers {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	if len(resp.Body) > 0 {
		_, _ = w.Write(resp.Body)
	}

	if observer != nil {
		observer.OnComplete(ctx, resp.StatusCode, time.Since(start).Milliseconds())
	}
}

func contentTypeFor(bt contractsrules.StatusCodeBodyType) string {
	switch bt {
	case contractsrules.StatusBodyJSON:
		return "application/json"
	case contractsrules.StatusBodyHTML:
		return "text/html; charset=utf-8"
	case contractsrules.StatusBodyText:
		return "text/plain; charset=utf-8"
	default:
		return "text/plain; charset=utf-8"
	}
}

// extractInboundModel reads the inbound model for condition matching.
// Resolution order mirrors cmd/gateway.outboundModel:
//
//  1. routing's {model} path param (Gemini puts the model in the
//     URL).
//  2. the decoded typed body's Model field, when the body carries
//     one.
//  3. empty — passthrough endpoints have no model in scope.
//
// This is the value rules SEE; mutations done by rules update both
// the typed body (for chat/responses/messages) and PathParams (for
// gemini) — see applyChangeModelName.
func extractInboundModel(captured bodycapture.Captured, params map[string]string) string {
	if v := strings.TrimSpace(params["model"]); v != "" {
		return v
	}
	return bodycapture.Model(captured.Body)
}

type mutableStateKey struct{}

type syntheticOutcomeKey struct{}

// WithMutableState attaches a MutableState to ctx. Exported so
// downstream middleware (notably the body re-marshal step) can be
// tested in isolation by installing a state directly rather than
// re-running the full HTTPHandler chain.
func WithMutableState(ctx context.Context, s *MutableState) context.Context {
	return context.WithValue(ctx, mutableStateKey{}, s)
}

// withSyntheticOutcome stashes a terminating Result on ctx so the
// per-request reporter observer can read the rule-derived status
// code and record it on gateway.request. The middleware writes the
// outcome here right before the synthetic response goes out so the
// reporter's OnComplete (invoked from the same goroutine after the
// write) sees the correct status.
func withSyntheticOutcome(ctx context.Context, r Result) context.Context {
	return context.WithValue(ctx, syntheticOutcomeKey{}, r)
}

// SyntheticOutcomeFromContext returns the terminating Result the
// rules middleware wrote, or the zero value when the request was
// forwarded normally. The reporter uses this to override
// status_code on the gateway.request event for synthetic
// responses.
func SyntheticOutcomeFromContext(ctx context.Context) (Result, bool) {
	if ctx == nil {
		return Result{}, false
	}
	r, ok := ctx.Value(syntheticOutcomeKey{}).(Result)
	return r, ok
}

// MutableStateFromContext returns the post-rule MutableState the
// HTTPHandler stashed on ctx, or nil if rules did not run. The final
// handler reads this to build the upstream destination.
func MutableStateFromContext(ctx context.Context) *MutableState {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(mutableStateKey{}).(*MutableState)
	return s
}
