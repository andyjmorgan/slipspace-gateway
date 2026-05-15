package rules

import (
	"context"
	"net/http"
	"strings"

	"github.com/andyjmorgan/sluice-gateway/internal/middleware/auth"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/bodycapture"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/providers/anthropic/messages"
	openaichat "github.com/andyjmorgan/sluice-gateway/providers/openai/chat"
	openairesponses "github.com/andyjmorgan/sluice-gateway/providers/openai/responses"
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
func HTTPHandler(eval *Evaluator, matchFrom MatchFromContextFunc, next http.Handler) http.Handler {
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

		if _, err := eval.Evaluate(ctx, gc, state, captured.Body); err != nil {
			// Engine errors today are surfaced via per-match
			// ErrorMessage + the gateway.rule.errors.total counter;
			// Evaluate itself returning err is reserved for future
			// fatal cases. Log and continue so a single broken rule
			// can't take the whole request path down.
			logger.WarnContext(ctx, "rules: evaluate", "err", err.Error())
		}

		next.ServeHTTP(w, r.WithContext(WithMutableState(ctx, state)))
	})
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
	switch b := captured.Body.(type) {
	case *openaichat.ChatCompletionRequest:
		return strings.TrimSpace(b.Model)
	case *openairesponses.ResponsesRequest:
		return strings.TrimSpace(b.Model)
	case *messages.MessagesRequest:
		return strings.TrimSpace(b.Model)
	}
	return ""
}

type mutableStateKey struct{}

// WithMutableState attaches a MutableState to ctx. Exported so
// downstream middleware (notably the body re-marshal step) can be
// tested in isolation by installing a state directly rather than
// re-running the full HTTPHandler chain.
func WithMutableState(ctx context.Context, s *MutableState) context.Context {
	return context.WithValue(ctx, mutableStateKey{}, s)
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
