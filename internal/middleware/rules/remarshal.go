package rules

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/bodycapture"
	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
)

// BodyRemarshalHandler sits between the rules middleware and the
// downstream forwarder. It re-encodes the typed request body when a
// rule mutated it (state.BodyMutated true) and replaces r.Body +
// Content-Length on the outgoing request. Otherwise it is a no-op —
// the forwarder reads the unchanged raw bytes.
//
// The actual marshal + apply is delegated to bodycapture.RemarshalTyped
// and bodycapture.ApplyBodyBytes so the v1.2 resilience orchestrator
// can reuse the same primitives per attempt without duplicating the
// content-length and reader-replacement logic.
//
// Marshal failures surface as a 500 response and increment
// gateway.rule.errors.total{error_kind="body_remarshal"}; the typed
// body's MarshalJSON is responsible for preserving DynamicProperties
// (the providers/* packages already do).
func BodyRemarshalHandler(meters *observability.Meters, next http.Handler) http.Handler {
	if next == nil {
		panic("rules: BodyRemarshalHandler called with nil next handler")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		state := MutableStateFromContext(ctx)
		if state == nil || !state.BodyMutated {
			next.ServeHTTP(w, r)
			return
		}

		newBytes, err := bodycapture.RemarshalTyped(ctx)
		if err != nil {
			recordRemarshalError(ctx, meters)
			logger := observability.FromContext(ctx)
			logger.ErrorContext(ctx, "rules: body remarshal", "err", err.Error())
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if newBytes == nil {
			// No typed body captured (admin route / unknown endpoint).
			next.ServeHTTP(w, r)
			return
		}

		bodycapture.ApplyBodyBytes(r, newBytes)
		next.ServeHTTP(w, r)
	})
}

func recordRemarshalError(ctx context.Context, meters *observability.Meters) {
	if meters == nil || meters.RuleErrorsTotal == nil {
		return
	}
	meters.RuleErrorsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("error_kind", "body_remarshal"),
	))
}
